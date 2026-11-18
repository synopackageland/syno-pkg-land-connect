package app

import (
	"context"
	"dsmapp/internal/control"
	"dsmapp/internal/ipctest"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"dsmapp/internal/binaries"
	"dsmapp/internal/connector"
	"dsmapp/internal/ipv6"
)

func TestControlPortsDoNotReturnUserSuppliedNamesOrInternalPlan(t *testing.T) {
	cfg := testConfig(t)
	cfg.PortDetector = &portFixture{http: 15000, https: 15001}
	code, out := call(t, cfg, alice, "ports.upsert", `{"name":"secret-passkey","protocol":"https","port":7443}`)
	if code != "" {
		t.Fatalf("fixture: %s %v", code, out)
	}
	code, out = call(t, cfg, alice, "ctl.ports.list", `{}`)
	if code != "" {
		t.Fatalf("%s %v", code, out)
	}
	raw, _ := json.Marshal(out)
	data := out["data"].(map[string]any)
	if strings.Contains(string(raw), "secret-passkey") || strings.Contains(string(raw), `"name"`) || strings.Contains(string(raw), `"plan"`) {
		t.Fatalf("private value escaped: %s", raw)
	}
	entries := data["entries"].([]any)
	if len(entries) != 3 || entries[2].(map[string]any)["port"] != float64(7443) {
		t.Fatalf("lost port diagnostics: %v", data)
	}
	code, _ = call(t, cfg, alice, "ctl.ports.list", `{"url":"secret-url"}`)
	if code != "bad_request" {
		t.Fatal(code)
	}
}

func TestControlIPv6DoesNotReturnPrivateAddressesOrEndpointURLs(t *testing.T) {
	cfg := testConfig(t)
	cfg.IPv6 = &ipv6.Provider{Source: ipv6Fixture{}, Ports: ipv6Fixture{}}
	code, out := call(t, cfg, alice, "ctl.ipv6.status", `{}`)
	if code != "" {
		t.Fatalf("%s %v", code, out)
	}
	raw, _ := json.Marshal(out)
	for _, secret := range []string{"2606:4700:1234::1", "https://", `"url"`, `"addresses"`, "eth0"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("private endpoint escaped: %s", raw)
		}
	}
	data := out["data"].(map[string]any)
	if data["state"] != "available" || data["reason"] != "external_unverified" || data["addressCount"] != float64(1) {
		t.Fatal(data)
	}
	endpoint := data["endpoints"].([]any)[0].(map[string]any)
	if endpoint["port"].(map[string]any)["number"] != float64(7443) || endpoint["reachability"] != "not_checked" {
		t.Fatal(endpoint)
	}
	assertDataDirEmpty(t, cfg)
	code, _ = call(t, cfg, alice, "ctl.ipv6.status", `{"url":"secret-url"}`)
	if code != "bad_request" {
		t.Fatal(code)
	}
}

type controlController struct {
	captureController
	forgotten string
}

func (c *controlController) Forget(provider string) (connector.Status, error) {
	c.forgotten = provider
	return connector.Status{Provider: provider, State: "disconnected", LoginURL: "secret-forget-url"}, nil
}

func (c *controlController) DisconnectProvider(provider string) (connector.Status, error) {
	c.stopped = true
	return connector.Status{Provider: provider, State: "stopping", LoginURL: "secret-stop-url"}, nil
}

