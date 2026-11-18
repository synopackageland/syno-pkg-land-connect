package app

import (
	"context"
	"errors"

	"dsmapp/internal/connector"
	"dsmapp/internal/dsmexternal"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExternalReadForwardsOnlyAuthorizedRequestSession(t *testing.T) {
	for _, tc := range []struct {
		name      string
		auth      stubAuth
		wantCalls int
	}{
		{"admin", alice, 1},
		{"nonadmin", bob, 0},
		{"anonymous", nobody, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig(t)
			calls := 0
			cfg.ExternalGet = func(_ context.Context, cookie, token string) (dsmexternal.External, error) {
				calls++
				if cookie != "id=fixture" || token != "fixture-token" {
					t.Errorf("request session was not forwarded")
				}
				return dsmexternal.External{}, nil
			}
			srv, err := newServer(tc.auth, cfg)
			if err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, "/api.cgi", strings.NewReader(`{"method":"external.get","params":{}}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			req.Header.Set("Cookie", "id=fixture")
			req.Header.Set("X-SYNO-TOKEN", "fixture-token")
			srv.ServeHTTP(httptest.NewRecorder(), req)
			if calls != tc.wantCalls {
				t.Fatalf("external reads = %d, want %d", calls, tc.wantCalls)
			}
		})
	}
}

// dsmFake is DSM's external access setting as the caller's session sees it.
type dsmFake struct {
	ext     dsmexternal.External
	writes  []dsmexternal.Update
	refuse  error
	ignore  bool
	readErr error
}

func (f *dsmFake) install(cfg *Config) {
	cfg.ExternalGet = func(context.Context, string, string) (dsmexternal.External, error) { return f.ext, f.readErr }
	cfg.ExternalSet = func(_ context.Context, _, _ string, u dsmexternal.Update) error {
		f.writes = append(f.writes, u)
		if f.refuse != nil || f.ignore {
			return f.refuse
		}
		if u.Hostname != nil {
			f.ext.Hostname = *u.Hostname
		}
		if u.HTTPPort != nil {
			f.ext.HTTPPort = u.HTTPPort
		}
		if u.HTTPSPort != nil {
			f.ext.HTTPSPort = u.HTTPSPort
		}
		return nil
	}
}

var ownDomain = connector.Status{Provider: "cloudflare", State: "connected", Mode: "domain", Hostname: "nas.example.com", Endpoints: []connector.Endpoint{{URL: "https://nas.example.com", Public: true}}}

func TestExternalStatusSuggestsTheOwnDomainOnHTTPS443(t *testing.T) {
	cfg, _ := liveConfig(t, multiple(ownDomain))
	dsm := &dsmFake{}
	dsm.install(&cfg)
	code, out := call(t, cfg, alice, "external.status", `{}`)
	data, _ := out["data"].(map[string]any)
	suggested, _ := data["suggested"].(map[string]any)
	if code != "" || data["state"] != "not-set" || suggested["hostname"] != "nas.example.com" || suggested["httpsPort"] != 443.0 {
		t.Fatalf("%s %v", code, out)
	}
	if len(dsm.writes) != 0 {
		t.Fatal("status wrote to DSM")
	}
}

func TestExternalSetIsIdempotentAndReadsBack(t *testing.T) {
	cfg, _ := liveConfig(t, multiple(ownDomain))
	http := 80
	dsm := &dsmFake{ext: dsmexternal.External{Hostname: "old.example.net", HTTPPort: &http}}
	dsm.install(&cfg)
	code, out := call(t, cfg, alice, "external.set", `{"hostname":"nas.example.com","httpsPort":443}`)
	data, _ := out["data"].(map[string]any)
	if code != "" || data["changed"] != true || data["previous"].(map[string]any)["hostname"] != "old.example.net" || data["advice"].(map[string]any)["state"] != "matches" {
		t.Fatalf("%s %v", code, out)
	}
	if len(dsm.writes) != 1 || dsm.writes[0].HTTPPort != nil || *dsm.writes[0].Hostname != "nas.example.com" || *dsm.writes[0].HTTPSPort != 443 {
		t.Fatalf("writes %+v", dsm.writes)
	}
	if dsm.ext.HTTPPort == nil || *dsm.ext.HTTPPort != 80 {
		t.Fatal("unchanged HTTP port lost")
	}
	code, out = call(t, cfg, alice, "external.set", `{"hostname":"NAS.example.com","httpsPort":443}`)
	if code != "" || out["data"].(map[string]any)["changed"] != false || len(dsm.writes) != 1 {
		t.Fatalf("repeat wrote again: %s %v %d", code, out, len(dsm.writes))
	}
}

func TestExternalSetRefusesInvalidValuesBeforeDSM(t *testing.T) {
	cfg := testConfig(t)
	dsm := &dsmFake{}
	dsm.install(&cfg)
	for _, params := range []string{`{}`, `{"hostname":"https://nas.example.com"}`, `{"hostname":"nas.example.com:443"}`, `{"hostname":"nas.example.com","httpsPort":0}`,
		`{"hostname":"nas.example.com","httpsPort":65536}`, `{"hostname":"nas.example.com","httpPort":443,"httpsPort":443}`, `{"hostname":"nas.example.com","enable_https":true}`} {
		if code, _ := call(t, cfg, alice, "external.set", params); code != "bad_request" {
			t.Errorf("%s: %q", params, code)
		}
	}
	if len(dsm.writes) != 0 {
		t.Fatal("invalid values reached DSM", dsm.writes)
	}
}

func TestExternalSetExplainsDSMRefusalsAndUnconfirmedWrites(t *testing.T) {
	for _, tc := range []struct {
		dsm  dsmFake
		code string
	}{
		{dsmFake{refuse: &dsmexternal.Refused{Code: 3510}}, "external_invalid"},
		{dsmFake{refuse: &dsmexternal.Refused{Code: 3511}}, "external_port_conflict"},
		{dsmFake{refuse: &dsmexternal.Refused{Code: 105}}, "external_denied"},
		{dsmFake{ignore: true}, "external_unconfirmed"},
		{dsmFake{readErr: errors.New("down")}, "external_unavailable"},
	} {
		cfg := testConfig(t)
		dsm := tc.dsm
		dsm.install(&cfg)
		if code, _ := call(t, cfg, alice, "external.set", `{"hostname":"nas.example.com","httpsPort":443}`); code != tc.code {
			t.Errorf("want %s, got %q", tc.code, code)
		}
	}
}

func TestExternalRoutesAreAdministratorOnly(t *testing.T) {
	cfg := testConfig(t)
	dsm := &dsmFake{}
	dsm.install(&cfg)
	for _, method := range []string{"external.status", "external.set"} {
		if code, _ := call(t, cfg, bob, method, `{"hostname":"nas.example.com"}`); code != "admin_required" {
			t.Errorf("%s: %s", method, code)
		}
	}
	if len(dsm.writes) != 0 {
		t.Fatal(dsm.writes)
	}
}

func TestDiagnoseOffersTheDSMExternalAddressOnlyWhenItDiffers(t *testing.T) {
	cfg, _ := liveConfig(t, multiple(ownDomain))
	dsm := &dsmFake{ext: dsmexternal.External{Hostname: "old.example.net"}}
	dsm.install(&cfg)
	_, out := call(t, cfg, alice, "ctl.diagnose", `{}`)
	ext := out["data"].(map[string]any)["dsmExternal"].(map[string]any)
	next := ext["next"].([]any)
	if ext["state"] != "differs" || ext["current"].(map[string]any)["hostname"] != "old.example.net" || len(next) != 1 {
		t.Fatalf("%v", ext)
	}
	step := next[0].(map[string]any)
	if strings.Join(toStrings(step["command"]), " ") != "external set" || step["input"].(map[string]any)["hostname"] != "nas.example.com" || step["input"].(map[string]any)["httpsPort"] != 443.0 || strings.Join(toStrings(step["needs"]), " ") != "user-consent" {
		t.Fatalf("%v", step)
	}
	dsm.ext = dsmexternal.External{Hostname: "nas.example.com", HTTPSPort: &[]int{443}[0]}
	_, out = call(t, cfg, alice, "ctl.diagnose", `{}`)
	ext = out["data"].(map[string]any)["dsmExternal"].(map[string]any)
	if ext["state"] != "matches" || len(ext["next"].([]any)) != 0 || len(dsm.writes) != 0 {
		t.Fatalf("%v", ext)
	}
}
