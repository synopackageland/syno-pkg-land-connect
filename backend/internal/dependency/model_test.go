package dependency

import (
	"context"
	"dsmapp/internal/connector"
	"testing"
	"time"
)

func TestRootCauseOrderAndOneRecoveryAction(t *testing.T) {
	cases := []struct{ state, mode, auth, token, bridge, signal, code, piece, action string }{
		{state: "disconnected", code: "cloudflare_not_connected", piece: "tunnel", action: "connect_cloudflare"},
		{state: "connecting", code: "cloudflare_connecting", piece: "tunnel", action: "check_status"},
		{state: "connected", mode: "quick", code: "cloudflare_own_domain_required", piece: "own_domain", action: "connect_domain"},
		{state: "connected", mode: "domain", auth: "cloudflare_reconnect_required", code: "cloudflare_reconnect_required", piece: "authorization", action: "authorize_cloudflare"},
		{state: "connected", mode: "domain", token: "workers_not_connected", code: "workers_not_connected", piece: "token", action: "save_token"},
		{state: "connected", mode: "domain", token: "workers_token_invalid", code: "workers_token_invalid", piece: "token", action: "replace_token"},
		{state: "connected", mode: "domain", token: "workers_routes_permission", code: "workers_routes_permission", piece: "token", action: "replace_token"},
		{state: "connected", mode: "domain", bridge: "netbird_server_bridge_missing", code: "netbird_server_bridge_missing", piece: "bridge", action: "repair_bridge"},
		{state: "connected", mode: "domain", signal: "netbird_server_signal_blocked", code: "netbird_server_signal_blocked", piece: "signal", action: "repair_bridge"},
		{state: "connected", mode: "domain", signal: "netbird_server_grpc_blocked", code: "netbird_server_grpc_blocked", piece: "own_domain", action: "enable_grpc"},
		{state: "connected", mode: "domain"},
	}
	for _, c := range cases {
		t.Run(c.code+c.state+c.mode, func(t *testing.T) {
			calls := []string{}
			err := func(code string) error {
				if code == "" {
					return nil
				}
				return connector.Coded(code, "fixture")
			}
			m := Model{Cloudflare: func() Cloudflare { return Cloudflare{State: c.state, Mode: c.mode, Zone: "example.com", Code: c.auth} },
				Token:  func(context.Context, string) error { calls = append(calls, "token"); return err(c.token) },
				Bridge: func(context.Context, string, string) error { calls = append(calls, "bridge"); return err(c.bridge) },
				Signal: func(context.Context, string) error { calls = append(calls, "signal"); return err(c.signal) }}
			r := m.Read(context.Background(), "netbird.example.com", true)
			if c.code == "" {
				if r.Problem != nil {
					t.Fatal(r)
				}
				return
			}
			if r.Problem == nil || r.Problem.Code != c.code || r.Problem.Piece != c.piece || r.Problem.Action != c.action {
				t.Fatalf("%+v", r.Problem)
			}
			if (c.state != "connected" || c.mode != "domain" || c.auth != "") && len(calls) > 0 {
				t.Fatal("downstream symptom probed before root", calls)
			}
		})
	}
}

func TestGuardFollowsTransitiveDependenciesAndFreeLinkIsIndependent(t *testing.T) {
	enabled := true
	m := Model{Enabled: func() bool { return enabled }}
	for _, resource := range []Resource{Tunnel, OwnDomain, Authorization, Bridge, Token} {
		if connector.ErrorCode(m.Guard(resource)) != "netbird_cloudflare_in_use" {
			t.Fatal(resource)
		}
	}
	if err := m.Guard(FreeLink); err != nil {
		t.Fatal(err)
	}
	enabled = false
	for _, resource := range []Resource{Tunnel, OwnDomain, Authorization, Bridge, Token, FreeLink} {
		if err := m.Guard(resource); err != nil {
			t.Fatal(resource, err)
		}
	}
}

func TestPreflightDoesNotRequireAnUndeployedBridge(t *testing.T) {
	m := Model{Cloudflare: func() Cloudflare { return Cloudflare{State: "connected", Mode: "domain", Zone: "example.com"} }, Token: func(context.Context, string) error { return nil }, Bridge: func(context.Context, string, string) error { t.Fatal("setup checked an undeployed bridge"); return nil }}
	if zone, e := m.Preflight(context.Background()); e != nil || zone != "example.com" {
		t.Fatal(zone, e)
	}
}

func TestAReconnectingTunnelIsWaitingNotDisconnected(t *testing.T) {
	m := Model{Cloudflare: func() Cloudflare { return Cloudflare{State: "connecting", Mode: "domain", Zone: "example.com"} }}
	r := m.Read(context.Background(), "", false)
	if r.Problem == nil || r.Problem.Code != "cloudflare_connecting" || r.Problem.Action != "check_status" {
		t.Fatalf("misleading reconnect cause: %+v", r.Problem)
	}
}

func TestExplicitRetryInvalidatesAFormerBridgeFailure(t *testing.T) {
	missing := true
	m := Model{Cloudflare: func() Cloudflare { return Cloudflare{State: "connected", Mode: "domain", Zone: "example.com"} }, Bridge: func(context.Context, string, string) error {
		if missing {
			return connector.Coded("netbird_server_bridge_missing", "missing")
		}
		return nil
	}}
	if m.Read(context.Background(), "netbird.example.com", true).Problem == nil {
		t.Fatal("missing bridge accepted")
	}
	missing = false
	m.Invalidate()
	if p := m.Read(context.Background(), "netbird.example.com", true).Problem; p != nil {
		t.Fatal("retry kept stale failure", p)
	}
}

func TestObservationDoesNotWaitForRemoteCheckAndNeverInventsHealthyEvidence(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	m := Model{Cloudflare: func() Cloudflare { return Cloudflare{State: "connected", Mode: "domain", Zone: "example.com"} }, Token: func(context.Context, string) error { close(entered); <-release; return nil }}
	r := m.Observe(context.Background(), "", false)
	if !r.Checking || r.UpstreamsReady {
		t.Fatal("missing protected evidence shown healthy", r)
	}
	<-entered
	result := make(chan Report, 1)
	go func() { result <- m.Observe(context.Background(), "", false) }()
	select {
	case r := <-result:
		if !r.Checking {
			t.Fatal(r)
		}
	case <-time.After(100 * time.Millisecond):
		close(release)
		t.Fatal("status blocked on remote check")
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		r = m.Observe(context.Background(), "", false)
		if !r.Checking && r.UpstreamsReady {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("observation did not settle", r)
}
