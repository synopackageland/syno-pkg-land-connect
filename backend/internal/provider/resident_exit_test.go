package provider

import (
	"context"
	"dsmapp/internal/supervise"
	"github.com/synopackageland/syno-pkg-land-sdk/supervision"
	"sync"
	"testing"
	"time"
)

type cleanNetBirdSession struct{ readySession }

func (cleanNetBirdSession) LastExit() supervision.Exit {
	return supervision.Exit{Code: 0, Reason: "child_exit"}
}

func TestEnabledNetBirdCleanExitBacksOffAndRestarts(t *testing.T) {
	session := &slowNetBirdSession{snapshot: connectedSnapshot()}
	driver := &slowNetBirdDriver{session: cleanNetBirdSession{readySession{session}}}
	p := NewNetBird(t.TempDir(), driver)
	p.RetryDelay = 30 * time.Millisecond
	t.Cleanup(func() { p.Stop() })
	if _, err := p.Start(context.Background(), nbSettings()); err != nil {
		t.Fatal(err)
	}
	session.set(func(s *slowNetBirdSession) { s.exits = true })
	awaitResidentBackoff(t, &p.mu, &p.restoring)
	deadline := time.Now().Add(3 * time.Second)
	for driver.count() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if driver.count() != 2 {
		t.Fatal("enabled NetBird did not restart")
	}
	if err := p.Stop(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if driver.count() != 2 {
		t.Fatal("stopped NetBird restarted")
	}
}

func TestEnabledTailscaleCleanExitBacksOffAndRestarts(t *testing.T) {
	session := &slowTailscaleSession{snapshot: runningTailscale()}
	p, driver := restoredTailscaleAfter(t, session, 30*time.Millisecond)
	waitTailscale(t, p, "connected")
	session.set(func(s *slowTailscaleSession) { s.exited = true })
	awaitResidentBackoff(t, &p.mu, &p.restoring)
	deadline := time.Now().Add(3 * time.Second)
	for driver.count() < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if driver.count() != 2 {
		t.Fatal("enabled Tailscale did not restart")
	}
	if err := p.Stop(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	if driver.count() != 2 {
		t.Fatal("stopped Tailscale restarted")
	}
}

func awaitResidentBackoff(t *testing.T, mu *sync.Mutex, r *supervise.Restorer) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		s := r.Snapshot()
		mu.Unlock()
		if s.State == supervision.Backoff {
			if s.LastExit == nil || s.LastExit.Code != 0 || s.Attempts != 1 || s.NextRetry.IsZero() {
				t.Fatal(s)
			}
			return
		}
		if s.State == supervision.Stopped || s.State == supervision.Failed {
			t.Fatal(s)
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("no bounded backoff after clean exit")
}
