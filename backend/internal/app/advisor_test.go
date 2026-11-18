package app

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"dsmapp/internal/connector"
	"dsmapp/internal/ipv6"
)

type statusController struct {
	captureController
	status connector.Status
	err    error
}

func (c *statusController) Status() connector.Status { return c.status }
func (c *statusController) Connect(_ context.Context, r connector.Request) (connector.Status, error) {
	c.request = r
	if c.err != nil {
		return connector.Status{}, c.err
	}
	return c.status, nil
}

func multiple(children ...connector.Status) connector.Status {
	return connector.Status{Provider: "multiple", State: "connected", Connections: children}
}

func liveConfig(t *testing.T, s connector.Status) (Config, *statusController) {
	t.Helper()
	cfg := testConfig(t)
	controller := &statusController{status: s}
	serveControlFixture(t, &cfg, controller)
	if err := connector.WritePrivate(cfg.DataDir+"/active-provider", []byte("multiple")); err != nil {
		t.Fatal(err)
	}
	return cfg, controller
}

func TestControlStatusExplainsFailuresAndHandsOutPendingSignIn(t *testing.T) {
	cfg, _ := liveConfig(t, multiple(
		connector.Status{Provider: "cloudflare", State: "failed", Code: "cloudflare_name_taken", Mode: "domain", ShareURL: "https://secret-share.invalid", Hostname: "secret-host"},
		connector.Status{Provider: "tailscale", State: "needs-login", LoginURL: "https://login.tailscale.com/a/abc123", LoginDeadline: "2026-10-23T10:10:00Z", LoginServer: "https://secret-server.invalid"},
		connector.Status{Provider: "netbird", State: "connected", LoginURL: "https://login.netbird.io/activate?user_code=OLDC-ODES", UserCode: "OLDC-ODES", LoggedIn: true},
	))
	code, out := call(t, cfg, alice, "ctl.connection.status", `{}`)
	if code != "" {
		t.Fatalf("%s %v", code, out)
	}
	raw, _ := json.Marshal(out)
	for _, secret := range []string{"secret-share", "secret-host", "secret-server", "OLDC-ODES"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("%s escaped: %s", secret, raw)
		}
	}
	children := out["data"].(map[string]any)["connections"].([]any)
	cf, ts, nb := children[0].(map[string]any), children[1].(map[string]any), children[2].(map[string]any)
	if cf["code"] != "cloudflare_name_taken" || cf["mode"] != "domain" {
		t.Fatalf("failure reason dropped: %v", cf)
	}
	if ts["loginUrl"] != "https://login.tailscale.com/a/abc123" || ts["loginDeadline"] != "2026-10-23T10:10:00Z" {
		t.Fatalf("pending sign-in not handed out: %v", ts)
	}
	if _, found := nb["loginUrl"]; found || nb["loggedIn"] != true {
		t.Fatalf("a finished sign-in keeps no link: %v", nb)
	}
}

func TestControlStatusRefusesAnUntrustedLoginAddress(t *testing.T) {
	for _, url := range []string{"http://login.tailscale.com/a/abc", "javascript:alert(1)", "https://user:pw@login.tailscale.com/a/abc"} {
		v := controlConnection(connector.Status{Provider: "tailscale", State: "needs-login", LoginURL: url})
		if v.LoginURL != "" {
			t.Fatalf("%s accepted", url)
		}
	}
}

func TestControlConnectWithoutConnectorSaysWhatToRunNext(t *testing.T) {
	cfg, controller := liveConfig(t, multiple())
	controller.err = connector.ErrBinaryRequired
	code, out := call(t, cfg, alice, "ctl.connection.connect", `{"provider":"netbird"}`)
	if code != "" {
		t.Fatalf("%s %v", code, out)
	}
	data := out["data"].(map[string]any)
	next := data["next"].([]any)
	first, second := next[0].(map[string]any), next[1].(map[string]any)
	if data["state"] != "connector-required" || data["code"] != "binary_required" || strings.Join(toStrings(first["command"]), " ") != "binaries update" || first["input"].(map[string]any)["product"] != "netbird" || strings.Join(toStrings(second["command"]), " ") != "connection connect" {
		t.Fatalf("%v", data)
	}
	// The UI keeps the plain refusal and downloads in place.
	if code, _ := call(t, cfg, alice, "connection.connect", `{"provider":"netbird"}`); code != "binary_required" {
		t.Fatal(code)
	}
}

func toStrings(v any) []string {
	out := []string{}
	for _, x := range v.([]any) {
		out = append(out, x.(string))
	}
	return out
}

