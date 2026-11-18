package provider

import (
	"context"
	"dsmapp/internal/connector"
	"os"
	"path/filepath"
	"testing"
)

func TestIndependentConnectionsAndDurableDisable(t *testing.T) {
	dir := t.TempDir()
	events := []string{}
	cf := &fixtureRuntime{id: "cloudflare", events: &events}
	ts := &fixtureRuntime{id: "tailscale", events: &events}
	m := NewMultiSelection(dir, cf, ts)
	for _, id := range []string{"cloudflare", "tailscale"} {
		if _, err := m.Connect(context.Background(), connector.Request{Provider: id, ConfirmExposure: true}); err != nil {
			t.Fatal(err)
		}
	}
	if !cf.live || !ts.live || len(m.Status().Connections) != 4 {
		t.Fatal("connections did not coexist", events)
	}
	if _, err := m.DisconnectProvider("cloudflare"); err != nil || cf.live || !ts.live {
		t.Fatal("scoped disable", err, events)
	}
	if err := m.Stop(); err != nil {
		t.Fatal(err)
	}
	events = nil
	fresh := NewMultiSelection(dir, cf, ts)
	if err := fresh.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if cf.live || !ts.live || len(events) != 1 || events[0] != "tailscale restore" {
		t.Fatal("durable independent intent", events)
	}
}

func TestUpgradeFromThreeConnectionSchemaPreservesTailscaleAndDefaultsNetBirdOff(t *testing.T) {
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
	if e := os.RemoveAll(filepath.Join(dir, "connections", "netbird")); e != nil {
		t.Fatal(e)
	}
	upgraded := NewMultiSelection(dir, ts)
	if e := upgraded.Restore(context.Background()); e != nil {
		t.Fatal(e)
	}
	if !ts.live || upgraded.StatusFor("netbird").State != "disconnected" {
		t.Fatal("migration lost scope")
	}
	s, e := readGate(filepath.Join(dir, "connections", "netbird", "provider.json"))
	if e != nil || s.Enabled {
		t.Fatal("new connection not durably disabled", e)
	}
}

func TestMultiMigratesLegacyIntentOnceAndFailsClosed(t *testing.T) {
	for _, raw := range []string{`{"provider":"tailscale","enabled":true}`, `{"provider":"tailscale","enabled":false}`, `{"provider":"bogus","enabled":true}`, `{"provider":"cloudflare","enabled":true} {}`} {
		dir := t.TempDir()
		if err := connector.WritePrivate(filepath.Join(dir, "provider.json"), []byte(raw)); err != nil {
			t.Fatal(err)
		}
		events := []string{}
		cf := &fixtureRuntime{id: "cloudflare", events: &events}
		ts := &fixtureRuntime{id: "tailscale", events: &events}
		m := NewMultiSelection(dir, cf, ts)
		err := m.Restore(context.Background())
		valid := raw == `{"provider":"tailscale","enabled":true}` || raw == `{"provider":"tailscale","enabled":false}`
		if (err == nil) != valid || cf.live || (!valid && ts.live) {
			t.Fatal(raw, err, events)
		}
		if valid {
			if ts.live != (raw == `{"provider":"tailscale","enabled":true}`) {
				t.Fatal("lost legacy intent", raw)
			}
			if _, err = m.DisconnectProvider("tailscale"); err != nil {
				t.Fatal(err)
			}
			// A stale legacy gate must never override the new disabled gate.
			connector.WritePrivate(filepath.Join(dir, "provider.json"), []byte(raw))
			events = nil
			if err = NewMultiSelection(dir, cf, ts).Restore(context.Background()); err != nil || len(events) != 0 {
				t.Fatal("legacy replay", err, events)
			}
		}
	}
}

