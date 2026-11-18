package provider

import (
	"context"
	"dsmapp/internal/connector"
	"errors"
	"slices"
	"testing"
)

// unsafeFixture refuses every live change, like Cloudflare's new name or a
// Tailscale port that cannot be replaced in place.
type unsafeFixture struct{ safeFixture }

func (p *unsafeFixture) PrecheckUpdate(context.Context, Settings) error {
	p.checks++
	return errApplyUnsupported
}

func unsafeSelection(t *testing.T) (*MultiSelection, *unsafeFixture) {
	t.Helper()
	p := &unsafeFixture{safeFixture{fixtureRuntime: fixtureRuntime{id: "cloudflare", events: &[]string{}}}}
	m := NewMultiSelection(t.TempDir(), p)
	if _, e := m.Connect(context.Background(), connector.Request{Provider: "cloudflare", Mode: "domain", ConfirmExposure: true}); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { m.Stop() })
	return m, p
}

func TestARejectedChangeIsTheActionsResultNotTheConnectionsStatus(t *testing.T) {
	m, p := unsafeSelection(t)
	status, e := m.Connect(context.Background(), connector.Request{Provider: "cloudflare", Mode: "domain", Subdomain: "other", ConfirmExposure: true})
	if !errors.Is(e, errApplyUnsupported) || !connector.IsPreserved(e) {
		t.Fatalf("the change was not refused as preserved: %v", e)
	}
	if !p.live || p.updates != 0 {
		t.Fatal("a refused change touched the running connection")
	}
	for _, s := range []connector.Status{status, m.children["cloudflare"].Status()} {
		if s.Apply != nil && s.Apply.Phase != "committed" {
			t.Fatalf("the refusal stayed as the connection's status: %+v", s.Apply)
		}
	}
}

func TestReplaceAppliesTheChangeThroughAReconnect(t *testing.T) {
	m, p := unsafeSelection(t)
	before := len(*p.events)
	status, e := m.Connect(context.Background(), connector.Request{Provider: "cloudflare", Mode: "domain", Subdomain: "other", ConfirmExposure: true, Replace: true})
	if e != nil || status.State != "connected" {
		t.Fatalf("replace: %+v %v", status, e)
	}
	events := (*p.events)[before:]
	if p.checks != 0 || !slices.Contains(events, "cloudflare stop") || !slices.Contains(events, "cloudflare start") {
		t.Fatalf("replace did not reconnect: prechecks %d, events %v", p.checks, events)
	}
	if got := m.children["cloudflare"].saved.Settings.Subdomain; got != "other" {
		t.Fatalf("the new settings were not saved: %q", got)
	}
}

// inPlaceFixture finds out only while applying that the change cannot keep
// its route, like a Tailscale port replaced in place, and rolls back.
type inPlaceFixture struct{ safeFixture }

func (p *inPlaceFixture) ApplyUpdate(context.Context, Settings) error {
	p.updates++
	return connector.Coded("apply_unavailable", "this port cannot be replaced without interrupting its route")
}

func TestAChangeRefusedWhileApplyingLeavesNoStatusAfterItsRollback(t *testing.T) {
	p := &inPlaceFixture{safeFixture{fixtureRuntime: fixtureRuntime{id: "cloudflare", events: &[]string{}}}}
	m := NewMultiSelection(t.TempDir(), p)
	defer m.Stop()
	if _, e := m.Connect(context.Background(), connector.Request{Provider: "cloudflare", Mode: "domain", ConfirmExposure: true}); e != nil {
		t.Fatal(e)
	}
	_, e := m.Connect(context.Background(), connector.Request{Provider: "cloudflare", Mode: "domain", Subdomain: "other", ConfirmExposure: true})
	if connector.ErrorCode(e) != "apply_unavailable" || p.rollbacks != 1 {
		t.Fatalf("got %v after %d rollbacks", e, p.rollbacks)
	}
	if s := m.children["cloudflare"].Status(); s.Apply != nil && s.Apply.Phase != "committed" {
		t.Fatalf("the refusal stayed as the connection's status: %+v", s.Apply)
	}
}
