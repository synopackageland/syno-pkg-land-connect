package provider

import (
	"context"
	"dsmapp/internal/apps"
	"reflect"
	"testing"
	"time"

	"dsmapp/internal/connector"
)

type appSession struct {
	tsSession
	seen []PortMapping
}

func (s *appSession) Configure(_ context.Context, m []PortMapping) error {
	s.seen = append([]PortMapping(nil), m...)
	return nil
}

func TestServeCarriesExactlyTheApprovedApps(t *testing.T) {
	ctx := context.Background()
	session := &appSession{tsSession: tsSession{snapshot: TailscaleSnapshot{Version: "1.104.1", BackendState: "Running", DNSName: "nas.example.ts.net.", CertDomains: []string{"nas.example.ts.net"}, IPs: []string{"100.64.0.1"}, Serving: true}}}
	p := NewTailscale(t.TempDir(), nil)
	p.session = session
	p.saved = tailscaleSaved{Enabled: true}
	app := PortMapping{Protocol: "unix", LocalPort: 28443, PublicPort: 28443, Socket: apps.PinSocket("app")}
	dsm := PortMapping{Protocol: "unix", LocalPort: 5001, PublicPort: 5001, Socket: apps.PinSocket("dsm")}
	p.AppMappings = func(context.Context) ([]PortMapping, error) { return []PortMapping{app, dsm}, nil }
	if err := p.RefreshApps(ctx); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(session.seen, []PortMapping{app, dsm}) {
		t.Fatal("Serve did not carry the approved Apps", session.seen)
	}
	if status := p.Status(); status.State != "connected" {
		t.Fatal("App-only mapping was not reported connected", status)
	}
	network, err := p.AppAccessNetwork(ctx)
	if err != nil || !network.Available {
		t.Fatal(network, err)
	}
	p.AppMappings = func(context.Context) ([]PortMapping, error) { return nil, nil }
	if err = p.RefreshApps(ctx); err != nil || len(session.seen) != 0 {
		t.Fatal("the last App's route was not withdrawn", session.seen, err)
	}
	p.saved.Enabled = false
	if network, err = p.AppAccessNetwork(ctx); err != nil || network.Available {
		t.Fatal("disconnected provider was available", network, err)
	}
}

type refusingSession struct {
	tsSession
	refuse error
}

func (s *refusingSession) Configure(ctx context.Context, m []PortMapping) error {
	if s.refuse != nil {
		return s.refuse
	}
	return s.tsSession.Configure(ctx, m)
}

// dsmRoute is the DSM route an approved private App brings along.
func dsmRoute(context.Context) ([]PortMapping, error) {
	return []PortMapping{{Protocol: "unix", LocalPort: 5001, PublicPort: 5001, Socket: apps.PinSocket("dsm")}}, nil
}

func TestTailscaleDSMReviewFailsAtOnceAndRecoversWithoutRestart(t *testing.T) {
	session := &refusingSession{tsSession: tsSession{snapshot: TailscaleSnapshot{Version: "1.104.1", BackendState: "Running", DNSName: "nas.example.ts.net.", CertDomains: []string{"nas.example.ts.net"}, IPs: []string{"100.64.0.1"}, Serving: true}}, refuse: connector.Coded("pin_proxy_required", "fixture")}
	p := NewTailscale(t.TempDir(), nil)
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	p.now = clock.Now
	p.session = session
	p.saved = tailscaleSaved{Enabled: true}
	p.AppMappings = dsmRoute
	for i := 0; i < 3; i++ {
		if s := p.Status(); s.State != "failed" || s.Code != "pin_proxy_required" {
			t.Fatal("DSM review was not reported at once", i, s.State, s.Code)
		}
		clock.Advance(time.Minute)
	}
	if session.stopped || !p.saved.Enabled {
		t.Fatal("waiting for DSM review stopped or disabled the connection")
	}
	session.refuse = nil
	if s := p.Status(); s.State != "connected" || s.Code != "" {
		t.Fatal("approval did not bring the route up", s.State, s.Code)
	}
}

// A DSM pin revoked while Serve runs fails the route at once with
// pin_proxy_required, keeps the tailnet up, never turns into a feature
// approval timeout, and recovers on the next approved refresh.
func TestRevokedPinFailsRunningPrivateRouteUntilReapproved(t *testing.T) {
	ctx := context.Background()
	session := &refusingSession{tsSession: tsSession{snapshot: TailscaleSnapshot{Version: "1.104.1", BackendState: "Running", DNSName: "nas.example.ts.net.", CertDomains: []string{"nas.example.ts.net"}, IPs: []string{"100.64.0.1"}, Serving: true}}}
	now := time.Unix(1000, 0)
	p := NewTailscale(t.TempDir(), nil)
	p.now = func() time.Time { return now }
	p.session = session
	p.saved = tailscaleSaved{Enabled: true}
	p.AppMappings = dsmRoute
	if err := p.RefreshApps(ctx); err != nil {
		t.Fatal(err)
	}
	if s := p.Status(); s.State != "connected" {
		t.Fatal("route did not connect", s)
	}
	now = now.Add(time.Hour)
	session.refuse = connector.Coded("pin_proxy_required", "fixture")
	if err := p.RefreshApps(ctx); connector.ErrorCode(err) != "pin_proxy_required" {
		t.Fatal(err)
	}
	for range 2 {
		if s := p.Status(); s.State != "failed" || s.Code != "pin_proxy_required" {
			t.Fatalf("revoked pin not surfaced: %+v", s)
		}
		now = now.Add(time.Minute)
	}
	if session.stopped || !p.saved.Enabled || p.failed {
		t.Fatal("waiting for review disconnected the tailnet")
	}
	session.refuse = nil
	if err := p.RefreshApps(ctx); err != nil {
		t.Fatal(err)
	}
	if s := p.Status(); s.State != "connected" || s.Code != "" {
		t.Fatalf("approval left the route failed: %+v", s)
	}
}
