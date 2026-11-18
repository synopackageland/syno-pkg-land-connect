package helper

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/ipc"
)

// This opt-in fixture uses a root-only, disposable Linux VM and an independently
// cross-compiled cmd/helper. Never creates accounts or accesses a NAS.
func linuxFixture(t *testing.T) LinuxProcesses {
	t.Helper()
	source := os.Getenv("M1C_HELPER_FIXTURE")
	if source == "" {
		t.Skip("requires disposable Linux root VM and M1C_HELPER_FIXTURE")
	}
	if os.Geteuid() != 0 {
		t.Fatal("VM fixture requires root")
	}
	digest, err := executableDigest(source)
	if err != nil {
		t.Fatal(err)
	}
	i := Inventory{Package: "root", Version: "1.0.0-0001", Protocol: 1, PayloadDigest: digest, RegistryDigest: strings.Repeat("b", 64)}
	c := NewClient(i)
	for _, dir := range []string{c.StateDir, c.RunDir} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(dir) })
	}
	exe := filepath.Join(c.StateDir, "payload-"+digest)
	b, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, b, 0700); err != nil {
		t.Fatal(err)
	}
	return LinuxProcesses{Client: c, Binary: exe}
}
func startVMHelper(t *testing.T, p LinuxProcesses) Process {
	t.Helper()
	ctl := Controller{Client: p.Client, Processes: p}
	if err := ctl.Control(context.Background(), "enable"); err != nil {
		t.Fatal(err)
	}
	record, err := readProcess(p.RunDir, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ctl.Control(context.Background(), "disable") })
	return record
}
func supervisedPID(t *testing.T, pid int) int {
	t.Helper()
	paths, _ := filepath.Glob("/proc/" + strconv.Itoa(pid) + "/task/*/children")
	for _, path := range paths {
		b, _ := os.ReadFile(path)
		for _, s := range strings.Fields(string(b)) {
			child, _ := strconv.Atoi(s)
			if child > 1 {
				return child
			}
		}
	}
	t.Fatal("supervisor has no child")
	return 0
}

