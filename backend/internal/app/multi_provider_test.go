package app

import (
	"context"
	"dsmapp/internal/connector"
	"dsmapp/internal/control"
	"dsmapp/internal/ipctest"
	"dsmapp/internal/ports"
	"dsmapp/internal/provider"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

type captureController struct {
	request connector.Request
	stopped bool
}

func (c *captureController) Status() connector.Status {
	return connector.Status{Provider: "tailscale", State: "needs-login", LoginURL: "https://login.tailscale.com/a/fixture"}
}
func (c *captureController) Connect(_ context.Context, r connector.Request) (connector.Status, error) {
	c.request = r
	return c.Status(), nil
}
func (c *captureController) Disconnect() (connector.Status, error) {
	c.stopped = true
	return connector.Status{Provider: "tailscale", State: "disconnected"}, nil
}
func (c *captureController) Stop() error { return nil }

func TestTailscaleAdminRouteRefusesRetiredExposureAndManualMappings(t *testing.T) {
	cfg := testConfig(t)
	controller := &captureController{}
	serveControlFixture(t, &cfg, controller)
	for _, params := range []string{
		`{"provider":"tailscale","confirmExposure":true}`,
		`{"provider":"tailscale","mappings":[{"localPort":5000,"publicPort":443,"protocol":"http"}]}`,
	} {
		if code, _ := call(t, cfg, alice, "connection.connect", params); code != "provider_unavailable" {
			t.Fatalf("retired settings admitted: %s: %s", params, code)
		}
		if controller.request.Provider != "" {
			t.Fatal("invalid settings reached the daemon", controller.request)
		}
	}
}

func TestTailscaleAdminRoutePersistsSelectionWithoutPublicExposure(t *testing.T) {
	cfg := testConfig(t)
	dir, e := os.MkdirTemp("/tmp", "prov-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(dir)
	cfg.DataDir = dir
	os.Mkdir(filepath.Join(dir, "connect"), 0700)
	ln, e := ipctest.Listen(t, control.Socket)
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	controller := &captureController{}
	server := &http.Server{Handler: connector.Handler(controller, func() {})}
	go server.Serve(ln)
	defer server.Close()
	params := `{"provider":"tailscale","token":""}`
	code, out := call(t, cfg, alice, "connection.connect", params)
	if code != "" {
		t.Fatal(code, out)
	}
	if controller.request.Provider != "tailscale" || controller.request.ConfirmExposure || len(controller.request.Mappings) != 0 {
		t.Fatal(controller.request)
	}
	raw, e := connector.ReadPrivate(filepath.Join(dir, "active-provider"))
	if e != nil || string(raw) != "multiple" {
		t.Fatal(e, string(raw))
	}
	code, out = call(t, cfg, alice, "connection.status", `{}`)
	if code != "" || out["data"].(map[string]any)["loginUrl"] != "https://login.tailscale.com/a/fixture" {
		t.Fatal(code, out)
	}
	code, out = call(t, cfg, alice, "connection.connect", `{"provider":"fake","token":"demo-connect"}`)
	if code != "" || !controller.stopped {
		t.Fatal(code, out)
	}
}

func TestIPv6ActivationWaitsForTrustedResolvedPorts(t *testing.T) {
	cfg := testConfig(t)
	dir, e := os.MkdirTemp("/tmp", "ipv6-prov-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(dir)
	cfg.DataDir = dir
	os.Mkdir(filepath.Join(dir, "connect"), 0700)
	ln, e := ipctest.Listen(t, control.Socket)
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	controller := &captureController{}
	server := &http.Server{Handler: connector.Handler(controller, func() {})}
	go server.Serve(ln)
	defer server.Close()
	code, _ := call(t, cfg, alice, "connection.connect", `{"provider":"ipv6","confirmExposure":true,"mappings":[{"localPort":7443,"publicPort":7443,"protocol":"https"}]}`)
	if code != "provider_unavailable" || cfg.activeProvider() != "fake" {
		t.Fatal("unresolved IPv6 input changed selection", code)
	}
}

func TestIPv6ActivatesOnlyFromTrustedResolvedPortsAndMaintainsSpoofRefusal(t *testing.T) {
	cfg := testConfig(t)
	dir, e := os.MkdirTemp("/tmp", "resolved-v6-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(dir)
	cfg.DataDir = dir
	d := &portFixture{http: 15000, https: 15001}
	cfg.PortDetector = d
	if code, out := call(t, cfg, alice, "ports.dsmset", `{"id":"dsm-https","enabled":true,"confirmExposure":true}`); code != "" {
		t.Fatal(code, out)
	}
	os.Mkdir(filepath.Join(dir, "connect"), 0700)
	ln, e := ipctest.Listen(t, control.Socket)
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	runtime := provider.NewIPv6Runtime(dir, ipv6Fixture{})
	manager := provider.NewSelection(dir, runtime)
	manager.SetPortSource(ports.ResolvedSource{Store: ports.Store{Dir: dir}, Detector: d})
	server := &http.Server{Handler: connector.Handler(manager, func() {})}
	go server.Serve(ln)
	defer server.Close()
	code, out := call(t, cfg, alice, "connection.connect", `{"provider":"ipv6","confirmExposure":true}`)
	if code != "" || cfg.activeProvider() != "multiple" || len(runtime.Endpoints()) != 1 || runtime.Endpoints()[0].LocalPort != 15001 {
		t.Fatal("trusted activation blocked", code, out, runtime.Endpoints())
	}
	for _, params := range []string{`{"provider":"ipv6","confirmExposure":false}`, `{"provider":"ipv6","confirmExposure":true,"token":"spoof"}`, `{"provider":"ipv6","confirmExposure":true,"hostname":"spoof.invalid"}`, `{"provider":"ipv6","confirmExposure":true,"funnel":true}`, `{"provider":"ipv6","confirmExposure":true,"mappings":[{"localPort":7443,"publicPort":7443,"protocol":"https"}]}`, `{"provider":"ipv6","confirmExposure":true,"resolvedPorts":[7443]}`} {
		if code, _ := call(t, cfg, alice, "connection.connect", params); code == "" || runtime.Endpoints()[0].LocalPort != 15001 {
			t.Fatal("spoof/consent changed trusted selection", code, params)
		}
	}
	d.fail = true
	code, _ = call(t, cfg, alice, "connection.connect", `{"provider":"ipv6","confirmExposure":true}`)
	if code == "" {
		t.Fatal("detection failure activated")
	}
}

// stoppingController accepts a disconnect that is still stopping, as connectd
// does when a loaded NAS takes longer than Async.Accept to stop a daemon.
type stoppingController struct{ captureController }

func (c *stoppingController) Disconnect() (connector.Status, error) {
	c.stopped = true
	return connector.Status{Provider: "multiple", State: "disconnecting", Connections: []connector.Status{{Provider: "tailscale", State: "disconnecting"}}}, nil
}
func TestDemoConnectWaitsForAnAcceptedDisconnectToFinish(t *testing.T) {
	cfg := testConfig(t)
	dir, e := os.MkdirTemp("/tmp", "prov-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(dir)
	cfg.DataDir = dir
	os.Mkdir(filepath.Join(dir, "connect"), 0700)
	ln, e := ipctest.Listen(t, control.Socket)
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	server := &http.Server{Handler: connector.Handler(&stoppingController{}, func() {})}
	go server.Serve(ln)
	defer server.Close()
	if e = connector.WritePrivate(filepath.Join(dir, "active-provider"), []byte("multiple")); e != nil {
		t.Fatal(e)
	}
	code, out := call(t, cfg, alice, "connection.connect", `{"provider":"fake","token":"demo-connect"}`)
	if code != "disconnect_busy" {
		t.Fatal("the demo must not take over while a real connection is still stopping", code, out)
	}
	if raw, e := connector.ReadPrivate(filepath.Join(dir, "active-provider")); e != nil || string(raw) != "multiple" {
		t.Fatal("routing must stay with the daemon until it has stopped", e, string(raw))
	}
}

type checkController struct {
	captureController
	asked string
}

func (c *checkController) Check(_ context.Context, provider string) (connector.CheckResult, error) {
	c.asked = provider
	return connector.CheckResult{Provider: provider, State: "ok", CheckedAt: "2026-10-12T08:00:00Z", Attempts: 1}, nil
}

func TestConnectionCheckForwardsOneProviderToTheDaemon(t *testing.T) {
	cfg := testConfig(t)
	dir, e := os.MkdirTemp("/tmp", "check-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(dir)
	cfg.DataDir = dir
	os.Mkdir(filepath.Join(dir, "connect"), 0700)
	ln, e := ipctest.Listen(t, control.Socket)
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	if e := connector.WritePrivate(filepath.Join(dir, "active-provider"), []byte("multiple")); e != nil {
		t.Fatal(e)
	}
	controller := &checkController{}
	server := &http.Server{Handler: connector.Handler(controller, func() {})}
	go server.Serve(ln)
	defer server.Close()
	code, out := call(t, cfg, alice, "connection.check", `{"provider":"netbird"}`)
	data, _ := out["data"].(map[string]any)
	if code != "" || controller.asked != "netbird" || data["state"] != "ok" || data["checkedAt"] != "2026-10-12T08:00:00Z" {
		t.Fatal(code, out)
	}
	for _, params := range []string{`{}`, `{"provider":"fake"}`, `{"provider":"netbird","extra":1}`} {
		if code, _ := call(t, cfg, alice, "connection.check", params); code != "bad_request" {
			t.Errorf("%s: %q, want bad_request", params, code)
		}
	}
}

func TestConnectionCheckBeforeAnyConnectionIsNotChecked(t *testing.T) {
	code, out := call(t, testConfig(t), alice, "connection.check", `{"provider":"tailscale"}`)
	if data, _ := out["data"].(map[string]any); code != "" || data["state"] != "not-checked" || data["provider"] != "tailscale" {
		t.Fatal(code, out)
	}
}

func TestConnectionCheckWithoutTheDaemonIsUnavailable(t *testing.T) {
	cfg := testConfig(t)
	if e := connector.WritePrivate(filepath.Join(cfg.DataDir, "active-provider"), []byte("multiple")); e != nil {
		t.Fatal(e)
	}
	if code, _ := call(t, cfg, alice, "connection.check", `{"provider":"tailscale"}`); code != "provider_unavailable" {
		t.Fatalf("got %q", code)
	}
}

type forgetController struct {
	captureController
	forgot string
}

func (c *forgetController) Forget(provider string) (connector.Status, error) {
	c.forgot = provider
	return connector.Status{Provider: "multiple", State: "disconnected", Connections: []connector.Status{{Provider: provider, State: "disconnected"}}}, nil
}

func TestConnectionForgetForwardsOneProviderToTheDaemon(t *testing.T) {
	cfg := testConfig(t)
	dir, e := os.MkdirTemp("/tmp", "forget-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(dir)
	cfg.DataDir = dir
	os.Mkdir(filepath.Join(dir, "connect"), 0700)
	ln, e := ipctest.Listen(t, control.Socket)
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	if e := connector.WritePrivate(filepath.Join(dir, "active-provider"), []byte("multiple")); e != nil {
		t.Fatal(e)
	}
	controller := &forgetController{}
	server := &http.Server{Handler: connector.Handler(controller, func() {})}
	go server.Serve(ln)
	defer server.Close()
	code, out := call(t, cfg, alice, "connection.forget", `{"provider":"ipv6"}`)
	data, _ := out["data"].(map[string]any)
	if code != "" || controller.forgot != "ipv6" || data["provider"] != "multiple" {
		t.Fatal(code, out)
	}
	for _, params := range []string{`{}`, `{"provider":"fake"}`, `{"provider":"ipv6","extra":1}`} {
		if code, _ := call(t, cfg, alice, "connection.forget", params); code != "bad_request" {
			t.Errorf("%s: %q, want bad_request", params, code)
		}
	}
}

func TestConnectionForgetBeforeAnyConnectionHasNothingToForget(t *testing.T) {
	code, out := call(t, testConfig(t), alice, "connection.forget", `{"provider":"ipv6"}`)
	if data, _ := out["data"].(map[string]any); code != "" || data["state"] != "disconnected" {
		t.Fatal(code, out)
	}
}
