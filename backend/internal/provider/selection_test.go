package provider

import (
	"context"
	"dsmapp/internal/connector"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type fixtureRuntime struct {
	id        string
	events    *[]string
	live      bool
	failStop  bool
	failStart bool
}

func (p *fixtureRuntime) ID() string { return p.id }
func (p *fixtureRuntime) Start(context.Context, Settings) (connector.Status, error) {
	*p.events = append(*p.events, p.id+" start")
	p.live = true
	if p.failStart {
		return p.Status(), errors.New("fixture start")
	}
	return p.Status(), nil
}
func (p *fixtureRuntime) Restore(context.Context) error {
	*p.events = append(*p.events, p.id+" restore")
	p.live = true
	return nil
}
func (p *fixtureRuntime) Stop() error {
	*p.events = append(*p.events, p.id+" stop")
	if p.failStop {
		return errors.New("fixture stop")
	}
	p.live = false
	return nil
}
func (p *fixtureRuntime) Disable() error { return p.Stop() }
func (p *fixtureRuntime) Status() connector.Status {
	s := connector.Status{Provider: p.id, State: "disconnected"}
	if p.live {
		s.State = "connected"
	}
	return s
}
func (*fixtureRuntime) Schema() []Field                    { return nil }
func (*fixtureRuntime) Endpoints() []Endpoint              { return nil }
func (*fixtureRuntime) Binaries() []BinaryRequirement      { return nil }
func (*fixtureRuntime) AcceptMappings([]PortMapping) error { return nil }
func TestSelectionStopsOldBeforeStartingNewAndRestoresOnlySelected(t *testing.T) {
	dir := t.TempDir()
	events := []string{}
	cf := &fixtureRuntime{id: "cloudflare", events: &events}
	ts := &fixtureRuntime{id: "tailscale", events: &events}
	m := NewSelection(dir, cf, ts)
	if _, e := m.Connect(context.Background(), connector.Request{ConfirmExposure: true}); e != nil {
		t.Fatal(e)
	}
	if _, e := m.Connect(context.Background(), connector.Request{Provider: "tailscale", ConfirmExposure: true}); e != nil {
		t.Fatal(e)
	}
	if cf.live || !ts.live {
		t.Fatal(events)
	}
	if events[len(events)-2] != "cloudflare stop" || events[len(events)-1] != "tailscale start" {
		t.Fatal(events)
	}
	m.Stop()
	events = nil
	next := NewSelection(dir, cf, ts)
	if e := next.Restore(context.Background()); e != nil {
		t.Fatal(e)
	}
	if len(events) != 1 || events[0] != "tailscale restore" {
		t.Fatal(events)
	}
	if _, e := next.Disconnect(); e != nil {
		t.Fatal(e)
	}
	events = nil
	if e := NewSelection(dir, cf, ts).Restore(context.Background()); e != nil || len(events) != 0 {
		t.Fatal(e, events)
	}
}
func TestSelectionFailureNeverRevivesOldProvider(t *testing.T) {
	for _, failStop := range []bool{false, true} {
		t.Run(map[bool]string{true: "stop", false: "start"}[failStop], func(t *testing.T) {
			dir := t.TempDir()
			events := []string{}
			cf := &fixtureRuntime{id: "cloudflare", events: &events}
			ts := &fixtureRuntime{id: "tailscale", events: &events, failStart: !failStop}
			m := NewSelection(dir, cf, ts)
			m.Connect(context.Background(), connector.Request{ConfirmExposure: true})
			cf.failStop = failStop
			if _, e := m.Connect(context.Background(), connector.Request{Provider: "tailscale", ConfirmExposure: true}); e == nil {
				t.Fatal("failure swallowed")
			}
			if failStop && ts.live {
				t.Fatal("overlapping providers")
			}
			events = nil
			if e := NewSelection(dir, cf, ts).Restore(context.Background()); e != nil || len(events) != 0 {
				t.Fatal("revived", e, events)
			}
		})
	}
}
func TestSelectionUnavailableAndUnconfirmedRefuseBeforeStop(t *testing.T) {
	events := []string{}
	cf := &fixtureRuntime{id: "cloudflare", events: &events}
	ts := &fixtureRuntime{id: "tailscale", events: &events}
	m := NewSelection(t.TempDir(), cf, ts)
	m.Connect(context.Background(), connector.Request{ConfirmExposure: true})
	for _, r := range []connector.Request{{Provider: "ipv6"}, {Provider: "bogus"}, {Provider: "cloudflare", Mode: "quick"}} {
		if _, e := m.Connect(context.Background(), r); e == nil || !cf.live {
			t.Fatal("unsafe switch")
		}
	}
}
func TestSelectionMalformedSavedStateFailsClosed(t *testing.T) {
	for _, raw := range []string{`{}`, `{"provider":"bogus","enabled":true}`, `{"provider":"cloudflare","enabled":true} {}`, `{"provider":"cloudflare","enabled":true,"extra":1}`} {
		dir := t.TempDir()
		connector.WritePrivate(filepath.Join(dir, "provider.json"), []byte(raw))
		events := []string{}
		cf := &fixtureRuntime{id: "cloudflare", events: &events}
		m := NewSelection(dir, cf)
		if e := m.Restore(context.Background()); e == nil || len(events) != 0 || m.Status().State != "failed" {
			t.Fatal(raw, e, events, m.Status())
		}
	}
}

// reportingRuntime fails its restore with a failed status of its own, then
// recovers without the selection, as Cloudflare does after the DSM review.
type reportingRuntime struct {
	fixtureRuntime
	state string
}

func (p *reportingRuntime) Restore(context.Context) error {
	p.state = "failed"
	return errors.New("fixture needs review")
}
func (p *reportingRuntime) Status() connector.Status {
	return connector.Status{Provider: p.id, State: p.state, Code: "pin_proxy_required"}
}
func TestSelectionRestoreShowsProviderRecovery(t *testing.T) {
	dir := t.TempDir()
	connector.WritePrivate(filepath.Join(dir, "provider.json"), []byte(`{"provider":"cloudflare","enabled":true}`))
	events := []string{}
	cf := &reportingRuntime{fixtureRuntime: fixtureRuntime{id: "cloudflare", events: &events}}
	m := NewSelection(dir, cf)
	if e := m.Restore(context.Background()); e == nil || m.Status().State != "failed" || m.Status().Code != "pin_proxy_required" {
		t.Fatal(e, m.Status())
	}
	cf.state = "connected"
	if s := m.Status(); s.State != "connected" {
		t.Fatal(s)
	}
}
func TestSelectionPersistenceFailureDoesNotStart(t *testing.T) {
	dir := t.TempDir()
	os.Mkdir(filepath.Join(dir, "provider.json"), 0700)
	events := []string{}
	cf := &fixtureRuntime{id: "cloudflare", events: &events}
	m := NewSelection(dir, cf)
	if _, e := m.Connect(context.Background(), connector.Request{ConfirmExposure: true}); e == nil || cf.live {
		t.Fatal("started without durable intent")
	}
}

func TestDisabledSelectionRetainsPublicSettingsAcrossRestartWithoutCredentials(t *testing.T) {
	dir := t.TempDir()
	events := []string{}
	cf := &fixtureRuntime{id: "cloudflare", events: &events}
	ts := &fixtureRuntime{id: "tailscale", events: &events}
	m := NewSelection(dir, cf, ts)
	req := connector.Request{Provider: "tailscale", Token: "SYNTHETIC-SECRET", LoginServer: "https://headscale.example.com"}
	if _, e := m.Connect(context.Background(), req); e != nil {
		t.Fatal(e)
	}
	if _, e := m.Disconnect(); e != nil {
		t.Fatal(e)
	}
	raw, e := connector.ReadPrivate(filepath.Join(dir, "provider.json"))
	if e != nil || containsSecret(raw, req.Token) {
		t.Fatal("secret in selection")
	}
	fresh := NewSelection(dir, cf, ts)
	events = nil
	if e = fresh.Restore(context.Background()); e != nil || len(events) != 0 {
		t.Fatal("disabled settings started a provider", e)
	}
	s := fresh.Status()
	if s.Provider != "tailscale" || s.State != "disconnected" {
		t.Fatal("lost saved selection", s)
	}
}

func TestSelectionRegistersIPv6RuntimeWithoutStartingIt(t *testing.T) {
	dir := t.TempDir()
	events := []string{}
	m := NewSelection(dir, &fixtureRuntime{id: "cloudflare", events: &events}, &fixtureRuntime{id: "tailscale", events: &events}, NewIPv6Runtime(dir, nil))
	v6, ok := m.registry["ipv6"].(*IPv6Runtime)
	if !ok || v6.Status().State != "disconnected" || len(v6.Endpoints()) != 0 || len(events) != 0 {
		t.Fatal("IPv6 slot missing or started")
	}
	if err := v6.AcceptMappings([]PortMapping{{LocalPort: 7443, PublicPort: 443, Protocol: "https"}}); err == nil {
		t.Fatal("IPv6 translation accepted")
	}
}

// Revoke steps in separate files overlap their fsync latency instead of adding it.
func TestConcurrentlyRunsEveryStepAtOnceAndJoinsErrors(t *testing.T) {
	const steps = 3
	var started sync.WaitGroup
	started.Add(steps)
	all := make(chan struct{})
	go func() { started.Wait(); close(all) }()
	failure := errors.New("step failed")
	step := func(err error) func() error {
		return func() error {
			started.Done()
			select {
			case <-all:
				return err
			case <-time.After(5 * time.Second):
				return errors.New("steps ran one at a time")
			}
		}
	}
	err := concurrently(step(nil), step(failure), step(nil))
	if !errors.Is(err, failure) || strings.Contains(fmt.Sprint(err), "one at a time") {
		t.Fatalf("got %v", err)
	}
}

type dependencyRuntime struct {
	*fixtureRuntime
	refused bool
}

func (p *dependencyRuntime) BeforeDisable() error {
	if p.refused {
		return connector.Coded("netbird_cloudflare_in_use", "turn off the server first")
	}
	return nil
}
func TestDependencyRefusalPrecedesDurableDisconnect(t *testing.T) {
	events := []string{}
	p := &dependencyRuntime{fixtureRuntime: &fixtureRuntime{id: "cloudflare", events: &events}}
	m := NewSelection(t.TempDir(), p)
	if _, e := m.Connect(context.Background(), connector.Request{Provider: "cloudflare", ConfirmExposure: true}); e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(filepath.Join(m.dir, "provider.json"))
	if e != nil {
		t.Fatal(e)
	}
	p.refused = true
	if _, e := m.Disconnect(); connector.ErrorCode(e) != "netbird_cloudflare_in_use" {
		t.Fatal("disconnect was allowed", e)
	}
	after, _ := os.ReadFile(filepath.Join(m.dir, "provider.json"))
	if string(before) != string(after) || !p.live {
		t.Fatal("refusal changed the gate or process", events)
	}
}

type identityRuntime struct {
	*fixtureRuntime
	revoked int
}

func (p *identityRuntime) Disable() error                      { p.revoked++; return p.fixtureRuntime.Disable() }
func (p *identityRuntime) PreserveOnReconnect(s Settings) bool { return s.Mode == "domain" }
func TestReconnectSavedDomainDoesNotRevokeItsIdentity(t *testing.T) {
	events := []string{}
	p := &identityRuntime{fixtureRuntime: &fixtureRuntime{id: "cloudflare", events: &events}}
	m := NewSelection(t.TempDir(), p)
	request := connector.Request{Provider: "cloudflare", Mode: "domain", ConfirmExposure: true}
	if _, e := m.Connect(context.Background(), request); e != nil {
		t.Fatal(e)
	}
	p.live = false // the runtime lost its connection, so safe-update is not available
	if _, e := m.Connect(context.Background(), request); e != nil {
		t.Fatal(e)
	}
	if p.revoked != 0 || !p.live {
		t.Fatal("reconnect revoked the retained certificate", p.revoked, events)
	}
	if _, e := m.Disconnect(); e != nil || p.revoked != 1 {
		t.Fatal("explicit shutdown did not revoke", p.revoked, e)
	}
}