// Observation only: an absent process or zombie has exited. No signal is sent
// to this PID, including if it has been reused. These fixtures run in a fresh VM.
func waitPIDExit(t *testing.T, pid int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if waitHealthy(ctx, func(context.Context) bool {
		b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if os.IsNotExist(err) {
			return true
		}
		end := strings.LastIndex(string(b), ") ")
		return err == nil && end >= 0 && strings.HasPrefix(string(b)[end+2:], "Z ")
	}) != nil {
		t.Fatal("owned process survived")
	}
}
func TestLinuxActivationAlwaysUsesSupervisorAndNormalStop(t *testing.T) {
	p := linuxFixture(t)
	for n := 0; n < 20; n++ {
		t.Run("cycle-"+strconv.Itoa(n), func(t *testing.T) {
			proc := startVMHelper(t, p)
			if proc.Mode != "supervised" {
				t.Fatalf("activation did not select supervisor: %+v", proc)
			}
			child := supervisedPID(t, proc.PID)
			if p.Status(context.Background()).State != "ready" {
				t.Fatal("missing live handshake")
			}
			if err := (Controller{Client: p.Client, Processes: p}).Control(context.Background(), "disable"); err != nil {
				t.Fatal(err)
			}
			waitPIDExit(t, child)
			if err := p.CheckStopped(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLinuxHelperSelfExitIsReapedWithoutSignallingReusedPID(t *testing.T) {
	p := linuxFixture(t)
	proc := startVMHelper(t, p)
	if proc.Mode != "supervised" {
		t.Fatal("not supervised")
	}
	child := supervisedPID(t, proc.PID)
	// The VM-only helper fixture exits itself when this root-owned marker exists.
	if err := os.WriteFile(filepath.Join(p.RunDir, "fixture-exit"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	waitPIDExit(t, child)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if waitHealthy(ctx, func(context.Context) bool {
		_, err := os.Stat("/proc/" + strconv.Itoa(child))
		return os.IsNotExist(err)
	}) != nil {
		t.Fatal("supervisor did not reap")
	}
	if err := p.Stop(proc); err != nil {
		t.Fatal(err)
	}
}
func TestLinuxSupervisorDeathKillsHelperAndPreservesFailedCleanup(t *testing.T) {
	p := linuxFixture(t)
	record(t, p.Client, Record{Inventory: p.Expected, Enabled: true})
	// Keep the supervisor as our own unreaped child; only its parent signals it.
	cmd := exec.Command(p.Binary, "supervise")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	owned := &ownedLinuxChild{process: cmd.Process}
	t.Cleanup(func() { stopChild(context.Background(), owned, time.Millisecond) })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	err := waitHealthy(ctx, func(ctx context.Context) bool { return p.Status(ctx).State == "ready" })
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	proc, err := readProcess(p.RunDir, 0)
	if err != nil || proc.Mode != "supervised" || proc.PID != cmd.Process.Pid {
		t.Fatalf("%+v %v", proc, err)
	}
	child := supervisedPID(t, proc.PID)
	if err := owned.signal(syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitPIDExit(t, child)
	waitPIDExit(t, proc.PID)
	ctl := Controller{Client: p.Client, Processes: p}
	if ctl.Control(context.Background(), "teardown") == nil {
		t.Fatal("lost supervisor falsely cleaned")
	}
	s := p.Status(context.Background())
	if s.State != "failed" || s.Cleanup != "required" {
		t.Fatal(s)
	}
	if ctl.Control(context.Background(), "disable") == nil {
		t.Fatal("lost supervisor falsely disabled cleanly")
	}
	r, err := readRecord(p.StateDir, 0)
	if err != nil || r.Enabled || !r.Residual {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := os.Stat(filepath.Join(p.RunDir, "process.json")); err != nil {
		t.Fatal("failure evidence removed")
	}
}
func TestLinuxUnsupervisedRecordAndDirectServeRefuse(t *testing.T) {
	p := linuxFixture(t)
	proc := startVMHelper(t, p)
	for _, mode := range []string{"", "unknown"} {
		foreign := proc
		foreign.Mode = mode
		if p.Stop(foreign) == nil {
			t.Fatal("unsupervised record accepted")
		}
	}
	// Remove stopped sockets before testing the retired action, so a bind
	// conflict cannot masquerade as refusal of unsupervised startup.
	if err := (Controller{Client: p.Client, Processes: p}).Control(context.Background(), "teardown"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, p.Binary, "serve")
	if cmd.Run() == nil || ctx.Err() != nil {
		t.Fatal("direct unsupervised serve accepted")
	}
	if _, err := os.Stat(filepath.Join(p.RunDir, "helper.sock")); !os.IsNotExist(err) {
		t.Fatal("direct serve opened socket")
	}
}

func TestLinuxOwnedChildSelfExitAndECHILDRefusal(t *testing.T) {
	for _, externallyReaped := range []bool{false, true} {
		cmd := exec.Command("/bin/sh", "-c", "exit 0")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		child := &ownedLinuxChild{process: cmd.Process}
		if externallyReaped {
			if err := cmd.Wait(); err != nil {
				t.Fatal(err)
			}
			if err := stopChild(context.Background(), child, time.Millisecond); !errors.Is(err, syscall.ECHILD) {
				t.Fatal("ECHILD did not refuse", err)
			}
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			err := waitHealthy(ctx, func(context.Context) bool { done, err := child.exited(); return done && err == nil })
			cancel()
			if err != nil {
				t.Fatal(err)
			}
			if err := stopChild(context.Background(), child, time.Millisecond); err != nil {
				t.Fatal(err)
			}
			if child.signal(syscall.SIGTERM) == nil {
				t.Fatal("reaped PID signalled")
			}
		}
	}
}
func TestLinuxSupervisorIdentityConflictsAndSocketSubstitutionRefuse(t *testing.T) {
	p := linuxFixture(t)
	proc := startVMHelper(t, p)
	for _, field := range []string{"boot", "start", "inode", "digest", "mode"} {
		foreign := proc
		switch field {
		case "boot":
			foreign.Boot = "reboot"
		case "start":
			foreign.Start = "reuse"
		case "inode":
			foreign.Inode++
		case "digest":
			foreign.Inventory.PayloadDigest = strings.Repeat("c", 64)
		case "mode":
			foreign.Mode = "unknown"
		}
		if p.Stop(foreign) == nil {
			t.Fatal("foreign supervisor accepted", field)
		}
	}
	socket := filepath.Join(p.RunDir, "supervisor.sock")
	old := socket + ".owned"
	if err := os.Rename(socket, old); err != nil {
		t.Fatal(err)
	}
	fake, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(socket, 0660); err != nil {
		t.Fatal(err)
	}
	if p.Stop(proc) == nil {
		t.Fatal("other root PID substituted supervisor")
	}
	fake.Close()
	if err := os.Rename(old, socket); err != nil {
		t.Fatal(err)
	}
	if p.Status(context.Background()).State != "ready" {
		t.Fatal("refusal stopped real helper")
	}
	if err := p.Stop(proc); err != nil {
		t.Fatal(err)
	}
}
func TestLinuxReparentedChildRefusesBeforeSocket(t *testing.T) {
	p := linuxFixture(t)
	record(t, p.Client, Record{Inventory: p.Expected, Enabled: true})
	cmd := exec.Command(p.Binary, "serve-child")
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "SYNOPKGLAND_PARENT=1"}
	if cmd.Run() == nil {
		t.Fatal("already reparented child served")
	}
	if _, err := os.Stat(filepath.Join(p.RunDir, "helper.sock")); !os.IsNotExist(err) {
		t.Fatal("unsafe child opened socket")
	}
}

// The separately built fixture pauses only the final process exit. All child,
// peer transport, StopAccepting and ACK code is the production implementation.
func TestLinuxSupervisorReapsAndStopsAdmissionBeforeACKAndRetainsUntilExit(t *testing.T) {
	source := os.Getenv("M1C_HELPER_ACK_FIXTURE")
	if source == "" {
		t.Skip("requires disposable root container and delayed-exit helper fixture")
	}
	t.Setenv("M1C_HELPER_FIXTURE", source)
	p := linuxFixture(t)
	record(t, p.Client, Record{Inventory: p.Expected, Enabled: true})
	cmd := exec.Command(p.Binary, "supervise")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := waitHealthy(ctx, func(ctx context.Context) bool { return p.Status(ctx).State == "ready" }); err != nil {
		t.Fatal(err)
	}
	proc, err := readProcess(p.RunDir, 0)
	if err != nil {
		t.Fatal(err)
	}
	child := supervisedPID(t, proc.PID)
	conn, err := ipc.DialPeerUnix(ctx, filepath.Join(p.RunDir, "supervisor.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	nonce := strings.Repeat("c", 64)
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if err := json.NewEncoder(conn).Encode(supervisorRequest{Process: proc, Op: "stop", Nonce: nonce}); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	ack, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatal("missing full ACK", err)
	}
	var response Response
	if json.Unmarshal(ack, &response) != nil || !response.OK || response.Error != "" ||
		response.Nonce != nonce || response.Inventory != p.Expected {
		t.Fatal("invalid ACK", string(ack))
	}
	if _, err := os.Stat("/proc/" + strconv.Itoa(child)); !os.IsNotExist(err) {
		t.Fatal("ACK preceded child reap", err)
	}
	if _, err := os.Lstat(filepath.Join(p.RunDir, "supervisor.sock")); !os.IsNotExist(err) {
		t.Fatal("ACK preceded socket removal", err)
	}
	if late, err := ipc.DialPeerUnix(ctx, filepath.Join(p.RunDir, "supervisor.sock")); err == nil {
		late.Close()
		t.Fatal("admission survived ACK")
	}
	if _, err := inspectSupervisor(proc.PID, p.Expected); err != nil {
		t.Fatal("fixture exited before retention check", err)
	}
	conn.SetReadDeadline(time.Now().Add(40 * time.Millisecond))
	if _, err := reader.ReadByte(); err == nil {
		t.Fatal("unexpected bytes after ACK")
	} else if timeout, ok := err.(net.Error); !ok || !timeout.Timeout() {
		t.Fatal("connection closed before process exit", err)
	}
	if err := os.WriteFile(filepath.Join(p.RunDir, "fixture-allow-exit"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	conn.SetReadDeadline(time.Now().Add(time.Second))
	if rest, err := io.ReadAll(reader); err != nil || len(rest) != 0 {
		t.Fatal("missing real EOF after exit", string(rest), err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
	t.Log("verified child reap -> admission stopped/socket removed -> complete ACK -> retained connection -> process exit/EOF")
}
