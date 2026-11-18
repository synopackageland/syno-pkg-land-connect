package main

import (
	"dsmapp/internal/daemon"
	"encoding/json"
	"github.com/synopackageland/syno-pkg-land-sdk/supervision"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var quick = supervision.Config{Initial: time.Millisecond, Min: time.Millisecond, Max: 4 * time.Millisecond, Stable: time.Minute, Burst: 3}

// counted runs a shell worker that records each start in dir/runs.
func counted(dir, script string) func() *exec.Cmd {
	return func() *exec.Cmd {
		cmd := exec.Command("sh", "-c", "echo x >> runs; n=$(wc -l < runs); "+script)
		cmd.Dir = dir
		return cmd
	}
}
func runs(t *testing.T, dir string) int {
	raw, e := os.ReadFile(filepath.Join(dir, "runs"))
	if e != nil {
		t.Fatal(e)
	}
	return strings.Count(string(raw), "x")
}

func TestCleanWorkerExitRestartsWithBackoffUntilTheLimit(t *testing.T) {
	dir := t.TempDir()
	cfg := quick
	cfg.Initial, cfg.Min, cfg.Max = 20*time.Millisecond, 20*time.Millisecond, 20*time.Millisecond
	started := time.Now()
	if e := superviseWorker(cfg, dir, counted(dir, "exit 0")); e == nil {
		t.Fatal("resident clean exits must exhaust the budget")
	}
	if runs(t, dir) != cfg.Burst || time.Since(started) < time.Duration(cfg.Burst-1)*cfg.Min {
		t.Fatal("clean exit bypassed restart backoff")
	}
	raw, err := os.ReadFile(filepath.Join(dir, stoppedMarker))
	if err != nil {
		t.Fatal(err)
	}
	var snapshot supervision.Snapshot
	if err := json.Unmarshal(raw, &snapshot); err != nil || snapshot.State != supervision.Failed || snapshot.LastExit.Code != 0 || !snapshot.NextRetry.IsZero() {
		t.Fatalf("%s %v", raw, err)
	}
}

func TestRepeatedFastCrashesStopWithAMarker(t *testing.T) {
	dir := t.TempDir()
	worker := counted(dir, "exit 3")
	if e := superviseWorker(quick, dir, worker); e == nil {
		t.Fatal("repeated crashes must stop serving")
	}
	if runs(t, dir) != quick.Burst {
		t.Fatal("expected exactly the crash limit", runs(t, dir))
	}
	if _, e := os.Stat(filepath.Join(dir, stoppedMarker)); e != nil {
		t.Fatal("the page needs the stopped marker", e)
	}
}

func TestDuplicateServeIsRefusedByTheLock(t *testing.T) {
	dir := t.TempDir()
	first, e := daemon.Lock(dir)
	if e != nil {
		t.Fatal(e)
	}
	defer first.Close()
	if _, e := daemon.Lock(dir); e == nil {
		t.Fatal("a second serve must not take the daemon lock")
	}
	if e := daemon.HeldLock(first, dir); e != nil {
		t.Fatal("the worker must accept the inherited lock", e)
	}
}
