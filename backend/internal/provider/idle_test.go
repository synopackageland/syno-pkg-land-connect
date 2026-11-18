package provider

import (
	"context"
	"dsmapp/internal/connector"
	"testing"
	"time"
)

// Health, swap sampling and port tracking run only while the administrator
// has a connection turned on: with none, no loop and no ticker exists.
func TestHealthRunsOnlyWhileAConnectionIsEnabled(t *testing.T) {
	events := []string{}
	cf := &fixtureRuntime{id: "cloudflare", events: &events}
	ts := &fixtureRuntime{id: "tailscale", events: &events}
	m := NewMultiSelection(t.TempDir(), cf, ts)
	health := &connector.Demand{Active: m.Enabled, Loop: func(ctx context.Context) { m.WatchHealth(ctx, time.Hour) }}
	defer health.Close()
	changes := 0
	m.OnChange(func() {
		changes++
		// The hook runs after the selection lock is released.
		_ = m.Status()
		health.Update()
	})
	if e := m.Restore(context.Background()); e != nil {
		t.Fatal(e)
	}
	if m.Enabled() || health.Running() {
		t.Fatal("health runs with no connection enabled")
	}
	if _, e := m.Connect(context.Background(), connector.Request{Provider: "tailscale", ConfirmExposure: true}); e != nil {
		t.Fatal(e)
	}
	if !m.Enabled() || !health.Running() {
		t.Fatal("an enabled connection has no health loop")
	}
	if _, e := m.Connect(context.Background(), connector.Request{Provider: "cloudflare", ConfirmExposure: true}); e != nil {
		t.Fatal(e)
	}
	if _, e := m.DisconnectProvider("tailscale"); e != nil || !health.Running() {
		t.Fatal("the other connection lost its health loop", e)
	}
	if _, e := m.Disconnect(); e != nil {
		t.Fatal(e)
	}
	if m.Enabled() || health.Running() || cf.live || ts.live {
		t.Fatal("health outlived the last connection")
	}
	if changes < 5 {
		t.Fatalf("%d change notices for five changes", changes)
	}
}

// A saved enabled connection turns health on as soon as it is restored.
func TestARestoredConnectionStartsHealth(t *testing.T) {
	dir := t.TempDir()
	events := []string{}
	ts := &fixtureRuntime{id: "tailscale", events: &events}
	m := NewMultiSelection(dir, ts)
	if _, e := m.Connect(context.Background(), connector.Request{Provider: "tailscale", ConfirmExposure: true}); e != nil {
		t.Fatal(e)
	}
	if e := m.Stop(); e != nil {
		t.Fatal(e)
	}
	restored := NewMultiSelection(dir, ts)
	if restored.Enabled() {
		t.Fatal("enabled before the saved intent was read")
	}
	notified := false
	restored.OnChange(func() { notified = true })
	if e := restored.Restore(context.Background()); e != nil {
		t.Fatal(e)
	}
	if !restored.Enabled() || !notified {
		t.Fatal("a restored connection did not report itself enabled")
	}
}
