package app

import (
	"context"
	"testing"

	"dsmapp/internal/advisor"
	"dsmapp/internal/dsmsecurity"
)

func TestSecurityStatusAsksDSM(t *testing.T) {
	cfg := testConfig(t)
	asked := false
	cfg.SecurityGet = func(context.Context, string, string) dsmsecurity.Status {
		asked = true
		return dsmsecurity.Status{AutoBlock: dsmsecurity.Off, TwoFactor: dsmsecurity.Unknown}
	}
	code, out := call(t, cfg, alice, "security.status", `{}`)
	data, _ := out["data"].(map[string]any)
	if code != "" || data["auto-block"] != "off" || data["dsm-2fa"] != "unknown" {
		t.Fatalf("%s %v", code, out)
	}
	if !asked {
		t.Fatal("DSM not asked")
	}
}

func TestDiagnoseLinksEachProtectionThatIsOff(t *testing.T) {
	cfg := testConfig(t)
	cfg.SecurityGet = func(context.Context, string, string) dsmsecurity.Status {
		return dsmsecurity.Status{AutoBlock: dsmsecurity.Off, TwoFactor: dsmsecurity.On}
	}
	_, out := call(t, cfg, alice, "ctl.diagnose", `{}`)
	sec := out["data"].(map[string]any)["dsmSecurity"].(map[string]any)
	next := sec["next"].([]any)
	if sec["auto-block"] != "off" || sec["dsm-2fa"] != "on" || len(next) != 1 {
		t.Fatalf("%v", sec)
	}
	step := next[0].(map[string]any)
	if step["kind"] != "user" || step["action"] != "turn-on-auto-block" || step["url"] != advisor.Default().Protections["auto-block"] {
		t.Fatalf("%v", step)
	}
}
