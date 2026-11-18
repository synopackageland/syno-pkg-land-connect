package main

import (
	"context"
	"dsmapp/internal/binaries"
	"dsmapp/internal/connector"
	"dsmapp/internal/provider"
	"testing"
)

type stalledSource struct{ started chan struct{} }

func (s stalledSource) Latest(ctx context.Context, _ string) (binaries.Release, error) {
	close(s.started)
	<-ctx.Done()
	return binaries.Release{}, ctx.Err()
}
func (stalledSource) Download(context.Context, binaries.Release, string) (string, error) {
	panic("unexpected download")
}
func TestPackageStopCancelsBinaryJob(t *testing.T) {
	source := stalledSource{started: make(chan struct{})}
	store := binaries.New(t.TempDir(), source)
	svc := binaries.NewService(store, nil)
	m := &managedSelection{lifecycleSelection: provider.NewSelection(t.TempDir()), binaries: svc}
	if _, e := svc.Call(context.Background(), "update", "tailscale"); e != nil {
		t.Fatal(e)
	}
	<-source.started
	if e := m.Stop(); e != nil {
		t.Fatal(e)
	}
	if _, e := svc.Call(context.Background(), "update", "tailscale"); e == nil {
		t.Fatal("stopped package accepted download")
	}
}

type scopedFixture struct {
	*provider.Selection
	disconnected string
	forgot       string
}

func (p *scopedFixture) Forget(id string) (connector.Status, error) {
	p.forgot = id
	return connector.Status{Provider: id, State: "disconnected"}, nil
}

func (p *scopedFixture) DisconnectProvider(id string) (connector.Status, error) {
	p.disconnected = id
	return connector.Status{Provider: id, State: "disconnected"}, nil
}
func TestManagedSelectionForwardsScopedControlExtensions(t *testing.T) {
	p := &scopedFixture{Selection: provider.NewSelection(t.TempDir())}
	m := &managedSelection{lifecycleSelection: p}
	if _, e := m.DisconnectProvider("tailscale"); e != nil || p.disconnected != "tailscale" {
		t.Fatal("scoped disable not forwarded", e)
	}
	if _, e := m.Forget("ipv6"); e != nil || p.forgot != "ipv6" {
		t.Fatal("forget not forwarded", e)
	}
}

func TestManagedSelectionChecksAndANewConnectionStartsAFreshCheck(t *testing.T) {
	probes := 0
	checker := connector.NewChecker(func(context.Context, string) (bool, error) { probes++; return false, nil })
	defer checker.Close()
	checker.Retries = 1
	m := &managedSelection{lifecycleSelection: provider.NewSelection(t.TempDir()), checker: checker}
	if got, e := m.Check(context.Background(), "tailscale"); e != nil || got.State != "ok" {
		t.Fatalf("got %+v %v", got, e)
	}
	m.Connect(context.Background(), connector.Request{Provider: "tailscale"})
	if got := checker.Last("tailscale"); got.State != "not-checked" {
		t.Fatalf("a new connection kept the old check: %+v", got)
	}
	m.Check(context.Background(), "tailscale")
	if probes != 2 {
		t.Fatalf("probe ran %d times, want 2", probes)
	}
}

func TestAdminConnectAloneCanEstablishDSMTrust(t *testing.T) {
	calls := 0
	checker := connector.NewChecker(nil)
	defer checker.Close()
	m := &managedSelection{lifecycleSelection: provider.NewSelection(t.TempDir()), checker: checker, trustDSM: func(context.Context) error { calls++; return nil }}
	for _, r := range []connector.Request{{Provider: "cloudflare", Mode: "quick"}, {Provider: "cloudflare", Mode: "token", ConfirmExposure: true}, {Provider: "ipv6", ConfirmExposure: true}, {Provider: "netbird"}, {Provider: "cloudflare", Mode: "domain"}} {
		_, _ = m.Connect(context.Background(), r)
	}
	if calls != 0 {
		t.Fatal("unrelated or unconfirmed actions trusted DSM", calls)
	}
	for _, r := range []connector.Request{{Provider: "cloudflare", Mode: "quick", ConfirmExposure: true}, {Provider: "cloudflare", Mode: "domain", ConfirmExposure: true}, {Provider: "tailscale"}} {
		_, _ = m.Connect(context.Background(), r)
	}
	if calls != 3 {
		t.Fatal("administrator connect did not establish trust", calls)
	}
	_ = m.Restore(context.Background())
	_ = m.Status()
	if calls != 3 {
		t.Fatal("restore or status established trust", calls)
	}
}

func TestDependencyRefusalDoesNotEnterAsyncDisconnect(t *testing.T) {
	p := &scopedFixture{Selection: provider.NewSelection(t.TempDir())}
	m := &managedSelection{lifecycleSelection: p, beforeDisconnect: func(id string) error {
		if id == "" || id == "cloudflare" {
			return connector.Coded("netbird_cloudflare_in_use", "turn off the server first")
		}
		return nil
	}}
	if _, e := m.DisconnectProvider("cloudflare"); connector.ErrorCode(e) != "netbird_cloudflare_in_use" || p.disconnected != "" {
		t.Fatal(e, p.disconnected)
	}
	if _, e := m.Disconnect(); connector.ErrorCode(e) != "netbird_cloudflare_in_use" {
		t.Fatal(e)
	}
	if _, e := m.DisconnectProvider("tailscale"); e != nil || p.disconnected != "tailscale" {
		t.Fatal(e, p.disconnected)
	}
}