func TestMultiFailedDisableDoesNotAffectOtherConnection(t *testing.T) {
	events := []string{}
	cf := &fixtureRuntime{id: "cloudflare", events: &events}
	ts := &fixtureRuntime{id: "tailscale", events: &events}
	m := NewMultiSelection(t.TempDir(), cf, ts)
	m.Connect(context.Background(), connector.Request{Provider: "cloudflare", ConfirmExposure: true})
	m.Connect(context.Background(), connector.Request{Provider: "tailscale", ConfirmExposure: true})
	cf.failStop = true
	if _, err := m.DisconnectProvider("cloudflare"); err == nil || !ts.live || m.StatusFor("cloudflare").State != "failed" {
		t.Fatal("failure isolation")
	}
	if _, err := m.DisconnectProvider("../../tailscale"); err == nil || !ts.live {
		t.Fatal("invalid provider accepted")
	}
}

func TestMultiInterruptedMigrationAndOfflineRevokeNeverRevive(t *testing.T) {
	dir := t.TempDir()
	events := []string{}
	cf := &fixtureRuntime{id: "cloudflare", events: &events}
	ts := &fixtureRuntime{id: "tailscale", events: &events}
	connector.WritePrivate(filepath.Join(dir, "connections-migration.json"), []byte(`{"provider":"tailscale","enabled":true}`))
	connector.WritePrivate(filepath.Join(dir, "provider.json"), []byte(`{"provider":"tailscale","enabled":false}`))
	m := NewMultiSelection(dir, cf, ts)
	if err := m.Restore(context.Background()); err != nil || !ts.live {
		t.Fatal("interrupted migration lost intent", err)
	}
	m.Stop()
	if err := RevokeOffline(dir); err != nil {
		t.Fatal(err)
	}
	events = nil
	if err := NewMultiSelection(dir, cf, ts).Restore(context.Background()); err != nil || len(events) != 0 {
		t.Fatal("revoked migration replay", err, events)
	}
	if _, err := os.Stat(filepath.Join(dir, "connections.json")); err != nil {
		t.Fatal(err)
	}
}

func TestIndependentIPv6ScopeNeverIncludesNewService(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := &ownerSource{ports: ownerPort(5000)}
	v6 := NewIPv6Runtime(dir, ownerAddresses{})
	m := NewMultiSelection(dir, v6)
	m.SetPortSource(source)
	if _, e := m.Connect(ctx, connector.Request{Provider: "ipv6", ConfirmExposure: true, ServiceIDs: []string{"unknown"}}); e == nil {
		t.Fatal("unresolved ID accepted")
	}
	if _, e := m.Connect(ctx, connector.Request{Provider: "ipv6", ConfirmExposure: true, ServiceIDs: []string{"dsm-http"}}); e != nil {
		t.Fatal(e)
	}
	source.ports = append(ownerPort(6000), ResolvedPort{ID: "new-service", Mapping: PortMapping{LocalPort: 7443, PublicPort: 7443, Protocol: "https"}})
	if e := m.ReplacePorts(ctx, source.ports); e != nil {
		t.Fatal(e)
	}
	got := m.StatusFor("ipv6")
	if len(got.Endpoints) != 1 || got.Endpoints[0].LocalPort != 6000 {
		t.Fatal("scope expanded", got)
	}
	m.Stop()
	fresh := NewMultiSelection(dir, NewIPv6Runtime(dir, ownerAddresses{}))
	fresh.SetPortSource(source)
	if e := fresh.Restore(ctx); e != nil {
		t.Fatal(e)
	}
	if got = fresh.StatusFor("ipv6"); len(got.Endpoints) != 1 || got.Endpoints[0].LocalPort != 6000 {
		t.Fatal("restored scope expanded", got)
	}
}

func TestMultiCorruptChildDoesNotRestoreThatRuntime(t *testing.T) {
	dir := t.TempDir()
	events := []string{}
	cf := &fixtureRuntime{id: "cloudflare", events: &events}
	ts := &fixtureRuntime{id: "tailscale", events: &events}
	m := NewMultiSelection(dir, cf, ts)
	m.Connect(context.Background(), connector.Request{Provider: "tailscale", ConfirmExposure: true})
	m.Stop()
	connector.WritePrivate(filepath.Join(dir, "connections", "cloudflare", "provider.json"), []byte(`{"provider":"cloudflare","enabled":true,"extra":1}`))
	events = nil
	fresh := NewMultiSelection(dir, cf, ts)
	if e := fresh.Restore(context.Background()); e == nil || cf.live || !ts.live || fresh.StatusFor("cloudflare").State != "failed" {
		t.Fatal("corrupt child not isolated", e, events)
	}
}

