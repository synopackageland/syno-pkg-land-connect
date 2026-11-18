package app

import "testing"

func TestBinaryRoutes(t *testing.T) {
	for _, method := range []string{"binaries.status", "binaries.check", "binaries.update", "binaries.rollback"} {
		for _, auth := range []stubAuth{bob, nobody} {
			code, _ := call(t, testConfig(t), auth, method, `{"product":"cloudflared"}`)
			want := "admin_required"
			if auth.err != nil {
				want = "session_required"
			}
			if code != want {
				t.Fatalf("%s: %s", method, code)
			}
		}
		code, _ := call(t, testConfig(t), alice, method, `{"product":"../evil"}`)
		if code != "bad_request" {
			t.Fatal(code)
		}
	}
	code, out := call(t, testConfig(t), alice, "binaries.status", `{"product":"tailscale"}`)
	if code != "" || out["data"].(map[string]any)["current"] != "" {
		t.Fatal(out)
	}
}

func TestRollbackWithoutPreviousIsRefusedAlikeForEveryProduct(t *testing.T) {
	cfg := testConfig(t)
	for _, product := range []string{"cloudflared", "tailscale", "netbird"} {
		code, _ := call(t, cfg, alice, "binaries.rollback", `{"product":"`+product+`"}`)
		if code != "binary_no_previous" {
			t.Fatal(product, code)
		}
		code, out := call(t, cfg, alice, "binaries.status", `{"product":"`+product+`"}`)
		status := out["data"].(map[string]any)
		if code != "" || status["phase"] == "failed" || status["error"] != "" || status["busy"] != false {
			t.Fatal("a refused rollback must not leave a failed job", product, status)
		}
	}
}
