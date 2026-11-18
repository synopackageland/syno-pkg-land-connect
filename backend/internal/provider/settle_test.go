package provider

import (
	"context"
	"dsmapp/internal/connector"
	"testing"
	"time"
)

// Most health tests count failures from the first tick; the settling tests
// below set the window themselves.
func init() { healthSettle = func(string) time.Duration { return 0 } }

func settling(t *testing.T) {
	t.Helper()
	healthSettle = connector.SettleWindow
	t.Cleanup(func() { healthSettle = func(string) time.Duration { return 0 } })
}

func failTicks(m *Selection, n int) {
	for i := 0; i < n; i++ {
		m.healthTick(context.Background())
	}
}

func TestHealthFailuresWhileSettlingAreNotCounted(t *testing.T) {
	settling(t)
	m, p := recoverySelection(t)
	p.healthError = connector.Coded("local_check_failed", "synthetic")
	failTicks(m, 5)
	if m.healthFailures != 0 || m.applyStatus != nil && m.applyStatus.Phase != "committed" || m.pending != nil {
		t.Fatalf("a settling connection was flagged or repaired: %d failures, %+v", m.healthFailures, m.applyStatus)
	}
}

func TestHealthFailuresAfterTheWindowCountAgain(t *testing.T) {
	settling(t)
	m, p := recoverySelection(t)
	m.settleUntil = time.Now().Add(-time.Second)
	p.healthError = connector.Coded("local_check_failed", "synthetic")
	failTicks(m, 2)
	if m.healthFailures != 2 {
		t.Fatalf("failures after the window: %d, want 2", m.healthFailures)
	}
}

func TestReconnectRestartsTheHealthWindow(t *testing.T) {
	settling(t)
	m, p := recoverySelection(t)
	m.settleUntil = time.Now().Add(-time.Second)
	if _, e := m.Connect(context.Background(), connector.Request{Provider: "netbird"}); e != nil {
		t.Fatal(e)
	}
	p.healthError = connector.Coded("local_check_failed", "synthetic")
	failTicks(m, 3)
	if m.healthFailures != 0 {
		t.Fatalf("a reconnect did not restart the window: %d failures", m.healthFailures)
	}
}

func TestARestoredConnectionSettlesAtBoot(t *testing.T) {
	settling(t)
	m, p := recoverySelection(t)
	fresh := NewSelection(m.dir, p)
	if e := fresh.Restore(context.Background()); e != nil {
		t.Fatal(e)
	}
	p.healthError = connector.Coded("local_check_failed", "synthetic")
	failTicks(fresh, 3)
	if fresh.healthFailures != 0 {
		t.Fatalf("a restored connection failed while settling: %d failures", fresh.healthFailures)
	}
	if until := time.Until(fresh.settleUntil); until <= 0 || until > time.Minute {
		t.Fatalf("netbird settles for %v, want up to a minute", until)
	}
}
