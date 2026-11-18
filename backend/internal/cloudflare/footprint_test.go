package cloudflare

import (
	"context"
	"runtime"
	"testing"
	"time"
)

// Turning the tunnel off ends cloudflared and every goroutine and connection
// that watched it; a restore of a turned-off tunnel starts nothing.
func TestATurnedOffTunnelLeavesNothingRunning(t *testing.T) {
	f := newFixture(t)
	f.metrics.hostname = "rest-texts.trycloudflare.com"
	base := runtime.NumGoroutine()
	if e := f.tunnel.Quick(context.Background()); e != nil {
		t.Fatal(e)
	}
	f.waitState("connected")
	// Let the watcher poll cloudflared's metrics a few times.
	time.Sleep(50 * time.Millisecond)
	if e := f.tunnel.Disable(); e != nil {
		t.Fatal(e)
	}
	for _, p := range f.launcher.procs {
		if p.Alive() {
			t.Fatal("cloudflared still runs after the tunnel was turned off")
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > base {
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<16)
			t.Fatalf("%d goroutines left over, want %d\n%s", runtime.NumGoroutine(), base, buf[:runtime.Stack(buf, true)])
		}
		time.Sleep(5 * time.Millisecond)
	}
	starts := len(f.launcher.starts())
	if e := f.tunnel.Restore(context.Background()); e != nil {
		t.Fatal(e)
	}
	if len(f.launcher.starts()) != starts {
		t.Fatal("a turned-off tunnel started cloudflared at restore")
	}
}