func TestControlConnectionUsesLiveDaemonAndHandsOutOnlyThePendingLoginURL(t *testing.T) {
	cfg := testConfig(t)
	dir, err := os.MkdirTemp("/tmp", "ctl-prov-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	cfg.DataDir = dir
	if err := os.Mkdir(filepath.Join(dir, "connect"), 0700); err != nil {
		t.Fatal(err)
	}
	ln, err := ipctest.Listen(t, control.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	controller := &controlController{}
	server := &http.Server{Handler: connector.Handler(controller, func() {})}
	go server.Serve(ln)
	defer server.Close()
	if err := connector.WritePrivate(filepath.Join(dir, "active-provider"), []byte("multiple")); err != nil {
		t.Fatal(err)
	}
	code, out := call(t, cfg, alice, "ctl.connection.status", `{}`)
	raw, _ := json.Marshal(out)
	if code != "" || out["data"].(map[string]any)["state"] != "needs-login" || out["data"].(map[string]any)["loginUrl"] != "https://login.tailscale.com/a/fixture" {
		t.Fatalf("%s %s", code, raw)
	}
	code, out = call(t, cfg, alice, "ctl.connection.disconnect", `{"provider":"tailscale"}`)
	if code != "" || !controller.stopped || out["data"].(map[string]any)["state"] != "stopping" {
		t.Fatalf("%s %v", code, out)
	}
	code, out = call(t, cfg, alice, "ctl.connection.connect", `{"provider":"tailscale","token":"secret-auth-key"}`)
	raw, _ = json.Marshal(out)
	if code != "" || controller.request.Provider != "tailscale" || controller.request.Token != "secret-auth-key" || len(controller.request.Mappings) != 0 || controller.request.ConfirmExposure || strings.Contains(string(raw), "secret") {
		t.Fatalf("connect forwarding/projection: %s %s", code, raw)
	}
	code, out = call(t, cfg, alice, "ctl.connection.forget", `{"provider":"ipv6"}`)
	raw, _ = json.Marshal(out)
	if code != "" || controller.forgotten != "ipv6" || strings.Contains(string(raw), "secret") {
		t.Fatalf("forget forwarding/projection: %s %s", code, raw)
	}
	server.Close()
	code, out = call(t, cfg, alice, "ctl.connection.status", `{}`)
	if code == "" || out["data"] != nil {
		t.Fatalf("unavailable daemon returned success: %v", out)
	}
}

func TestControlConnectionProjectionOmitsCredentialsRecursively(t *testing.T) {
	s := connector.Status{
		Provider: "multiple", State: "connecting", Phase: "login", NetworkReady: false,
		LoginURL: "https://example.invalid/?token=secret-login", UserCode: "secret-code",
		ShareURL: "https://secret-share.invalid", Hostname: "secret-host",
		LoginServer: "https://secret-server.invalid", ManagementURL: "https://secret-management.invalid",
		Connections: []connector.Status{{Provider: "netbird", State: "connecting", UserCode: "secret-nested", LoginURL: "secret-url"}},
	}
	raw, err := json.Marshal(controlConnection(s))
	if err != nil || strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "loginUrl") || strings.Contains(string(raw), "userCode") {
		t.Fatalf("credential escaped projection: %s %v", raw, err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out["state"] != "connecting" || out["networkReady"] != false || out["connections"].([]any)[0].(map[string]any)["state"] != "connecting" {
		t.Fatalf("accepted was promoted to completed: %s", raw)
	}
}

func TestControlConnectionRoutesReuseStateAndValidation(t *testing.T) {
	cfg := testConfig(t)
	for _, method := range []string{"ctl.connection.status", "ctl.connection.disconnect"} {
		code, out := call(t, cfg, alice, method, `{}`)
		if code != "" || out["data"].(map[string]any)["state"] != "disconnected" {
			t.Fatalf("%s: %s %v", method, code, out)
		}
		if _, found := out["data"].(map[string]any)["shareUrl"]; found {
			t.Fatal("UI URL escaped into control output")
		}
		code, _ = call(t, cfg, alice, method, `{"token":"secret"}`)
		if code != "bad_request" {
			t.Fatalf("%s accepted unknown parameters: %s", method, code)
		}
	}
	code, _ := call(t, cfg, alice, "ctl.connection.disconnect", `{"provider":"unknown"}`)
	if code != "bad_request" {
		t.Fatal(code)
	}
	call(t, cfg, alice, "connection.connect", `{"provider":"fake","token":"demo-connect"}`)
	code, out := call(t, cfg, alice, "ctl.connection.disconnect", `{}`)
	if code != "" || out["data"].(map[string]any)["state"] != "disconnected" {
		t.Fatalf("%s %v", code, out)
	}
	_, out = call(t, cfg, alice, "connection.status", `{}`)
	if out["data"].(map[string]any)["state"] != "disconnected" {
		t.Fatal("control and UI state diverged")
	}
}

func TestControlPortMutationsReuseUIAndKeepPrivateNamesOut(t *testing.T) {
	cfg := testConfig(t)
	cfg.PortDetector = &portFixture{http: 15000, https: 15001}
	code, out := call(t, cfg, alice, "ctl.ports.upsert", `{"name":"secret-service","protocol":"https","port":7443,"enabled":true,"confirmExposure":true}`)
	if code != "" {
		t.Fatalf("%s %v", code, out)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "secret-service") || strings.Contains(string(raw), `"plan"`) {
		t.Fatalf("private port result: %s", raw)
	}
	entries := out["data"].(map[string]any)["entries"].([]any)
	id := entries[2].(map[string]any)["id"].(string)
	code, out = call(t, cfg, alice, "ports.list", `{}`)
	if code != "" || out["data"].(map[string]any)["entries"].([]any)[2].(map[string]any)["name"] != "secret-service" {
		t.Fatalf("UI state diverged: %s %v", code, out)
	}
	code, out = call(t, cfg, alice, "ctl.ports.dsmset", `{"id":"dsm-https","enabled":true,"confirmExposure":true}`)
	if code != "" || out["data"].(map[string]any)["entries"].([]any)[1].(map[string]any)["enabled"] != true {
		t.Fatalf("%s %v", code, out)
	}
	code, out = call(t, cfg, alice, "ctl.ports.delete", `{"id":"`+id+`"}`)
	if code != "" || len(out["data"].(map[string]any)["entries"].([]any)) != 2 {
		t.Fatalf("%s %v", code, out)
	}
	for method, params := range map[string]string{
		"ctl.ports.upsert": `{"name":"secret-service","protocol":"https","port":7443,"enabled":true}`,
		"ctl.ports.dsmset": `{"id":"dsm-http","enabled":true}`,
		"ctl.ports.delete": `{"id":"dsm-http"}`,
	} {
		if code, _ := call(t, cfg, alice, method, params); code == "" {
			t.Fatalf("%s bypassed existing refusal", method)
		}
	}
}

func serveControlFixture(t *testing.T, cfg *Config, controller connector.Controller) {
	t.Helper()
	dir := t.TempDir()
	cfg.DataDir = dir
	if err := os.Mkdir(filepath.Join(dir, "connect"), 0700); err != nil {
		t.Fatal(err)
	}
	ln, err := ipctest.Listen(t, control.Socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: connector.Handler(controller, func() {})}
	go server.Serve(ln)
	t.Cleanup(func() { server.Close() })
}

func TestControlConnectAndForgetReuseProviderValidation(t *testing.T) {
	cfg := testConfig(t)
	for _, params := range []string{`{"provider":"fake","token":"demo-connect"}`, `{"provider":"unknown"}`, `{"provider":"cloudflare","mode":"quick","confirmExposure":false}`, `{"provider":"netbird","managementUrl":"https://private.invalid","token":"secret-key"}`, `{"provider":"ipv6","confirmExposure":false}`, `{"provider":"tailscale","funnel":true,"confirmExposure":false}`} {
		code, out := call(t, cfg, alice, "ctl.connection.connect", params)
		if code == "" {
			t.Fatalf("unexpected connect: %s %v", params, out)
		}
	}
	for _, params := range []string{`{"provider":"unknown"}`, `{"provider":"all"}`, `{"provider":"ipv6","token":"secret"}`} {
		if code, _ := call(t, cfg, alice, "ctl.connection.forget", params); code != "bad_request" {
			t.Fatalf("%s: %s", params, code)
		}
	}
	code, out := call(t, cfg, alice, "ctl.connection.forget", `{"provider":"ipv6"}`)
	if code != "" || out["data"].(map[string]any)["state"] != "disconnected" {
		t.Fatalf("%s %v", code, out)
	}
}

type slowControlController struct {
	captureController
	release chan struct{}
}

func (c *slowControlController) Connect(ctx context.Context, r connector.Request) (connector.Status, error) {
	select {
	case <-c.release:
	case <-ctx.Done():
		return connector.Status{}, ctx.Err()
	}
	return connector.Status{Provider: r.Provider, State: "connected", LoginURL: "secret-login", UserCode: "secret-code"}, nil
}

func TestControlConnectAcceptsSlowWorkAndStatusRemainsQueryable(t *testing.T) {
	cfg := testConfig(t)
	controller := &slowControlController{release: make(chan struct{})}
	async := connector.NewAsync(controller)
	async.Accept = 10 * time.Millisecond
	defer async.Stop()
	defer close(controller.release)
	serveControlFixture(t, &cfg, async)
	started := time.Now()
	code, out := call(t, cfg, alice, "ctl.connection.connect", `{"provider":"tailscale"}`)
	if code != "" || time.Since(started) > time.Second {
		t.Fatalf("ctl waited for background work: %s %v", code, out)
	}
	if out["data"].(map[string]any)["state"] != "connecting" {
		t.Fatalf("acceptance promoted to completion: %v", out)
	}
	code, out = call(t, cfg, alice, "ctl.connection.status", `{}`)
	if code != "" || out["data"].(map[string]any)["state"] != "connecting" {
		t.Fatalf("cannot query ongoing work: %s %v", code, out)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "loginUrl") {
		t.Fatalf("secret leaked: %s", raw)
	}
}

type blockedBinarySource struct{ release chan struct{} }

func (s blockedBinarySource) Latest(ctx context.Context, product string) (binaries.Release, error) {
	select {
	case <-s.release:
		return binaries.Release{Product: product, Version: "1.0.0"}, nil
	case <-ctx.Done():
		return binaries.Release{}, ctx.Err()
	}
}
func (blockedBinarySource) Download(_ context.Context, _ binaries.Release, path string) (string, error) {
	return "sha256", os.WriteFile(path, []byte("fixture"), 0600)
}
func TestControlBinaryCheckAndUpdateDoNotWaitForSlowUpstream(t *testing.T) {
	for _, action := range []string{"check", "update"} {
		t.Run(action, func(t *testing.T) {
			cfg := testConfig(t)
			source := blockedBinarySource{release: make(chan struct{})}
			service := binaries.NewService(binaries.New(filepath.Join(cfg.DataDir, "binaries"), source), nil)
			defer service.Close()
			defer close(source.release)
			cfg.BinaryControl = service
			started := time.Now()
			code, out := call(t, cfg, alice, "binaries."+action, `{"product":"tailscale"}`)
			if code != "" || time.Since(started) > time.Second || out["data"].(map[string]any)["busy"] != true {
				t.Fatalf("waited for upstream instead of accepting: %s %v", code, out)
			}
			code, out = call(t, cfg, alice, "binaries.status", `{"product":"tailscale"}`)
			if code != "" || out["data"].(map[string]any)["busy"] != true {
				t.Fatalf("cannot query pending operation: %s %v", code, out)
			}
		})
	}
}
