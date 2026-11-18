package provider

import (
	"context"
	"strings"
	"testing"

	"dsmapp/internal/apps"
	"dsmapp/internal/connector"
)

func TestPrivateTargetsUseOnlyCurrentApprovedAuthorityAndMigrateDSM(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := apps.Store{Dir: dir}
	pin := apps.Pin{Mode: "S", SPKI: strings.Repeat("b", 64)}
	if e := store.Update(ctx, func(s *apps.State) error {
		rev, e := s.Revision()
		if e != nil {
			return e
		}
		s.DSM = &apps.DSMState{Port: 5001, Pin: pin, Private: &apps.Endpoint{Socket: "dsm", Authority: "nas.example.ts.net:5001"}}
		s.Apps["Example"] = apps.AppState{Rev: rev, Private: &apps.Scope{State: apps.Active, Binding: apps.Binding{Port: 7443, Summary: strings.Repeat("a", 64), Pin: pin}, Socket: "app", Authority: "nas.example.ts.net:7443"}}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	source := ApprovedPrivateTargets{Store: store, DSMPort: func(context.Context) (int, error) { return 5001, nil }}
	input := []PortMapping{{Protocol: "https", LocalPort: 5001, PublicPort: 5001}, {Protocol: "unix", LocalPort: 7443, PublicPort: 7443, Socket: apps.PinSocket("app")}}
	result, e := source.ResolvePrivateTargets(ctx, input, "nas.example.ts.net")
	if e != nil || result[0].Protocol != "unix" || result[0].Socket != apps.PinSocket("dsm") || input[0].Protocol != "https" {
		t.Fatal(result, e)
	}
	changed := source
	changed.DSMPort = func(context.Context) (int, error) { return 7444, nil }
	for _, old := range [][]PortMapping{input, result} {
		if _, e := changed.ResolvePrivateTargets(ctx, old, "nas.example.ts.net"); connector.ErrorCode(e) != "pin_proxy_required" {
			t.Fatal("changed DSM port retained a route", e)
		}
	}
	if _, e = source.ResolvePrivateTargets(ctx, input, "renamed.example.ts.net"); connector.ErrorCode(e) != "pin_proxy_required" {
		t.Fatal("renamed tailnet was accepted", e)
	}
	if e = store.Update(ctx, func(s *apps.State) error { s.DSM.Private = nil; return nil }); e != nil {
		t.Fatal(e)
	}
	if _, e = source.ResolvePrivateTargets(ctx, input, "nas.example.ts.net"); connector.ErrorCode(e) != "pin_proxy_required" {
		t.Fatal("missing DSM approval was accepted", e)
	}
}

type refusingPrivateTargets struct{}

func (refusingPrivateTargets) ResolvePrivateTargets(context.Context, []PortMapping, string) ([]PortMapping, error) {
	return nil, apps.ErrUnavailable
}
func TestPrivateTargetRefusalWithdrawsTheOwnedRoute(t *testing.T) {
	f := &commandFixture{status: []byte(`{"Self":{"DNSName":"nas.example.ts.net."},"CertDomains":["nas.example.ts.net"]}`)}
	session, e := (TailscaleExec{Dir: t.TempDir(), Binaries: unixBinaries{version: "1.104.1"}, Commands: f}).Open(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer session.Stop()
	s := session.(*tailscaleExecSession)
	m := []PortMapping{{Protocol: "unix", LocalPort: 7443, PublicPort: 7443, Socket: apps.PinSocket("app")}}
	if e = s.Configure(context.Background(), m); e != nil {
		t.Fatal(e)
	}
	child := s.forwards[0].(*execFixtureProcess)
	s.targets = refusingPrivateTargets{}
	if e = s.Configure(context.Background(), m); e == nil || !child.stopped || len(s.mappings) != 0 {
		t.Fatal("unapproved child survived", e)
	}
}

func TestMissingPrivateAppBindingDoesNotAskForDSMCertificate(t *testing.T) {
	source := ApprovedPrivateTargets{Store: apps.Store{Dir: t.TempDir()}, DSMPort: func(context.Context) (int, error) { return 5001, nil }}
	_, err := source.ResolvePrivateTargets(context.Background(), []PortMapping{{Protocol: "unix", LocalPort: 7443, PublicPort: 7443, Socket: "/private/missing.sock"}}, "nas.example.ts.net")
	if connector.ErrorCode(err) != "apps_confirmation_changed" {
		t.Fatal("an App failure was misreported as a DSM review", err)
	}
}

func TestNativePrivateAppSharesBackendPortWithoutGrantingDSM(t *testing.T) {
	ctx := context.Background()
	store := apps.Store{Dir: t.TempDir()}
	pin := apps.Pin{Mode: "S", SPKI: strings.Repeat("b", 64)}
	if err := store.Update(ctx, func(s *apps.State) error {
		rev, _ := s.Revision()
		s.DSM = &apps.DSMState{Port: 5001, Pin: pin, Private: &apps.Endpoint{Socket: "dsm", Authority: "nas.example.ts.net:5001"}}
		s.Apps["Example"] = apps.AppState{Rev: rev, Private: &apps.Scope{State: apps.Active, Socket: "app", Authority: "nas.example.ts.net:31001", Binding: apps.Binding{Port: 5001, Summary: strings.Repeat("a", 64), Pin: pin, Entry: &apps.PackageEntry{Package: "Example", Port: 5001, Path: "/music/"}}}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	dsmChecks := 0
	source := ApprovedPrivateTargets{Store: store, DSMPort: func(context.Context) (int, error) { return 5001, nil }, EnsureDSM: func(context.Context, string, string) error { dsmChecks++; return nil }}
	m := []PortMapping{{Protocol: "unix", LocalPort: 5001, PublicPort: 31001, Socket: apps.PinSocket("app")}, {Protocol: "https", LocalPort: 5001, PublicPort: 5001}}
	got, err := source.ResolvePrivateTargets(ctx, m, "nas.example.ts.net")
	if err != nil || len(got) != 2 || got[0].Socket != apps.PinSocket("app") || got[1].Socket != apps.PinSocket("dsm") || dsmChecks != 1 {
		t.Fatalf("scopes crossed: %+v checks=%d %v", got, dsmChecks, err)
	}
	m[0].Socket = apps.PinSocket("foreign")
	if _, err := source.ResolvePrivateTargets(ctx, m, "nas.example.ts.net"); err == nil {
		t.Fatal("unknown socket accepted as package")
	}
}
