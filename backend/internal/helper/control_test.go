package helper

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func staleSocket(t *testing.T, p string) {
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: p, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	l.SetUnlinkOnClose(false)
	l.Close()
}

type fakeProcesses struct {
	current          Process
	stopped, started int
	stopErr          error
}

func (f *fakeProcesses) Stop(want Process) error {
	if f.stopErr != nil {
		return f.stopErr
	}
	if want != f.current {
		return errTrust
	}
	f.stopped++
	return nil
}
func (f *fakeProcesses) Start(Inventory) error { f.started++; return nil }

func controlFixture(t *testing.T) (Controller, *fakeProcesses) {
	c := fixture(t)
	p := Process{Inventory: c.Expected, Mode: "supervised", PID: 1234, Boot: "fixture-boot", Start: "100", Device: 1, Inode: 2}
	f := &fakeProcesses{current: p}
	ctl := Controller{Client: c, Processes: f}
	record(t, c, Record{Inventory: c.Expected, Enabled: true})
	if err := writeProcess(c.RunDir, p); err != nil {
		t.Fatal(err)
	}
	return ctl, f
}

func TestTeardownAndRecoveryVerifyOwnedProcess(t *testing.T) {
	for _, kind := range []string{"pid", "boot", "start", "inode", "digest", "package"} {
		t.Run(kind, func(t *testing.T) {
			c, f := controlFixture(t)
			switch kind {
			case "pid":
				f.current.PID++
			case "boot":
				f.current.Boot = "new-boot"
			case "start":
				f.current.Start = "reused"
			case "inode":
				f.current.Inode++
			case "digest":
				f.current.Inventory.PayloadDigest = "foreign"
			case "package":
				f.current.Inventory.Package = "Other"
			}
			if c.Control(context.Background(), "recover") == nil || f.stopped != 0 || f.started != 0 {
				t.Fatal("foreign process touched")
			}
			if _, err := os.Stat(filepath.Join(c.RunDir, "process.json")); err != nil {
				t.Fatal("failure evidence removed")
			}
		})
	}
	c, f := controlFixture(t)
	if err := c.Control(context.Background(), "teardown"); err != nil {
		t.Fatal(err)
	}
	if f.stopped != 1 || f.started != 0 {
		t.Fatal(f)
	}
	if _, err := os.Stat(filepath.Join(c.RunDir, "process.json")); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := c.Control(context.Background(), "recover"); err != nil || f.started != 1 {
		t.Fatalf("%v %+v", err, f)
	}
}

func TestDisableIsDurableAndPrecedesFailedCleanup(t *testing.T) {
	c, f := controlFixture(t)
	f.stopErr = errors.New("fixture stop failed")
	if c.Control(context.Background(), "disable") == nil {
		t.Fatal("cleanup falsely succeeded")
	}
	r, err := readRecord(c.StateDir, c.Owner)
	if err != nil || r.Enabled || !r.Residual {
		t.Fatalf("%+v %v", r, err)
	}
	s := c.Status(context.Background())
	if s.State != "not-enabled" || s.Cleanup != "required" {
		t.Fatal(s)
	}
	if c.Control(context.Background(), "recover") == nil || f.started != 0 {
		t.Fatal("old recovery resurrected disabled integration")
	}
	f.stopErr = nil
	if err := c.Control(context.Background(), "disable"); err != nil {
		t.Fatal(err)
	}
	r, _ = readRecord(c.StateDir, c.Owner)
	if r.Enabled || r.Residual {
		t.Fatal(r)
	}
	if err := c.Control(context.Background(), "enable"); err != nil || f.started != 1 {
		t.Fatalf("%v %+v", err, f)
	}
}

func TestMissingApprovalAndUnknownActionCannotStart(t *testing.T) {
	c, f := controlFixture(t)
	os.Remove(filepath.Join(c.StateDir, "approval.json"))
	if c.Control(context.Background(), "recover") == nil {
		t.Fatal("recover without approval")
	}
	if c.Control(context.Background(), "arbitrary-command") == nil || f.started != 0 {
		t.Fatal("arbitrary action accepted")
	}
}

func TestSocketResidueCannotUnlinkForeignFilesOrLiveListeners(t *testing.T) {
	for _, kind := range []string{"file", "symlink", "live", "stale"} {
		t.Run(kind, func(t *testing.T) {
			c, f := controlFixture(t)
			os.Remove(filepath.Join(c.RunDir, "process.json"))
			p := filepath.Join(c.RunDir, "helper.sock")
			switch kind {
			case "file":
				os.WriteFile(p, []byte("foreign"), 0600)
			case "symlink":
				os.Symlink("foreign", p)
			case "live":
				live(t, c.Client, c.Expected, nil)
			case "stale":
				staleSocket(t, p)
			}
			err := c.Control(context.Background(), "teardown")
			if kind == "stale" {
				if err != nil {
					t.Fatal(err)
				}
				if _, err := os.Lstat(p); !os.IsNotExist(err) {
					t.Fatal("stale socket not removed")
				}
			} else {
				if err == nil {
					t.Fatal("foreign residue accepted")
				}
				if _, err := os.Lstat(p); err != nil {
					t.Fatal("foreign residue removed")
				}
			}
			if f.stopped != 0 {
				t.Fatal("unrecorded process signalled")
			}
		})
	}
}

func TestLifecycleGateBlocksResidueAndAllowsCleanStoppedState(t *testing.T) {
	c, _ := controlFixture(t)
	if c.CheckStopped(context.Background()) == nil {
		t.Fatal("process residue accepted")
	}
	if err := c.Control(context.Background(), "teardown"); err != nil {
		t.Fatal(err)
	}
	if err := c.CheckStopped(context.Background()); err != nil {
		t.Fatal(err)
	}
	record(t, c.Client, Record{Inventory: c.Expected, Enabled: false, Residual: true})
	if c.CheckStopped(context.Background()) == nil {
		t.Fatal("failed disable accepted")
	}
}

func TestStartupProbesShareOneDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := waitHealthy(ctx, func(probeCtx context.Context) bool {
		if probeCtx != ctx {
			t.Fatal("probe got an independent deadline")
		}
		<-probeCtx.Done()
		return false
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if err := waitHealthy(context.Background(), func(context.Context) bool { return true }); err != nil {
		t.Fatal(err)
	}
}

func TestSupervisorSocketResidueAlsoBlocksLifecycle(t *testing.T) {
	c, _ := controlFixture(t)
	os.Remove(filepath.Join(c.RunDir, "process.json"))
	p := filepath.Join(c.RunDir, "supervisor.sock")
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: p, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if c.CheckStopped(context.Background()) == nil {
		t.Fatal("unidentified supervisor accepted")
	}
	if c.Control(context.Background(), "teardown") == nil {
		t.Fatal("live supervisor ignored")
	}
}

func TestOnlySupervisedProcessRecordsAreReadable(t *testing.T) {
	for _, mode := range []string{"", "supervised", "unknown"} {
		c, f := controlFixture(t)
		p := f.current
		p.Mode = mode
		if err := writeProcess(c.RunDir, p); err != nil {
			t.Fatal(err)
		}
		got, err := readProcess(c.RunDir, c.Owner)
		if mode != "supervised" {
			if err == nil {
				t.Fatal("unsupervised process mode accepted")
			}
		} else if err != nil || got != p {
			t.Fatalf("mode %q: %+v %v", mode, got, err)
		}
	}
}
