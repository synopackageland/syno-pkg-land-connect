package app

import (
	"context"
	"encoding/json"
	"testing"

	"dsmapp/internal/ports"
)

type portFixture struct {
	fail        bool
	http, https int
}

func (p *portFixture) Detect(context.Context) (ports.DSMPorts, error) {
	if p.fail {
		return ports.DSMPorts{}, ports.ErrDetection
	}
	return ports.DSMPorts{HTTP: p.http, HTTPS: p.https}, nil
}
func TestPortRoutesEnforceConsentAndCurrentDetection(t *testing.T) {
	cfg := testConfig(t)
	d := &portFixture{http: 15000, https: 15001}
	cfg.PortDetector = d
	code, out := call(t, cfg, alice, "ports.list", `{}`)
	if code != "" {
		t.Fatal(code, out)
	}
	entries := out["data"].(map[string]any)["entries"].([]any)
	if len(entries) != 2 || entries[0].(map[string]any)["port"] != float64(15000) {
		t.Fatal(out)
	}
	for _, params := range []string{`{"id":"dsm-http","enabled":true}`, `{"id":"dsm-http","enabled":true,"confirmExposure":false}`, `{"id":"dsm-http","enabled":true,"confirmExposure":true,"admin":true}`} {
		code, _ = call(t, cfg, alice, "ports.dsmset", params)
		if code != "port_confirmation_required" && code != "bad_request" {
			t.Fatal(code)
		}
	}
	code, out = call(t, cfg, alice, "ports.dsmset", `{"id":"dsm-http","enabled":true,"confirmExposure":true}`)
	if code != "" {
		t.Fatal(code, out)
	}
	code, _ = call(t, cfg, bob, "ports.dsmset", `{"id":"dsm-http","enabled":true,"confirmExposure":true}`)
	if code != "admin_required" {
		t.Fatal(code)
	}
	code, out = call(t, cfg, alice, "ports.upsert", `{"name":"web","protocol":"https","port":443,"enabled":true,"confirmExposure":true}`)
	if code != "" {
		t.Fatal(code, out)
	}
	data := out["data"].(map[string]any)
	entries = data["entries"].([]any)
	id := entries[2].(map[string]any)["id"].(string)
	if data["mappingState"] != "pending-provider" {
		t.Fatal("pretended to expose", out)
	}
	d.http = 443
	code, out = call(t, cfg, alice, "ports.list", `{}`)
	entries = out["data"].(map[string]any)["entries"].([]any)
	if code != "" || entries[2].(map[string]any)["state"] != "conflict" {
		t.Fatal(code, out)
	}
	d.fail = true
	code, out = call(t, cfg, alice, "ports.list", `{}`)
	if code != "" || out["data"].(map[string]any)["detection"] != "needs-attention" {
		t.Fatal(code, out)
	}
	if len(out["data"].(map[string]any)["plan"].(map[string]any)["mappings"].([]any)) != 0 {
		t.Fatal("stale mappings", out)
	}
	raw, _ := json.Marshal(map[string]string{"id": id})
	code, out = call(t, cfg, alice, "ports.delete", string(raw))
	if code != "" {
		t.Fatal(code, out)
	}
	code, _ = call(t, cfg, alice, "ports.delete", `{"id":"dsm-http"}`)
	if code != "bad_request" {
		t.Fatal("auto entry removed", code)
	}
}
func TestPortInputRejectsProtocolRangeDuplicateAndUnknownFields(t *testing.T) {
	cfg := testConfig(t)
	cfg.PortDetector = &portFixture{http: 5000, https: 5001}
	for _, params := range []string{`{"name":"web","protocol":"udp","port":80}`, `{"name":"web","protocol":"http","port":0}`, `{"name":"web","protocol":"http","port":65536}`, `{"name":"web","protocol":"http","port":80,"admin":true}`, `{"name":"web","protocol":"http","port":80.5}`} {
		code, _ := call(t, cfg, alice, "ports.upsert", params)
		if code != "bad_request" {
			t.Fatal(code, params)
		}
	}
	code, _ := call(t, cfg, alice, "ports.upsert", `{"name":"web","protocol":"http","port":5000}`)
	if code != "port_conflict" {
		t.Fatal(code)
	}
	code, _ = call(t, cfg, alice, "ports.upsert", `{"name":"web","protocol":"http","port":80,"enabled":true}`)
	if code != "port_confirmation_required" {
		t.Fatal(code)
	}
	code, out := call(t, cfg, alice, "ports.upsert", `{"name":"web","protocol":"http","port":80}`)
	if code != "" {
		t.Fatal(code, out)
	}
	code, _ = call(t, cfg, alice, "ports.upsert", `{"name":"tcp","protocol":"tcp","port":80}`)
	if code != "port_conflict" {
		t.Fatal(code)
	}
}
