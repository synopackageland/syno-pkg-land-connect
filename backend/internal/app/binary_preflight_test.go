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
	"time"
)

// missingBinaries resolves no program, as before any download.
type missingBinaries struct{}

func (missingBinaries) Resolve(context.Context, string) (provider.Binary, error) {
	return provider.Binary{}, provider.ErrUnavailable
}
func (missingBinaries) Version(context.Context, string) (string, error) {
	return "", provider.ErrUnavailable
}

func TestMissingBinaryCodeSurvivesDaemonAndCGI(t *testing.T) {
	cfg := testConfig(t)
	dir, e := os.MkdirTemp("/tmp", "cbin-api-")
	if e != nil {
		t.Fatal(e)
	}
	defer os.RemoveAll(dir)
	cfg.DataDir = dir
	controlDir := filepath.Join(dir, "connect")
	if e = os.Mkdir(controlDir, 0700); e != nil {
		t.Fatal(e)
	}
	ts := provider.NewTailscale(filepath.Join(controlDir, "tailscale"), provider.TailscaleExec{Binaries: missingBinaries{}})
	selection := provider.NewSelection(controlDir, ts, provider.NewIPv6Runtime(controlDir, nil))
	selection.SetPortSource(ports.ResolvedSource{Store: ports.Store{Dir: dir}, Detector: &portFixture{fail: true}})
	ln, e := ipctest.Listen(t, control.Socket)
	if e != nil {
		t.Fatal(e)
	}
	server := &http.Server{Handler: connector.Handler(selection, func() {})}
	go server.Serve(ln)
	defer server.Close()
	code, _ := call(t, cfg, alice, "connection.connect", `{"provider":"tailscale"}`)
	if code != "binary_required" {
		t.Fatal(code)
	}
}

// refusingController refuses every mutation with one coded error.
type refusingController struct{ err error }

func (refusingController) Status() connector.Status { return connector.Status{Provider: "multiple"} }
func (c refusingController) Connect(context.Context, connector.Request) (connector.Status, error) {
	return connector.Status{}, c.err
}
func (c refusingController) Disconnect() (connector.Status, error) {
	return connector.Status{}, c.err
}
func (c refusingController) DisconnectProvider(string) (connector.Status, error) {
	return connector.Status{}, c.err
}
func (refusingController) Stop() error { return nil }

func serveRefusals(t *testing.T, err error) Config {
	return serveController(t, refusingController{err})
}
func serveController(t *testing.T, c connector.Controller) Config {
	t.Helper()
	cfg := testConfig(t)
	dir, e := os.MkdirTemp("/tmp", "cbusy-api-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	cfg.DataDir = dir
	if e = os.Mkdir(filepath.Join(dir, "connect"), 0700); e != nil {
		t.Fatal(e)
	}
	if e = connector.WritePrivate(filepath.Join(dir, "active-provider"), []byte("multiple")); e != nil {
		t.Fatal(e)
	}
	ln, e := ipctest.Listen(t, control.Socket)
	if e != nil {
		t.Fatal(e)
	}
	server := &http.Server{Handler: connector.Handler(c, func() {})}
	go server.Serve(ln)
	t.Cleanup(func() { server.Close() })
	return cfg
}
func TestStopFailureCodeSurvivesDaemonAndCGI(t *testing.T) {
	cfg := serveRefusals(t, provider.ErrTailscaleStop)
	if code, _ := call(t, cfg, alice, "connection.disconnect", `{"provider":"tailscale"}`); code != "tailscale_stop_failed" {
		t.Fatal("a failed stop must name itself, not provider_unavailable", code)
	}
}
func TestConnectBusyCodeSurvivesDaemonAndCGI(t *testing.T) {
	cfg := serveRefusals(t, connector.ErrConnectBusy)
	if code, _ := call(t, cfg, alice, "connection.connect", `{"provider":"netbird","managementUrl":"https://api.netbird.io:443","confirmManagementPolicy":true,"token":"SYNTHETIC","confirmExposure":true}`); code != "connect_busy" {
		t.Fatal("connect", code)
	}
	if code, _ := call(t, cfg, alice, "connection.disconnect", `{"provider":"cloudflare"}`); code != "connect_busy" {
		t.Fatal("disconnect", code)
	}
}

type slowStatusRefusals struct{ refusingController }

func (slowStatusRefusals) Status() connector.Status {
	time.Sleep(2 * time.Second)
	return connector.Status{Provider: "multiple"}
}
func TestConnectDoesNotReadTheFullStatusBeforeStarting(t *testing.T) {
	cfg := serveController(t, slowStatusRefusals{refusingController{connector.ErrConnectBusy}})
	started := time.Now()
	if code, _ := call(t, cfg, alice, "connection.connect", `{"provider":"tailscale"}`); code != "connect_busy" {
		t.Fatal(code)
	}
	if took := time.Since(started); took > time.Second {
		t.Fatal("a reachability check must not wait for every provider's status", took)
	}
}
