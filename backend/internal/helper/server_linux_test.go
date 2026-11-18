package helper

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// This fixture runs only in an explicitly selected disposable root container.
// The test executable is bound as the approved executor tier for this package.
func TestPeerTransportHealthAndCurrentApproval(t *testing.T) {
	i := Inventory{Package: "SynoPkgConnect", Version: "1.0.0-0001", Protocol: 1,
		PayloadDigest: strings.Repeat("a", 64), RegistryDigest: strings.Repeat("b", 64)}
	c := NewClient(i)
	if role := os.Getenv("CONNECT_PEER_CHILD"); role != "" {
		c.StateDir = os.Getenv("CONNECT_PEER_STATE")
		if role == "serve" {
			t.Fatal(serveLinux(c))
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		state, reason := c.probe(ctx, filepath.Join(c.RunDir, "helper.sock"))
		if (state == "ready") != (role == "ready") {
			t.Fatalf("probe: %s %s, expected %s", state, reason, role)
		}
		return
	}
	if os.Getenv("CONNECT_PEER_FIXTURE") != "1" || os.Geteuid() != 0 {
		t.Skip("requires disposable root Linux container with executor binding")
	}
	dir, err := os.MkdirTemp("/tmp", "connect-peer-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	c.StateDir = dir
	if err := writeProtected(dir, "approval.json", Record{Inventory: i, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	exe = filepath.Join(dir, "peer-fixture")
	if err := os.WriteFile(exe, data, 0755); err != nil {
		t.Fatal(err)
	}
	command := func(role string) *exec.Cmd {
		cmd := exec.Command(exe, "-test.run=^TestPeerTransportHealthAndCurrentApproval$", "-test.v")
		cmd.Env = append(os.Environ(), "CONNECT_PEER_CHILD="+role, "CONNECT_PEER_STATE="+dir)
		return cmd
	}
	server := command("serve")
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		server.Process.Kill()
		server.Wait()
		for _, name := range []string{"helper.sock", "helper-root.sock"} {
			os.Remove(filepath.Join(c.RunDir, name))
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := waitHealthy(ctx, func(ctx context.Context) bool {
		state, _ := c.probe(ctx, filepath.Join(c.RunDir, "helper.sock"))
		return state == "ready"
	}); err != nil {
		t.Fatal("root lifecycle health channel did not start", err)
	}
	probe := func(uid, gid uint32, role string) {
		t.Helper()
		cmd := command(role)
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uid, Gid: gid}}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("uid %d: %v\n%s", uid, err, out)
		}
	}
	probe(0, 0, "ready")
	probe(1000, 1000, "ready")
	// Same socket group does not confer the selected peer UID.
	probe(1001, 1000, "refused")
	if err := writeProtected(dir, "approval.json", Record{Inventory: i, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	probe(0, 0, "refused")
	probe(1000, 1000, "refused")
}
