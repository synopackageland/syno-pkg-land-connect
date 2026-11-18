package app

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestConnectionPersistsAcrossCGIRequestsWithoutToken(t *testing.T) {
	cfg := testConfig(t)
	code, out := call(t, cfg, alice, "connection.status", `{}`)
	if code != "" || out["data"].(map[string]any)["state"] != "disconnected" {
		t.Fatalf("initial: %s %v", code, out)
	}
	code, out = call(t, cfg, alice, "connection.connect", `{"provider":"fake","token":"demo-connect"}`)
	if code != "" || out["data"].(map[string]any)["state"] != "connected" {
		t.Fatalf("connect: %s %v", code, out)
	}
	cfg2 := Config{PackageID: cfg.PackageID, Version: cfg.Version, DataDir: cfg.DataDir}
	code, out = call(t, cfg2, alice, "connection.status", `{}`)
	data := out["data"].(map[string]any)
	if code != "" || data["shareUrl"] != "https://nas-demo.invalid/share/example" || data["simulation"] != true {
		t.Fatalf("reopen: %s %v", code, out)
	}
	raw, err := os.ReadFile(filepath.Join(cfg.DataDir, "connection.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "demo-connect") || strings.Contains(string(raw), "token") {
		t.Fatalf("secret persisted: %s", raw)
	}
	info, _ := os.Stat(filepath.Join(cfg.DataDir, "connection.json"))
	if info.Mode().Perm() != 0600 {
		t.Fatal("state not private")
	}
	code, out = call(t, cfg2, alice, "connection.disconnect", `{}`)
	if code != "" || out["data"].(map[string]any)["state"] != "disconnected" {
		t.Fatalf("disconnect: %s %v", code, out)
	}
	_, out = call(t, cfg, alice, "connection.status", `{}`)
	if out["data"].(map[string]any)["shareUrl"] != "" {
		t.Fatal("stale share link")
	}
}

func TestConnectionValidationAndProviderFailurePreserveState(t *testing.T) {
	cfg := testConfig(t)
	for _, tc := range []struct{ params, code string }{
		{`{"provider":"cloudflare","token":"anything"}`, "cloudflare_invalid_request"},
		{`{"provider":"tailscale","token":"anything"}`, "provider_unavailable"},
		{`{"provider":"unknown","token":"anything"}`, "provider_unavailable"},
		{`{"provider":"fake","token":"a-real-secret"}`, "invalid_demo_token"},
		{`{"provider":"fake","token":""}`, "invalid_demo_token"},
		{`{"provider":"fake","token":"demo-connect","admin":true}`, "bad_request"},
		{`{"provider":"fake","token":"demo-connect"} {}`, "bad_request"},
		{`{"provider":"fake","token":"` + strings.Repeat("x", 4097) + `"}`, "bad_request"},
	} {
		code, _ := call(t, cfg, alice, "connection.connect", tc.params)
		if code != tc.code {
			t.Fatalf("%s: %s", tc.params, code)
		}
	}
	call(t, cfg, alice, "connection.connect", `{"provider":"fake","token":"demo-connect"}`)
	code, out := call(t, cfg, alice, "connection.connect", `{"provider":"fake","token":"demo-fail"}`)
	if code != "provider_unavailable" || strings.Contains(strings.TrimSpace(strings.ReplaceAll(strings.TrimSpace(out["error"].(map[string]any)["message"].(string)), " ", "")), "demo-fail") {
		t.Fatalf("failure: %s %v", code, out)
	}
	_, out = call(t, cfg, alice, "connection.status", `{}`)
	if out["data"].(map[string]any)["state"] != "connected" {
		t.Fatal("failed replacement changed prior state")
	}
}

func TestCorruptConnectionStateFailsClosed(t *testing.T) {
	cfg := testConfig(t)
	if err := os.WriteFile(filepath.Join(cfg.DataDir, "connection.json"), []byte(`{"provider":"cloudflare","state":"connected","shareUrl":"https://attacker.example"}`), 0600); err != nil {
		t.Fatal(err)
	}
	code, out := call(t, cfg, alice, "connection.status", `{}`)
	if code != "settings_unavailable" || out["data"] != nil {
		t.Fatalf("corruption accepted: %s %v", code, out)
	}
}

func TestConcurrentCGIMutationsLeaveCompleteState(t *testing.T) {
	cfg := testConfig(t)
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				call(t, cfg, alice, "connection.connect", `{"provider":"fake","token":"demo-connect"}`)
			} else {
				call(t, cfg, alice, "connection.disconnect", `{}`)
			}
		}(i)
	}
	wg.Wait()
	code, out := call(t, cfg, alice, "connection.status", `{}`)
	if code != "" {
		t.Fatalf("concurrent: %s %v", code, out)
	}
}

func TestStatusNamesAConnectThatStoppedAfterCrashes(t *testing.T) {
	cfg := testConfig(t)
	if e := os.WriteFile(filepath.Join(cfg.DataDir, "active-provider"), []byte("multiple"), 0600); e != nil {
		t.Fatal(e)
	}
	if code, _ := call(t, cfg, alice, "connection.status", `{}`); code != "provider_unavailable" {
		t.Fatal("an unreachable daemon stays the generic error", code)
	}
	if e := os.MkdirAll(filepath.Join(cfg.DataDir, "connect"), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(cfg.DataDir, "connect", "stopped-after-crashes.json"), []byte(`{"crashes":5}`), 0600); e != nil {
		t.Fatal(e)
	}
	if code, _ := call(t, cfg, alice, "connection.status", `{}`); code != "connect_stopped" {
		t.Fatal("a daemon that gave up must say so", code)
	}
}