func TestMultiBinaryUpdateOnlyStopsItsOwner(t *testing.T) {
	events := []string{}
	cf := &fixtureRuntime{id: "cloudflare", events: &events}
	ts := &fixtureRuntime{id: "tailscale", events: &events}
	m := NewMultiSelection(t.TempDir(), cf, ts)
	m.Connect(context.Background(), connector.Request{Provider: "cloudflare", ConfirmExposure: true})
	m.Connect(context.Background(), connector.Request{Provider: "tailscale", ConfirmExposure: true})
	events = nil
	if e := m.UpdateBinaries(context.Background(), "tailscale", func() (func() error, func() error, error) {
		return func() error { return nil }, func() error { return nil }, nil
	}); e != nil {
		t.Fatal(e)
	}
	for _, e := range events {
		if e == "cloudflare stop" {
			t.Fatal("update stopped unrelated connection", events)
		}
	}
	if !cf.live || !ts.live {
		t.Fatal("update lost connection", events)
	}
}

func TestLegacyIPv6MissingServiceNeverExpandsScopeAfterRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	saved := `{"provider":"ipv6","enabled":true,"settings":{"confirmExposure":true,"mappings":[{"localPort":5000,"publicPort":5000,"protocol":"http"}]}}`
	connector.WritePrivate(filepath.Join(dir, "provider.json"), []byte(saved))
	source := &ownerSource{ports: []ResolvedPort{{ID: "new-service", Mapping: PortMapping{LocalPort: 7443, PublicPort: 7443, Protocol: "https"}}}}
	for i := 0; i < 2; i++ {
		v6 := NewIPv6Runtime(dir, ownerAddresses{})
		m := NewMultiSelection(dir, v6)
		m.SetPortSource(source)
		_ = m.Restore(ctx)
		if len(v6.Endpoints()) != 0 {
			t.Fatal("legacy empty scope expanded", i, v6.Endpoints())
		}
		m.Stop()
	}
}

func TestForgetClearsADisabledConnectionsServicesAcrossRestart(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := &ownerSource{ports: ownerPort(5000)}
	m := NewMultiSelection(dir, NewIPv6Runtime(dir, ownerAddresses{}))
	m.SetPortSource(source)
	if _, e := m.Connect(ctx, connector.Request{Provider: "ipv6", ConfirmExposure: true, ServiceIDs: []string{"dsm-http"}}); e != nil {
		t.Fatal(e)
	}
	if _, e := m.Forget("ipv6"); e == nil {
		t.Fatal("forgot the services of a live connection")
	}
	if _, e := m.DisconnectProvider("ipv6"); e != nil {
		t.Fatal(e)
	}
	if got := m.StatusFor("ipv6"); len(got.Mappings) != 1 || len(got.ServiceIDs) != 1 {
		t.Fatal("disconnect should keep the saved services", got)
	}
	s, e := m.Forget("ipv6")
	if e != nil {
		t.Fatal(e)
	}
	for _, c := range s.Connections {
		if c.Provider == "ipv6" && (len(c.Mappings) != 0 || len(c.ServiceIDs) != 0 || c.State != "disconnected") {
			t.Fatal("forget", c)
		}
	}
	fresh := NewMultiSelection(dir, NewIPv6Runtime(dir, ownerAddresses{}))
	fresh.SetPortSource(source)
	if e := fresh.Restore(ctx); e != nil {
		t.Fatal(e)
	}
	if got := fresh.StatusFor("ipv6"); len(got.Mappings) != 0 || len(got.ServiceIDs) != 0 || got.State != "disconnected" {
		t.Fatal("forgotten services came back", got)
	}
}

func TestForgetRefusesConnectionsWithoutIt(t *testing.T) {
	m := NewMultiSelection(t.TempDir(), &fixtureRuntime{id: "tailscale", events: &[]string{}})
	for _, id := range []string{"tailscale", "unknown"} {
		if _, e := m.Forget(id); e == nil {
			t.Fatal("forget accepted", id)
		}
	}
}