func TestRecommendUsesDetectedFactsAndCatalog(t *testing.T) {
	cfg, _ := liveConfig(t, multiple(connector.Status{Provider: "tailscale", State: "connected"}))
	cfg.PortDetector = &portFixture{http: 15000, https: 15001}
	cfg.IPv6 = &ipv6.Provider{Source: noIPv6{}, Ports: ipv6Fixture{}}
	code, out := call(t, cfg, alice, "advisor.recommend", `{"goal":"apps"}`)
	if code != "" {
		t.Fatalf("%s %v", code, out)
	}
	data := out["data"].(map[string]any)
	options := data["options"].([]any)
	last := options[len(options)-1].(map[string]any)
	if data["catalogVersion"] == "" || last["id"] != "ipv6" || last["suitability"] != "unavailable" {
		t.Fatalf("%v", data)
	}
	code, out = call(t, cfg, alice, "advisor.recommend", `{"goal":"all"}`)
	first := out["data"].(map[string]any)["options"].([]any)[0].(map[string]any)
	if code != "" || first["id"] != "tailscale" || first["state"] != "connected" {
		t.Fatalf("%s %v", code, first)
	}
	for _, params := range []string{`{"goal":"anything"}`, `{"goal":"apps","extra":1}`} {
		if code, _ := call(t, cfg, alice, "advisor.recommend", params); code != "bad_request" {
			t.Fatalf("%s: %s", params, code)
		}
	}
	if code, _ := call(t, cfg, alice, "advisor.recommend", `{}`); code != "" {
		t.Fatal("a goal is optional")
	}
}

func TestDiagnoseSaysWhatIsMissingAndWhatToDoNext(t *testing.T) {
	cfg, _ := liveConfig(t, multiple(
		connector.Status{Provider: "cloudflare", State: "failed", Code: "binary_required"},
		connector.Status{Provider: "tailscale", State: "needs-login", LoginURL: "https://login.tailscale.com/a/abc123"},
		connector.Status{Provider: "netbird", State: "connected", NetBirdIP: "100.64.0.9", Endpoints: []connector.Endpoint{{URL: "http://100.64.0.9:5000/"}}},
		connector.Status{Provider: "ipv6", State: "connecting"},
	))
	cfg.IPv6 = &ipv6.Provider{Source: ipv6Fixture{}, Ports: ipv6Fixture{}}
	code, out := call(t, cfg, alice, "ctl.diagnose", `{}`)
	if code != "" {
		t.Fatalf("%s %v", code, out)
	}
	data := out["data"].(map[string]any)
	if data["ipv6Global"] != true {
		t.Fatal(data)
	}
	byProvider := map[string]map[string]any{}
	for _, p := range data["providers"].([]any) {
		byProvider[p.(map[string]any)["provider"].(string)] = p.(map[string]any)
	}
	nextOf := func(p string) map[string]any {
		t.Helper()
		next, ok := byProvider[p]["next"].([]any)
		if !ok || len(next) == 0 {
			t.Fatalf("%s has no next step: %v", p, byProvider[p])
		}
		return next[0].(map[string]any)
	}
	if nextOf("cloudflare")["input"].(map[string]any)["product"] != "cloudflared" || byProvider["cloudflare"]["connector"] != "missing" {
		t.Fatalf("cloudflare: %v", byProvider["cloudflare"])
	}
	if nextOf("tailscale")["action"] != "open-login-url" || nextOf("tailscale")["url"] != "https://login.tailscale.com/a/abc123" {
		t.Fatalf("tailscale: %v", byProvider["tailscale"])
	}
	if strings.Join(toStrings(nextOf("netbird")["command"]), " ") != "connection check" || !strings.Contains(strings.Join(toStrings(byProvider["netbird"]["addresses"]), " "), "100.64.0.9") {
		t.Fatalf("netbird: %v", byProvider["netbird"])
	}
	if strings.Join(toStrings(nextOf("ipv6")["command"]), " ") != "diagnose" {
		t.Fatalf("ipv6: %v", byProvider["ipv6"])
	}
	if code, _ := call(t, cfg, alice, "ctl.diagnose", `{"provider":"x"}`); code != "bad_request" {
		t.Fatal(code)
	}
}

func TestDiagnoseBeforeAnyConnectionListsEveryMethodAsOff(t *testing.T) {
	cfg := testConfig(t)
	code, out := call(t, cfg, alice, "ctl.diagnose", `{}`)
	if code != "" {
		t.Fatalf("%s %v", code, out)
	}
	providers := out["data"].(map[string]any)["providers"].([]any)
	if len(providers) != 4 {
		t.Fatal(providers)
	}
	for _, p := range providers {
		m := p.(map[string]any)
		if m["state"] != "disconnected" || len(m["next"].([]any)) != 0 {
			t.Fatal(m)
		}
	}
}

func TestAdvisorRoutesAreAdministratorOnly(t *testing.T) {
	cfg := testConfig(t)
	for _, method := range []string{"advisor.recommend", "ctl.diagnose"} {
		if code, _ := call(t, cfg, bob, method, `{}`); code != "admin_required" {
			t.Fatalf("%s: %s", method, code)
		}
	}
}

type noIPv6 struct{}

func (noIPv6) Addresses(context.Context) ([]ipv6.Address, error) { return nil, nil }
