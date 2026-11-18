package helper

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/ipc"
	"github.com/synopackageland/syno-pkg-land-sdk/supervision"
)

func setParentDeath() error {
	_, _, err := syscall.Syscall6(syscall.SYS_PRCTL, syscall.PR_SET_PDEATHSIG, uintptr(syscall.SIGKILL), 0, 0, 0, 0)
	if err != 0 {
		return err
	}
	return nil
}

type ownedLinuxChild struct {
	process *os.Process
	reaped  bool
}

func (c *ownedLinuxChild) exited() (bool, error) {
	if c.reaped {
		return true, nil
	}
	var status syscall.WaitStatus
	ready, err := supervision.GroupExited(c.process.Pid)
	if err != nil {
		return false, err
	}
	if !ready {
		return false, nil
	}
	pid, err := syscall.Wait4(c.process.Pid, &status, syscall.WNOHANG, nil)
	if errors.Is(err, syscall.EINTR) {
		return false, nil
	}
	if err != nil {
		return false, err
	} // ECHILD never permits signalling.
	if pid == c.process.Pid {
		c.reaped = true
		c.process.Release()
	}
	return c.reaped, nil
}
func (c *ownedLinuxChild) signal(s syscall.Signal) error {
	if c.reaped {
		return errTrust
	}
	// This PID came from our own Start, and ONLY this event loop calls Wait4.
	// Even if it exits here it remains an unreaped zombie, pinning its PID.
	return syscall.Kill(-c.process.Pid, s)
}

type supervisorRequest struct {
	Process Process `json:"process"`
	Op      string  `json:"op"`
	Nonce   string  `json:"nonce"`
}

type supervisorStop struct {
	conn  net.Conn
	nonce string
}

// The thread creating the child must remain alive: PDEATHSIG is tied to that
// Linux thread, not merely the supervisor's thread group / Go process.
func superviseLinux(c Client, binary string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	r, err := readRecord(c.StateDir, 0)
	if err != nil || !r.Enabled || r.Inventory != c.Expected {
		return errTrust
	}
	identity, err := inspectSupervisor(os.Getpid(), c.Expected)
	if err != nil {
		return err
	}
	if err := writeProcess(c.RunDir, identity); err != nil {
		return err
	}
	socket := filepath.Join(c.RunDir, "supervisor.sock")
	requests := make(chan supervisorStop)
	server, err := ipc.StartPeerUnix(context.Background(), socket, ipc.PeerPolicy{UID: 0, GID: 0},
		func(ctx context.Context, conn net.Conn, _ ipc.PeerCredentials) {
			conn.SetDeadline(time.Now().Add(3 * time.Second))
			var req supervisorRequest
			d := json.NewDecoder(io.LimitReader(conn, 4097))
			d.DisallowUnknownFields()
			if d.Decode(&req) != nil || req.Process != identity || req.Op != "stop" || len(req.Nonce) != 64 || !digestPattern.MatchString(req.Nonce) {
				conn.Close()
				return
			}
			// Transfer the verified connection to the child-owning OS thread.
			// StartPeerUnix retains it when this callback returns.
			select {
			case requests <- supervisorStop{conn: conn, nonce: req.Nonce}:
			case <-ctx.Done():
				conn.Close()
			}
		})
	if err != nil {
		return err
	}
	defer server.Close()
	serving := make(chan error, 1)
	go func() { serving <- server.Wait() }()
	cmd := exec.Command(binary, "serve-child")
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "SYNOPKGLAND_PARENT=" + strconv.Itoa(os.Getpid())}
	// Go sets prctl before exec and rechecks the parent immediately afterward.
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL, Setpgid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	child := &ownedLinuxChild{process: cmd.Process}
	policy := supervision.New(supervision.Defaults(), nil)
	policy.Started(time.Now())
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	// No cmd.Wait goroutine: reaping and signalling are serialized below.
	for {
		select {
		case err := <-serving:
			return err
		case <-tick.C:
			exited, err := child.exited()
			if err != nil {
				return err
			}
			// Protected helper authority is not renewed by a restart loop.
			// Retain the authenticated stop socket, with no idle timer once reaped.
			if exited {
				policy.Exited(time.Now(), supervision.Exit{Code: -1, Reason: "protected_helper_exit", Permanent: true})
				tick.Stop()
			}
		case request := <-requests:
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			err := stopChild(ctx, child, time.Second)
			cancel()
			stopped := err == nil
			// Keep peers/context alive until ACK and process exit. Close or
			// cancellation here would create EOF before supervisor exit.
			if stopped {
				err = server.StopAccepting()
			}
			res := Response{Inventory: c.Expected, Nonce: request.nonce, OK: err == nil}
			if err != nil {
				res.Error = "cleanup-required"
			}
			if writeErr := json.NewEncoder(request.conn).Encode(res); writeErr != nil {
				request.conn.Close()
				return writeErr
			}
			if err == nil {
				// A full ACK plus real EOF certifies reap, admission stop and exit.
				os.Exit(0)
			}
			request.conn.Close()
			if stopped {
				return err
			}
		}
	}
}

func (p LinuxProcesses) Stop(want Process) error {
	if want.Mode != "supervised" {
		return errTrust
	}
	// Loss/reboot/PID reuse is not sufficient proof of child teardown. Preserve
	// the record for fixed admin repair; never signal this recorded PID.
	actual, err := inspectSupervisor(want.PID, want.Inventory)
	if err != nil || actual != want {
		return errTrust
	}
	socket := filepath.Join(p.RunDir, "supervisor.sock")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := ipc.DialPeerUnix(ctx, socket)
	if err != nil {
		return err
	}
	defer conn.Close()
	unix, ok := conn.(*net.UnixConn)
	if !ok {
		return errTrust
	}
	peer, err := peerCredentials(unix)
	if err != nil || peer.Uid != 0 || int(peer.Pid) != want.PID {
		return errTrust
	}
	// Recheck after binding the connection too. A connected socket cannot be
	// substituted by a later process that reused the path or PID.
	actual, err = inspectSupervisor(want.PID, want.Inventory)
	if err != nil || actual != want {
		return errTrust
	}
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return err
	}
	req := supervisorRequest{Process: want, Op: "stop", Nonce: hex.EncodeToString(nonce[:])}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return err
	}
	return readSupervisorReply(conn, want.Inventory, req.Nonce)
}
