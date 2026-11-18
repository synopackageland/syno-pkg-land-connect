package provider

import (
	"context"
	"dsmapp/internal/connector"
	"dsmapp/internal/ipv6"
	"encoding/json"
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type ownerSource struct {
	ports []ResolvedPort
	err   error
}

func (s *ownerSource) ResolvePorts(context.Context) ([]ResolvedPort, error) {
	return append([]ResolvedPort(nil), s.ports...), s.err
}
func ownerPort(n int) []ResolvedPort {
	return []ResolvedPort{{ID: "dsm-http", Mapping: PortMapping{LocalPort: n, PublicPort: n, Protocol: "http"}}}
}

type ownerAddresses struct{}

func (ownerAddresses) Addresses(context.Context) ([]ipv6.Address, error) {
	return []ipv6.Address{{IP: netip.MustParseAddr("2606:4700:1234::1"), Interface: "eth0", PrefixBits: 64}}, nil
}
func TestPortOwnerIPv6UsesTrustedSourceAndRejectsBrowserPorts(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	v6 := NewIPv6Runtime(dir, ownerAddresses{})
	source := &ownerSource{ports: ownerPort(5000)}
	m := NewSelection(dir, v6)
	m.SetPortSource(source)
	if _, e := m.Connect(ctx, connector.Request{Provider: "ipv6", ConfirmExposure: true, Mappings: []PortMapping{{LocalPort: 7443, PublicPort: 7443, Protocol: "https"}}}); e == nil {
		t.Fatal("browser spoof accepted")
	}
	if _, e := m.Connect(ctx, connector.Request{Provider: "ipv6", ConfirmExposure: true}); e != nil {
		t.Fatal(e)
	}
	source.ports = ownerPort(6000)
	if e := m.ReplacePorts(ctx, source.ports); e != nil || v6.Endpoints()[0].LocalPort != 6000 {
		t.Fatal(e, v6.Endpoints())
	}
	if e := m.WithdrawPorts(ctx); e != nil || len(v6.Endpoints()) != 0 {
		t.Fatal(e, v6.Endpoints())
	}
}
func TestPortOwnerCloudflareAndDisconnectedNeverStartOrStop(t *testing.T) {
	events := []string{}
	cf := &fixtureRuntime{id: "cloudflare", events: &events}
	ts := &fixtureRuntime{id: "tailscale", events: &events}
	m := NewSelection(t.TempDir(), cf, ts)
	source := &ownerSource{ports: ownerPort(5000)}
	m.SetPortSource(source)
	m.Connect(context.Background(), connector.Request{ConfirmExposure: true})
	events = nil
	if e := m.ReplacePorts(context.Background(), source.ports); !errors.Is(e, ErrPortsInactive) {
		t.Fatal(e)
	}
	if e := m.WithdrawPorts(context.Background()); e != nil || len(events) != 0 || !cf.live {
		t.Fatal(e, events)
	}
}

func TestIPv6SelectionWithoutTrustedSourceCannotActivate(t *testing.T) {
	runtime := NewIPv6Runtime(t.TempDir(), ownerAddresses{})
	manager := NewSelection(t.TempDir(), runtime)
	if _, e := manager.Connect(context.Background(), connector.Request{Provider: "ipv6", ConfirmExposure: true, Mappings: []PortMapping{{LocalPort: 7443, PublicPort: 7443, Protocol: "https"}}}); e == nil || len(runtime.Endpoints()) != 0 {
		t.Fatal("unwired manager accepted browser ports", e)
	}
}
func TestIPv6PortRecoveryReactivatesOnlyCurrentCandidatesAfterFailedWrite(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := &ownerSource{ports: ownerPort(5000)}
	runtime := NewIPv6Runtime(dir, ownerAddresses{})
	manager := NewSelection(dir, runtime)
	manager.SetPortSource(source)
	if _, e := manager.Connect(ctx, connector.Request{Provider: "ipv6", ConfirmExposure: true}); e != nil {
		t.Fatal(e)
	}
	// A runtime-store failure stops candidates. It may recover only by rewriting
	// trusted current ports, never by restoring the old numeric value.
	path := filepath.Join(dir, "ipv6.json")
	raw, e := os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	os.Remove(path)
	os.Mkdir(path, 0700)
	source.ports = ownerPort(6000)
	if e = manager.ReplacePorts(ctx, source.ports); e == nil || len(runtime.Endpoints()) != 0 {
		t.Fatal("write failure", e)
	}
	os.Remove(path)
	os.WriteFile(path, raw, 0600)
	if e = manager.ReplacePorts(ctx, source.ports); e != nil || len(runtime.Endpoints()) != 1 || runtime.Endpoints()[0].LocalPort != 6000 {
		t.Fatal("failed to resume trusted candidates", e, runtime.Endpoints())
	}
}

func TestTailscaleOwnsNoPortsAndWithdrawalAfterStopStartsNothing(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := &ownerSource{ports: ownerPort(5000)}
	session := &tsSession{snapshot: TailscaleSnapshot{BackendState: "Running", IPs: []string{"100.64.0.1"}, DNSName: "nas.fixture.ts.net", Serving: true}}
	driver := &tsDriver{session: session}
	ts := NewTailscale(dir+"/tailscale", driver)
	manager := NewSelection(dir, ts)
	manager.SetPortSource(source)
	if _, e := manager.Connect(ctx, connector.Request{Provider: "tailscale"}); e != nil {
		t.Fatal(e)
	}
	// Tailscale owns no ports: the tracker never reconfigures or restarts it.
	if e := manager.ReplacePorts(ctx, source.ports); !errors.Is(e, ErrPortsInactive) || driver.starts != 1 || len(session.served) != 0 {
		t.Fatal("port tracking reached Tailscale", e, driver.starts, session.served)
	}
	if e := manager.Stop(); e != nil {
		t.Fatal(e)
	}
	before := driver.starts
	if e := manager.WithdrawPorts(ctx); e != nil || driver.starts != before {
		t.Fatal("withdrawal reopened the stopped daemon", e, driver.starts)
	}
}

// knownSource also lists every service that still exists, enabled or not.
type knownSource struct {
	ownerSource
	known []ResolvedPort
}

func (s *knownSource) KnownPorts(context.Context) ([]ResolvedPort, error) {
	return append([]ResolvedPort(nil), s.known...), s.err
}

// cancelingDriver passes Selection's preflight, then fails the runtime's own
// check, as a Stop that cancels a pending start does.
type cancelingDriver struct {
	tsDriver
	checks int
}

func (d *cancelingDriver) CheckBinaries(context.Context) error {
	if d.checks++; d.checks > 1 {
		return context.Canceled
	}
	return nil
}
func savedTailscaleMappings(t *testing.T, dir string) []PortMapping {
	raw, e := os.ReadFile(filepath.Join(dir, "tailscale", "settings.json"))
	if e != nil {
		t.Fatal(e)
	}
	var saved tailscaleSaved
	if e = json.Unmarshal(raw, &saved); e != nil {
		t.Fatal(e)
	}
	return saved.Settings.Mappings
}
func savedIPv6Mappings(t *testing.T, dir string) []PortMapping {
	raw, e := os.ReadFile(filepath.Join(dir, "ipv6.json"))
	if e != nil {
		t.Fatal(e)
	}
	var saved ipv6Config
	if e = json.Unmarshal(raw, &saved); e != nil {
		t.Fatal(e)
	}
	return saved.Mappings
}
func TestIPv6StartThenStopKeepsServicesAfterRestart(t *testing.T) {
	http := ResolvedPort{ID: "dsm-http", Mapping: PortMapping{LocalPort: 5000, PublicPort: 5000, Protocol: "http"}}
	https := ResolvedPort{ID: "dsm-https", Mapping: PortMapping{LocalPort: 5001, PublicPort: 5001, Protocol: "https"}}
	want := []PortMapping{http.Mapping, https.Mapping}
	process := func(dir string) *MultiSelection {
		source := &knownSource{ownerSource: ownerSource{ports: []ResolvedPort{http, https}}, known: []ResolvedPort{http, https}}
		m := NewMultiSelection(dir, NewIPv6Runtime(dir, ownerAddresses{}))
		m.SetPortSource(source)
		if e := m.Restore(context.Background()); e != nil {
			t.Fatal(e)
		}
		return m
	}
	request := connector.Request{Provider: "ipv6", ConfirmExposure: true, ServiceIDs: []string{"dsm-http", "dsm-https"}}
	dir := t.TempDir()
	first := process(dir)
	if _, e := first.Connect(context.Background(), request); e != nil {
		t.Fatal(e)
	}
	first.DisconnectProvider("ipv6")
	m := process(dir)
	if _, e := m.Connect(context.Background(), request); e != nil {
		t.Fatal(e)
	}
	// The tracker withdraws every route when a start changes the port owner
	// and re-applies them a poll later; Stop can land in between.
	if e := m.ReplacePorts(context.Background(), nil); e != nil {
		t.Fatal(e)
	}
	if _, e := m.DisconnectProvider("ipv6"); e != nil {
		t.Fatal(e)
	}
	if got := savedIPv6Mappings(t, dir); !reflect.DeepEqual(got, want) {
		t.Fatal("ipv6.json lost the services", got)
	}
	if got := process(dir).StatusFor("ipv6"); got.State != "disconnected" || !reflect.DeepEqual(got.Mappings, want) || len(got.ServiceIDs) != 2 {
		t.Fatal("a restart reported other services", got.State, got.Mappings, got.ServiceIDs)
	}
}
