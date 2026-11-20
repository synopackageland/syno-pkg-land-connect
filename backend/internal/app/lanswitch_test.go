package app

import (
	"context"
	"errors"
	"testing"

	"dsmapp/internal/lanswitch"
)

// lanSwitchFixture stands in for the LAN switch owner in connectd.
type lanSwitchFixture struct {
	enabled bool
	down    bool
	sets    []bool
}

func (f *lanSwitchFixture) Status(context.Context) (lanswitch.Status, error) {
	if f.down {
		return lanswitch.Status{}, errors.New("down")
	}
	return lanswitch.Status{Enabled: f.enabled, Addresses: []string{}}, nil
}

func (f *lanSwitchFixture) Set(_ context.Context, on bool) (lanswitch.Status, error) {
	if f.down {
		return lanswitch.Status{}, errors.New("down")
	}
	f.sets = append(f.sets, on)
	f.enabled = on
	return f.Status(context.Background())
}

func (f *lanSwitchFixture) Probe(context.Context) (lanswitch.Probe, error) {
	if f.down {
		return lanswitch.Probe{}, errors.New("down")
	}
	if !f.enabled {
		return lanswitch.Probe{Candidates: []lanswitch.Candidate{}}, nil
	}
	return lanswitch.Probe{Enabled: true, Ufrag: "abcdEFGH", Pwd: "p", Candidates: []lanswitch.Candidate{{Address: "192.168.1.20", Port: 45001, URL: "http://192.168.1.20:5000"}}}, nil
}

func TestLANSwitchSetRequiresAnExplicitValue(t *testing.T) {
	cfg := testConfig(t)
	fix := &lanSwitchFixture{}
	cfg.LANSwitch = fix
	for _, bad := range []string{`{}`, `{"enabled":"yes"}`, `{"enabled":true,"x":1}`} {
		if code, _ := call(t, cfg, alice, "lanswitch.set", bad); code != "bad_request" {
			t.Errorf("%s: %q", bad, code)
		}
	}
	if len(fix.sets) != 0 {
		t.Fatal("a bad request changed the option")
	}
	code, out := call(t, cfg, alice, "lanswitch.set", `{"enabled":true}`)
	if data, _ := out["data"].(map[string]any); code != "" || data["enabled"] != true || len(fix.sets) != 1 {
		t.Fatal(code, out)
	}
	code, out = call(t, cfg, alice, "lanswitch.get", `{}`)
	if data, _ := out["data"].(map[string]any); code != "" || data["enabled"] != true {
		t.Fatal(code, out)
	}
}

func TestPhoneLANProbe(t *testing.T) {
	cfg := testConfig(t)
	fix := &lanSwitchFixture{}
	cfg.LANSwitch = fix
	code, out := call(t, cfg, alice, "phone.lanprobe", `{}`)
	if data, _ := out["data"].(map[string]any); code != "" || data["enabled"] != false || data["ufrag"] != nil {
		t.Fatal(code, out)
	}
	fix.enabled = true
	code, out = call(t, cfg, alice, "phone.lanprobe", `{}`)
	data, _ := out["data"].(map[string]any)
	if cands, _ := data["candidates"].([]any); code != "" || data["ufrag"] != "abcdEFGH" || len(cands) != 1 {
		t.Fatal(code, out)
	}
	if code, _ := call(t, cfg, alice, "phone.lanprobe", `{"host":"x"}`); code != "bad_request" {
		t.Fatal(code)
	}
	// An unreachable daemon means no switch, not an error the page must show.
	fix.down = true
	code, out = call(t, cfg, alice, "phone.lanprobe", `{}`)
	if data, _ := out["data"].(map[string]any); code != "" || data["enabled"] != false {
		t.Fatal(code, out)
	}
	if code, _ := call(t, cfg, alice, "lanswitch.get", `{}`); code != "lan_switch_unavailable" {
		t.Fatal(code)
	}
}
