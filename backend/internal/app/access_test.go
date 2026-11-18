package app

import (
	"context"
	"dsmapp/internal/cfaccess"
	"encoding/json"
	"strings"
	"testing"
)

type accessPrivateFixture struct{ stubAppsAPI }

func (*accessPrivateFixture) AccessGet(context.Context) (cfaccess.Status, error) {
	return cfaccess.Status{State: "synced", Revision: 4, Emails: []string{"reader@example.test"}, Endpoints: []cfaccess.Endpoint{{Host: "web.example.test", Protected: true}}}, nil
}
func TestAccessControlProjectionOmitsEmailAndHostname(t *testing.T) {
	cfg := testConfig(t)
	cfg.Apps = &accessPrivateFixture{}
	code, out := call(t, cfg, alice, "ctl.cloudflare.access.status", `{}`)
	if code != "" {
		t.Fatal(code)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "example.test") || strings.Contains(string(raw), "emails\"") || strings.Contains(string(raw), "host\"") {
		t.Fatal("private values escaped control projection")
	}
	data := out["data"].(map[string]any)
	if data["emailCount"] != float64(1) || data["protectedCount"] != float64(1) {
		t.Fatal(data)
	}
}
func TestAccessMutationRequiresConfirmationAndRejectsCallerSuppliedAuthority(t *testing.T) {
	cfg := testConfig(t)
	stub := &stubAppsAPI{}
	cfg.Apps = stub
	for _, params := range []string{`{"emails":[],"revision":0}`, `{"emails":[],"revision":0,"confirm":true,"accountId":"spoofed"}`, `{"emails":[],"revision":0,"confirm":true,"appId":"spoofed"}`} {
		code, _ := call(t, cfg, alice, "cloudflare.access.set", params)
		if code != "bad_request" {
			t.Fatal(code)
		}
	}
	if stub.calls != 0 {
		t.Fatal("invalid request reached daemon")
	}
}
