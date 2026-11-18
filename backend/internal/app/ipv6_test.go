package app

import (
	"context"
	"dsmapp/internal/ipv6"
	"dsmapp/internal/provider"
	"net/netip"
	"testing"
)

type ipv6Fixture struct{}

func (ipv6Fixture) Addresses(context.Context) ([]ipv6.Address, error) {
	return []ipv6.Address{{IP: netip.MustParseAddr("2606:4700:1234::1"), Interface: "eth0", PrefixBits: 64}}, nil
}
func (ipv6Fixture) Ports(context.Context) ([]ipv6.Port, error) {
	return []ipv6.Port{{Number: 7443, Scheme: "https"}}, nil
}

func TestIPv6StatusIsReadOnlyAdminOnlyAndHasNoProbeParameters(t *testing.T) {
	cfg := testConfig(t)
	cfg.IPv6 = &ipv6.Provider{Source: ipv6Fixture{}, Ports: ipv6Fixture{}}
	for _, auth := range []stubAuth{nobody, bob} {
		code, _ := call(t, cfg, auth, "ipv6.status", `{}`)
		if code == "" {
			t.Fatal("nonadmin IPv6 diagnostics")
		}
	}
	code, out := call(t, cfg, alice, "ipv6.status", `{}`)
	if code != "" {
		t.Fatal(code)
	}
	s := out["data"].(map[string]any)
	if s["state"] != "available" || s["reason"] != "external_unverified" {
		t.Fatal(s)
	}
	e := s["endpoints"].([]any)[0].(map[string]any)
	if e["url"] != "https://[2606:4700:1234::1]:7443/" || e["reachability"] != "not_checked" {
		t.Fatal(e)
	}
	for _, params := range []string{`{"consent":true}`, `{"url":"https://fixture.invalid"}`, `{"ports":[443]}`} {
		code, _ = call(t, cfg, alice, "ipv6.status", params)
		if code != "bad_request" {
			t.Fatal("browser controls diagnostic dependencies")
		}
	}
	assertDataDirEmpty(t, cfg)
}

func TestIPv6HasNoHardcodedPortFallback(t *testing.T) {
	cfg := testConfig(t)
	cfg.IPv6 = &ipv6.Provider{Source: ipv6Fixture{}}
	code, out := call(t, cfg, alice, "ipv6.status", `{}`)
	if code != "" || out["data"].(map[string]any)["reason"] != "ports_unavailable" {
		t.Fatal(code, out)
	}
}

func TestIPv6DiagnosticRouteAcceptsTheFormalRuntimeAdapter(t *testing.T) {
	cfg := testConfig(t)
	runtime := provider.NewIPv6Runtime(cfg.DataDir, ipv6Fixture{})
	if _, err := runtime.Start(context.Background(), provider.Settings{ConfirmExposure: true, Mappings: []provider.PortMapping{{LocalPort: 7443, PublicPort: 7443, Protocol: "https"}}}); err != nil {
		t.Fatal(err)
	}
	cfg.IPv6 = runtime
	code, out := call(t, cfg, alice, "ipv6.status", `{}`)
	if code != "" || out["data"].(map[string]any)["reason"] != "external_unverified" {
		t.Fatal(code, out)
	}
}
