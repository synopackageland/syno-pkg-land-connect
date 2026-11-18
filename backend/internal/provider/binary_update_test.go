package provider

import (
	"context"
	"dsmapp/internal/connector"
	"errors"
	"reflect"
	"testing"
	"time"
)

type updateRuntime struct {
	fixtureRuntime
	version *string
	failNew bool
}

func (p *updateRuntime) Restore(ctx context.Context) error {
	if p.failNew && *p.version == "new" {
		*p.events = append(*p.events, "new failed")
		return errors.New("fixture restart")
	}
	return p.fixtureRuntime.Restore(ctx)
}
func updateFixture(t *testing.T) (*Selection, *updateRuntime, *[]string, *string) {
	t.Helper()
	events := []string{}
	version := "old"
	p := &updateRuntime{fixtureRuntime: fixtureRuntime{id: "tailscale", events: &events}, version: &version}
	m := NewSelection(t.TempDir(), p)
	if _, e := m.Connect(context.Background(), connector.Request{Provider: "tailscale", ConfirmExposure: true}); e != nil {
		t.Fatal(e)
	}
	events = nil
	return m, p, &events, &version
}
func changeVersion(events *[]string, version *string) func() (func() error, func() error, error) {
	return func() (func() error, func() error, error) {
		return func() error { *events = append(*events, "apply"); *version = "new"; return nil }, func() error { *events = append(*events, "rollback"); *version = "old"; return nil }, nil
	}
}
func TestBinaryUpdateRestartsSelectedRuntimeAndPreservesIntent(t *testing.T) {
	m, p, events, v := updateFixture(t)
	if e := m.UpdateBinaries(context.Background(), "tailscale", changeVersion(events, v)); e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(*events, []string{"tailscale stop", "apply", "tailscale restore"}) || *v != "new" || !p.live || !m.saved.Enabled {
		t.Fatal(*events, *v, m.saved)
	}
}
func TestBinaryUpdateFailureRestoresOldVersion(t *testing.T) {
	m, p, events, v := updateFixture(t)
	p.failNew = true
	e := m.UpdateBinaries(context.Background(), "tailscale", changeVersion(events, v))
	if e == nil || *v != "old" || !p.live || !m.saved.Enabled || m.failed {
		t.Fatal(e, *events, *v, m.saved)
	}
	if !reflect.DeepEqual(*events, []string{"tailscale stop", "apply", "new failed", "tailscale stop", "rollback", "tailscale restore"}) {
		t.Fatal(*events)
	}
}
func TestBinaryUpdateStopFailureDoesNotSwitch(t *testing.T) {
	m, p, events, v := updateFixture(t)
	p.failStop = true
	e := m.UpdateBinaries(context.Background(), "tailscale", changeVersion(events, v))
	if e == nil || *v != "old" || m.saved.Enabled || len(*events) != 1 {
		t.Fatal(e, *events, *v, m.saved)
	}
}
func TestBinaryUpdateInactiveProviderDoesNotStart(t *testing.T) {
	m, p, events, v := updateFixture(t)
	if _, e := m.Disconnect(); e != nil {
		t.Fatal(e)
	}
	*events = nil
	if e := m.UpdateBinaries(context.Background(), "tailscale", changeVersion(events, v)); e != nil || p.live || m.saved.Enabled || !reflect.DeepEqual(*events, []string{"apply"}) {
		t.Fatal(e, *events)
	}
}
func TestBinaryUpdateCancellationRestoresSelectionWithoutRestart(t *testing.T) {
	m, p, events, v := updateFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	prepare := func() (func() error, func() error, error) {
		apply, rollback, e := changeVersion(events, v)()
		return func() error {
			if e := apply(); e != nil {
				return e
			}
			cancel()
			return ctx.Err()
		}, rollback, e
	}
	if e := m.UpdateBinaries(ctx, "tailscale", prepare); e == nil || *v != "old" || p.live || !m.saved.Enabled {
		t.Fatal(e, *events, *v, m.saved)
	}
	for _, event := range *events {
		if event == "tailscale restore" {
			t.Fatal("cancelled shutdown restarted runtime")
		}
	}
}
func TestBinaryUpdateSerializesConcurrentDisconnect(t *testing.T) {
	m, p, events, v := updateFixture(t)
	entered := make(chan struct{})
	release := make(chan struct{})
	updated := make(chan error, 1)
	disconnected := make(chan error, 1)
	prepare := func() (func() error, func() error, error) {
		apply, revert, e := changeVersion(events, v)()
		return func() error { close(entered); <-release; return apply() }, revert, e
	}
	go func() { updated <- m.UpdateBinaries(context.Background(), "tailscale", prepare) }()
	<-entered
	go func() { _, e := m.Disconnect(); disconnected <- e }()
	select {
	case e := <-disconnected:
		t.Fatal("disconnect bypassed update lock", e)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if e := <-updated; e != nil {
		t.Fatal(e)
	}
	if e := <-disconnected; e != nil {
		t.Fatal(e)
	}
	if p.live || m.saved.Enabled {
		t.Fatal("update revived a disconnected provider")
	}
}

type statesRuntime struct {
	fixtureRuntime
	states []string
}

func (p *statesRuntime) Status() connector.Status {
	s := p.states[0]
	if len(p.states) > 1 {
		p.states = p.states[1:]
	}
	return connector.Status{Provider: p.id, State: s}
}
func TestBinaryReadinessWaitsThroughUnknownReads(t *testing.T) {
	p := &statesRuntime{fixtureRuntime: fixtureRuntime{id: "tailscale"}, states: []string{"connecting", "unknown", "connected"}}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if e := waitBinaryReady(ctx, p, false); e != nil {
		t.Fatal("an unconfirmed read is not a failed candidate", e)
	}
}
