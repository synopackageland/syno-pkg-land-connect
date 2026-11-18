package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"dsmapp/internal/connector"
	"dsmapp/internal/nbserver"
)

type serverControlFixture struct {
	actions []string
	in      []nbserver.Request
	err     error
}

func (f *serverControlFixture) Call(_ context.Context, action string, in nbserver.Request) (nbserver.Result, error) {
	f.actions = append(f.actions, action)
	f.in = append(f.in, in)
	return nbserver.Result{Status: nbserver.Status{State: "off", Zone: "example.com"}, Email: "admin@netbird.example.com", Password: "pw"}, f.err
}

func TestNetBirdServerRoutes(t *testing.T) {
	cfg := testConfig(t)
	f := &serverControlFixture{}
	cfg.NetBirdServer = f
	code, out := call(t, cfg, alice, "netbirdserver.status", `{}`)
	if code != "" || out["data"].(map[string]any)["state"] != "off" {
		t.Fatal(code, out)
	}
	if _, e := os.Stat(filepath.Join(cfg.DataDir, "active-provider")); !os.IsNotExist(e) {
		t.Fatal("status wrote the provider marker")
	}
	if code, _ = call(t, cfg, alice, "netbirdserver.setup", `{}`); code != "" {
		t.Fatal(code)
	}
	if raw, e := connector.ReadPrivate(filepath.Join(cfg.DataDir, "active-provider")); e != nil || string(raw) != "multiple" {
		t.Fatal("setup did not route NetBird through connectd", string(raw), e)
	}
	if code, _ = call(t, cfg, alice, "netbirdserver.disable", `{"removeData":true}`); code != "" || !f.in[len(f.in)-1].RemoveData {
		t.Fatal(code, f.in)
	}
	if code, _ = call(t, cfg, alice, "netbirdserver.setup", `{"removeData":true}`); code != "bad_request" {
		t.Fatal("setup accepted disable params", code)
	}
	if code, _ = call(t, cfg, alice, "netbirdserver.setup", `{"useAnotherName":true}`); code != "" || !f.in[len(f.in)-1].UseAnotherName {
		t.Fatal(code, f.in)
	}
	for _, params := range []string{`{"owner":"other"}`, `{"script":"other"}`, `{"useAnotherName":"yes"}`} {
		if code, _ = call(t, cfg, alice, "netbirdserver.setup", params); code != "bad_request" {
			t.Fatal("setup accepted authority selector", params, code)
		}
	}
	if code, _ = call(t, cfg, alice, "netbirdserver.disable", `{"useAnotherName":true}`); code != "bad_request" {
		t.Fatal(code)
	}
	code, out = call(t, cfg, alice, "netbirdserver.credentials", `{}`)
	if data := out["data"].(map[string]any); code != "" || data["email"] != "admin@netbird.example.com" || data["password"] != "pw" || data["state"] != nil {
		t.Fatal(code, out)
	}
	f.err = connector.Coded("netbird_server_busy", "busy")
	if code, _ = call(t, cfg, alice, "netbirdserver.setup", `{}`); code != "netbird_server_busy" {
		t.Fatal(code)
	}
	f.err = os.ErrNotExist
	if code, _ = call(t, cfg, alice, "netbirdserver.status", `{}`); code != "netbird_server_unavailable" {
		t.Fatal(code)
	}
	want := []string{"status", "setup", "disable", "setup", "credentials", "setup", "status"}
	if len(f.actions) != len(want) {
		t.Fatal(f.actions)
	}
}
