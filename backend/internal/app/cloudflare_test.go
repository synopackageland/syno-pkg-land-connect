package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The own domain is browser authorization only: a tunnel token request is
// refused before the daemon or any state is touched, without echoing it.
func TestCloudflareTokenRequestIsRefusedBeforeDaemonOrStateMutation(t *testing.T) {
	cfg := testConfig(t)
	code, out := call(t, cfg, alice, "connection.connect", `{"provider":"cloudflare","mode":"token","hostname":"nas.example.com","token":"SYNTHETIC-INVALID","confirmExposure":true}`)
	if code != "cloudflare_invalid_request" {
		t.Fatal(code)
	}
	raw, _ := json.Marshal(out)
	if strings.Contains(string(raw), "SYNTHETIC-INVALID") {
		t.Fatal("token disclosed")
	}
	if _, e := os.Stat(filepath.Join(cfg.DataDir, "active-provider")); !os.IsNotExist(e) {
		t.Fatal("token request changed provider", e)
	}
}
