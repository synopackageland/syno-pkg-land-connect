package provider

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// goroutinesBackTo waits until no goroutine started after base is left.
func goroutinesBackTo(t *testing.T, base int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > base {
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<16)
			t.Fatalf("%d goroutines left over, want %d\n%s", runtime.NumGoroutine(), base, buf[:runtime.Stack(buf, true)])
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A provider the administrator turned off starts no daemon when connectd
// restores, and turning it off stopped the daemon it had.
func TestADisabledProviderStartsNoDaemon(t *testing.T) {
	dir := t.TempDir()
	nbs := &nbSession{snapshot: connectedSnapshot()}
	nb := NewNetBird(filepath.Join(dir, "netbird"), &nbDriver{session: nbs})
	tss := &tsSession{snapshot: TailscaleSnapshot{BackendState: "Running", IPs: []string{"100.64.0.1"}, DNSName: "nas.example.ts.net.", Serving: true}}
	ts := NewTailscale(filepath.Join(dir, "tailscale"), &tsDriver{session: tss})
	if _, e := nb.Start(context.Background(), nbSettings()); e != nil {
		t.Fatal(e)
	}
	if _, e := ts.Start(context.Background(), tsSettings()); e != nil {
		t.Fatal(e)
	}
	if e := nb.Disable(); e != nil || !nbs.stopped {
		t.Fatal("NetBird kept its daemon after it was turned off", e)
	}
	if e := ts.Disable(); e != nil || !tss.stopped {
		t.Fatal("tailscaled kept running after it was turned off", e)
	}
	nbDriver, tsDriver := &nbDriver{session: &nbSession{}}, &tsDriver{session: &tsSession{}}
	base := runtime.NumGoroutine()
	if e := NewNetBird(filepath.Join(dir, "netbird"), nbDriver).Restore(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e := NewTailscale(filepath.Join(dir, "tailscale"), tsDriver).Restore(context.Background()); e != nil {
		t.Fatal(e)
	}
	if nbDriver.opens != 0 || tsDriver.starts != 0 {
		t.Fatal("a disabled provider started its daemon at restore")
	}
	goroutinesBackTo(t, base)
}

// Turning NetBird off while its saved identity still starts ends the
// background restore: no retry loop or timer is left behind.
func TestTurningOffAStartingNetBirdLeavesNoRestoreLoop(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "netbird")
	first := NewNetBird(dir, &nbDriver{session: &nbSession{snapshot: connectedSnapshot()}})
	if _, e := first.Start(context.Background(), nbSettings()); e != nil {
		t.Fatal(e)
	}
	if e := first.Stop(); e != nil {
		t.Fatal(e)
	}
	session := &slowNetBirdSession{snapshot: connectedSnapshot(), readyErrors: 1 << 30}
	driver := &slowNetBirdDriver{session: readySession{session}}
	p := NewNetBird(dir, driver)
	p.RetryDelay = time.Millisecond
	base := runtime.NumGoroutine()
	if e := p.Restore(context.Background()); e != nil {
		t.Fatal(e)
	}
	if p.Status().State != "connecting" {
		t.Fatal("the saved identity is not starting")
	}
	if e := p.Disable(); e != nil {
		t.Fatal(e)
	}
	opens := driver.count()
	goroutinesBackTo(t, base)
	time.Sleep(20 * time.Millisecond)
	if driver.count() != opens {
		t.Fatal("a turned-off NetBird is still being started")
	}
}
