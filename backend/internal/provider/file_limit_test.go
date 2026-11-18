package provider

import (
	"dsmapp/internal/transient"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProviderFileLimitHelper(t *testing.T) {
	if os.Getenv("CONNECT_LIMIT_HELPER") != "1" {
		return
	}
	if err := ExecLimited([]string{"/bin/sh", "-c", `exec dd if=/dev/zero of="$CONNECT_LIMIT_OUTPUT" bs=65536 count=64 2>/dev/null`}); err != nil {
		os.Exit(92)
	}
	os.Exit(93)
}

func TestProviderKernelFileCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.tmp")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, "-test.run=^TestProviderFileLimitHelper$")
	cmd.Env = append(os.Environ(), "CONNECT_LIMIT_HELPER=1", "CONNECT_LIMIT_OUTPUT="+path)
	if err := cmd.Run(); err == nil {
		t.Fatal("oversized write succeeded")
	}
	st, err := os.Stat(path)
	if err != nil || st.Size() != ProviderFileLimit {
		t.Fatalf("kernel ceiling: %v %v", st, err)
	}
}

func TestProductionNetBirdRuntimeLimits(t *testing.T) {
	dir := t.TempDir()
	nb, err := netbirdRuntimeCommands(dir)
	if err != nil || !nb.LimitFiles {
		t.Fatal("NetBird lacks hard file ceiling", err)
	}
	defer transient.Clear(dir, "netbird")
	cmd := nb.command("/fixture/netbird", "service", "run")
	if len(cmd.Args) < 3 || cmd.Args[1] != "provider-child" || cmd.Args[2] != "/fixture/netbird" {
		t.Fatal("official binary bypassed trampoline:", cmd.Args)
	}
	if !strings.Contains(strings.Join(cmd.Env, "\n"), "TMPDIR=/tmp/SynoPkgConnect-") || !strings.Contains(strings.Join(cmd.Env, "\n"), "XDG_CONFIG_HOME=/tmp/SynoPkgConnect-") {
		t.Fatal("unowned temporary files")
	}
}

func TestProviderStopClearsTransientButKeepsIdentity(t *testing.T) {
	for _, kind := range []string{"tailscale", "netbird"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			identity := filepath.Join(dir, "identity")
			os.WriteFile(identity, []byte("retained"), 0600)
			runtime, err := transient.For(dir, kind, true)
			if err != nil {
				t.Fatal(err)
			}
			os.WriteFile(filepath.Join(runtime, "state.json.corrupted.1"), []byte("stale"), 0600)
			monitor := watchRuntime(runtime, kind, runtimeSweep, func() error {
				t.Error("writer stopped by monitor during normal shutdown")
				return nil
			}, nil)
			var stop func() error
			if kind == "tailscale" {
				s := &tailscaleExecSession{dir: dir, socket: filepath.Join(dir, "socket"), daemon: &execFixtureProcess{}, storage: monitor}
				stop = s.Stop
			} else {
				s := &netbirdExecSession{dir: dir, socket: "unix://" + filepath.Join(dir, "socket"), daemon: &execFixtureProcess{}, storage: monitor}
				stop = s.Stop
			}
			if err := stop(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-monitor.done:
			default:
				t.Fatal("stop left runtime monitor running")
			}
			if _, err := os.Stat(runtime); !os.IsNotExist(err) {
				t.Fatal("retained transient data", err)
			}
			if raw, err := os.ReadFile(identity); err != nil || string(raw) != "retained" {
				t.Fatal("identity lost", err)
			}
		})
	}
}

func TestTailscaleDisablesUnboundedTaildropAndDiskLogs(t *testing.T) {
	dir := t.TempDir()
	c, err := tailscaleRuntimeCommands(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer transient.Clear(dir, "tailscale")
	env := strings.Join(c.command("/fixture/tailscaled").Env, "\n")
	if !c.LimitFiles || !strings.Contains(env, "TS_DISABLE_TAILDROP=true") || !strings.Contains(env, "TS_USE_CACHED_NETMAP=false") || !strings.Contains(env, "TS_NO_LOGS_NO_SUPPORT=true") || !strings.Contains(env, "TS_LOGS_DIR=/tmp/SynoPkgConnect-") {
		t.Fatal("unbounded Tailscale storage", env)
	}
}
