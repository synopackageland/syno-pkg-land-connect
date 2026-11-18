package apps

import (
	"context"
	"dsmapp/internal/connector"
	"dsmapp/internal/ipctest"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

type fixtureApprover struct {
	failPreflight bool
	preflightErr  error
}

func (a *fixtureApprover) Inspect(_ context.Context, app App, kind string, previous *Scope) (Approval, error) {
	scope := fixtureScope(kind)
	scope.Binding.Port = app.Port
	scope.Binding.Summary = app.Summary
	if previous != nil {
		scope.Binding.Pin = previous.Binding.Pin
	}
	return Approval{Scope: *scope, Certificate: Certificate{Mode: scope.Binding.Pin.Mode, SPKI: scope.Binding.Pin.SPKI, Issuer: "Example issuer", Expires: "2030-01-01T00:00:00Z"}}, nil
}
func (a *fixtureApprover) CheckPublic(context.Context, []Target) error {
	if a.failPreflight {
		return ErrInvalid
	}
	return a.preflightErr
}

func fixtureService(t *testing.T) *Service {
	t.Helper()
	s := &Service{Discovery: Discovery{PackagesDir: t.TempDir(), DetectPorts: fixturePorts, Ping: func(context.Context, string, int) string { return Ready }}, Lifecycle: fixtureLifecycle(t.TempDir()), Approver: &fixtureApprover{}}
	g := gatewayFixture("ExampleApp", "", 28443)
	g.External = &ExternalPolicy{Connect: "optional", OriginChange: "none"}
	writeDiscoveryGateway(t, s.Discovery.root(), g.Package, g)
	return s
}

func TestListAndUnconfirmedMutationNeverCreateAnything(t *testing.T) {
	s := fixtureService(t)
	if _, e := s.List(context.Background()); e != nil {
		t.Fatal(e)
	}
	preview, e := s.changePrivate(context.Background(), Change{Package: "ExampleApp", Enabled: true})
	if e != nil || preview.Confirmation == nil {
		t.Fatal(preview, e)
	}
	entries, _ := os.ReadDir(s.Lifecycle.Store.Dir)
	if len(entries) != 0 {
		t.Fatal("preview created persistent or provider state", entries)
	}
	result, e := s.changePrivate(context.Background(), Change{Package: "ExampleApp", Enabled: true, Confirmation: preview.Confirmation.Token})
	if e != nil || len(result.Results) != 1 || result.Results[0].State != Active {
		t.Fatal(result, e)
	}
	p, e := s.Lifecycle.Store.Load()
	if e != nil || p.Apps["ExampleApp"].Private == nil {
		t.Fatal(p, e)
	}
}

func TestStaleConfirmationAndUnreadyCapabilitiesRefuseApproval(t *testing.T) {
	s := fixtureService(t)
	preview, e := s.changePrivate(context.Background(), Change{Package: "ExampleApp", Enabled: true})
	if e != nil {
		t.Fatal(e)
	}
	g, e := s.Discovery.ReadGateway("ExampleApp")
	if e != nil {
		t.Fatal(e)
	}
	g.Passthrough = []string{"/new"}
	writeDiscoveryGateway(t, s.Discovery.root(), g.Package, g)
	if _, e = s.changePrivate(context.Background(), Change{Package: "ExampleApp", Enabled: true, Confirmation: preview.Confirmation.Token}); !errors.Is(e, ErrConfirmationChanged) {
		t.Fatal(e)
	}
	for _, status := range []string{Waiting, NeedsUpdate} {
		s.Discovery.Ping = func(context.Context, string, int) string { return status }
		_, e = s.changePrivate(context.Background(), Change{Package: "ExampleApp", Enabled: true})
		want := ErrNotReady
		if status == NeedsUpdate {
			want = ErrNeedsUpdate
		}
		if !errors.Is(e, want) {
			t.Fatal(status, e)
		}
	}
	s.Discovery.Ping = func(context.Context, string, int) string { return Ready }
	g.External = nil
	writeDiscoveryGateway(t, s.Discovery.root(), g.Package, g)
	if _, e = s.changePublic(context.Background(), PublicChange{Packages: []string{"ExampleApp"}}); !errors.Is(e, ErrNeedsUpdate) {
		t.Fatal(e)
	}
	entries, _ := os.ReadDir(s.Lifecycle.Store.Dir)
	if len(entries) != 0 {
		t.Fatal("rejected approval created state", entries)
	}
}

func TestPublicBatchPreflightBeforeAnyDNSAndReenableReusesDNS(t *testing.T) {
	s := fixtureService(t)
	var events []string
	s.Lifecycle.Runtime.(*fixtureRuntime).events = &events
	preview, e := s.changePublic(context.Background(), PublicChange{Packages: []string{"ExampleApp"}})
	if e != nil {
		t.Fatal(e)
	}
	s.Approver.(*fixtureApprover).failPreflight = true
	if _, e = s.changePublic(context.Background(), PublicChange{Packages: []string{"ExampleApp"}, Confirmation: preview.Confirmation.Token}); e == nil {
		t.Fatal("collision accepted")
	}
	if len(events) != 0 {
		t.Fatal("preflight created DNS/runtime", events)
	}
	s.Approver.(*fixtureApprover).failPreflight = false
	if _, e = s.changePublic(context.Background(), PublicChange{Packages: []string{"ExampleApp"}, Confirmation: preview.Confirmation.Token}); e != nil {
		t.Fatal(e)
	}
	if e = s.Lifecycle.Close(context.Background(), "ExampleApp", Public, "certificate-changed"); e != nil {
		t.Fatal(e)
	}
	events = nil
	preview, e = s.changeReenable(context.Background(), ScopedChange{Package: "ExampleApp", Kind: Public})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.changeReenable(context.Background(), ScopedChange{Package: "ExampleApp", Kind: Public, Confirmation: preview.Confirmation.Token}); e != nil {
		t.Fatal(e)
	}
	for _, event := range events {
		if event == "dns" {
			t.Fatal("reenable repeated DNS")
		}
	}
}

func TestDaemonControlRoundTripAndCorruptionCode(t *testing.T) {
	s := fixtureService(t)
	socketDir, e := os.MkdirTemp("/tmp", "apps-control-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(socketDir)
	ln, e := ipctest.Listen(t, ipctest.Path(t, filepath.Join(socketDir, "control.sock")))
	if e != nil {
		t.Fatal(e)
	}
	srv := &http.Server{Handler: Handler(s)}
	defer srv.Close()
	go srv.Serve(ln)
	client := Client{Socket: ipctest.Path(t, filepath.Join(socketDir, "control.sock"))}
	list, e := client.List(context.Background())
	if e != nil || len(list.Apps) != 1 {
		t.Fatal(list, e)
	}
	inspect, e := client.Inspect(context.Background(), InspectRequest{Package: "ExampleApp", Scope: Private})
	if e != nil || !inspect.Ready || inspect.Pin == nil {
		t.Fatal(inspect, e)
	}
	approval := &Approved{Port: inspect.Port, Pin: *inspect.Pin, Token: inspect.Token}
	if _, e = client.SetPrivate(context.Background(), PrivateRequest{Package: "ExampleApp", Enabled: true, Approval: approval}); e != nil {
		t.Fatal(e)
	}
	if e = client.Clear(context.Background(), ScopedChange{Package: "ExampleApp"}); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(s.Lifecycle.Store.Dir, "apps-state.json"), []byte("{"), 0600); e != nil {
		t.Fatal(e)
	}
	list, e = client.List(context.Background())
	if e != nil || !list.StateCorrupt {
		t.Fatal(list, e)
	}
	if _, e = client.SetPrivate(context.Background(), PrivateRequest{Package: "ExampleApp", Enabled: true, Approval: approval}); !errors.Is(e, ErrCorrupt) {
		t.Fatal("corruption code lost", e)
	}
}

// A provider's refusal, such as a public name already in DNS, crosses the
// control socket with its code; anything else still reads as unavailable.
func TestDaemonControlKeepsProviderRefusalCode(t *testing.T) {
	s := fixtureService(t)
	socketDir, e := os.MkdirTemp("/tmp", "apps-control-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(socketDir)
	ln, e := ipctest.Listen(t, ipctest.Path(t, filepath.Join(socketDir, "control.sock")))
	if e != nil {
		t.Fatal(e)
	}
	srv := &http.Server{Handler: Handler(s)}
	defer srv.Close()
	go srv.Serve(ln)
	client := Client{Socket: ipctest.Path(t, filepath.Join(socketDir, "control.sock"))}
	for refusal, want := range map[error]string{
		connector.Coded("cloudflare_name_taken", "this name is already used"): "cloudflare_name_taken",
		errors.New("plain failure"): "apps_unavailable",
	} {
		s.Approver.(*fixtureApprover).preflightErr = refusal
		inspect, e := client.Inspect(context.Background(), InspectRequest{Package: "ExampleApp", Scope: Public})
		if e != nil {
			t.Fatal(e)
		}
		_, e = client.SetPublic(context.Background(), PublicRequest{Packages: []string{"ExampleApp"}, Enabled: true, Approvals: map[string]Approved{"ExampleApp": {Port: inspect.Port, Pin: *inspect.Pin, Token: inspect.Token}}})
		if ErrorCode(e) != want {
			t.Fatal(want, e)
		}
	}
}

func TestApprovalRefusesChangedInspectedPortOrPin(t *testing.T) {
	for _, change := range []string{"port", "pin", "metadata"} {
		t.Run(change, func(t *testing.T) {
			s := fixtureService(t)
			ctx := context.Background()
			inspected, e := s.Inspect(ctx, InspectRequest{Package: "ExampleApp", Scope: Private})
			if e != nil {
				t.Fatal(e)
			}
			given := &Approved{Port: inspected.Port, Pin: *inspected.Pin, Token: inspected.Token}
			switch change {
			case "port":
				given.Port++
			case "pin":
				given.Pin.SPKI = "wrong"
			case "metadata":
				g, _ := s.Discovery.ReadGateway("ExampleApp")
				g.Passthrough = []string{"/new-public-path"}
				writeDiscoveryGateway(t, s.Discovery.root(), g.Package, g)
			}
			if _, e = s.SetPrivate(ctx, PrivateRequest{Package: "ExampleApp", Enabled: true, Approval: given}); !errors.Is(e, ErrConfirmationChanged) {
				t.Fatal("stale approval accepted", e)
			}
			p, e := s.Lifecycle.Store.Load()
			if e != nil || len(p.Apps) != 0 {
				t.Fatal("rejected approval wrote state", p, e)
			}
		})
	}
}

func TestPrivateActivationFailureIsAnAPIError(t *testing.T) {
	s := fixtureService(t)
	ctx := context.Background()
	inspect, e := s.Inspect(ctx, InspectRequest{Package: "ExampleApp", Scope: Private})
	if e != nil {
		t.Fatal(e)
	}
	s.Lifecycle.Runtime.(*fixtureRuntime).failActivate = true
	_, e = s.SetPrivate(ctx, PrivateRequest{Package: "ExampleApp", Enabled: true, Approval: &Approved{Port: inspect.Port, Pin: *inspect.Pin, Token: inspect.Token}})
	if !errors.Is(e, ErrUnavailable) {
		t.Fatal("UI would report success after failed activation", e)
	}
	state, e := s.Lifecycle.Store.Load()
	if e != nil || state.Apps["ExampleApp"].Private.State != Paused {
		t.Fatal(state, e)
	}
}

// A runtime without DSM-only approval never invents one; malformed requests
// are refused before any runtime call.
func TestDSMOnlyApprovalRefusesBadInputAndMissingRuntime(t *testing.T) {
	s := fixtureService(t)
	ctx := context.Background()
	if _, e := s.InspectDSM(ctx, DSMInspectRequest{Scope: "both"}); !errors.Is(e, ErrInvalid) {
		t.Fatal("unknown scope accepted", e)
	}
	if _, e := s.SetDSM(ctx, DSMRequest{Scope: Public, Token: "t"}); !errors.Is(e, ErrInvalid) {
		t.Fatal("approval without the displayed DSM accepted", e)
	}
	if _, e := s.InspectDSM(ctx, DSMInspectRequest{Scope: Public}); !errors.Is(e, ErrUnavailable) {
		t.Fatal("runtime without DSM approval answered", e)
	}
	if _, e := s.SetDSM(ctx, DSMRequest{Scope: Public, DSM: &DSMApproval{Port: 5001}, Token: "t"}); !errors.Is(e, ErrUnavailable) {
		t.Fatal("runtime without DSM approval approved", e)
	}
	if p, e := s.Lifecycle.Store.Load(); e != nil || p.DSM != nil {
		t.Fatal(p.DSM, e)
	}
}
