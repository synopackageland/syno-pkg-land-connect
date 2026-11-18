package app

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"dsmapp/internal/apps"
)

func TestAppsRoutesRejectSpoofedBindingsBeforeDaemon(t *testing.T) {
	cfg := testConfig(t)
	stub := &stubAppsAPI{}
	cfg.Apps = stub
	for _, test := range []struct{ method, params string }{
		{"apps.list", `{"refresh":true}`},
		{"apps.setprivate", `{"package":"ExampleApp","enabled":true,"port":5001}`},
		{"apps.setprivate", `{"package":"ExampleApp"}`},
		{"apps.setpublic", `{"packages":["ExampleApp"],"origin":"https://example.com"}`},
		{"apps.reenable", `{"package":"ExampleApp","scope":"private","pin":{"mode":"S"}}`},
		{"apps.clear", `{"package":"ExampleApp","scope":"private","socket":"a1"}`},
		{"apps.inspectdsm", `{"scope":"public","authority":"dsm.example.com"}`},
		{"apps.setdsm", `{"scope":"public","token":"t","socket":"a1"}`},
		{"apps.cleanuporphans", `{"package":"ExampleApp"}`},
	} {
		if code, _ := call(t, cfg, alice, test.method, test.params); code != "bad_request" {
			t.Fatalf("%s accepted spoofed input: %s", test.method, code)
		}
	}
	if stub.calls != 0 {
		t.Fatal("invalid input reached daemon")
	}
}

type corruptAppsAPI struct{ stubAppsAPI }

func (*corruptAppsAPI) SetPrivate(context.Context, apps.PrivateRequest) (apps.Mutation, error) {
	return apps.Mutation{}, apps.ErrCorrupt
}
func TestAppsErrorsPreserveRecoveryCode(t *testing.T) {
	cfg := testConfig(t)
	cfg.Apps = &corruptAppsAPI{}
	if code, _ := call(t, cfg, alice, "apps.setprivate", `{"package":"ExampleApp","enabled":true}`); code != "apps_state_corrupt" {
		t.Fatal(code)
	}
}

// Every method the App URLs page calls is a registered route with that exact name.
func TestAppURLsPageMethodsAreRoutes(t *testing.T) {
	raw, err := os.ReadFile("../../../frontend/src/connect/appUrls.ts")
	if err != nil {
		t.Fatal(err)
	}
	block := regexp.MustCompile(`(?s)export const appsApi = \{(.*?)\}`).FindSubmatch(raw)
	if block == nil {
		t.Fatal("appsApi not found")
	}
	routes := map[string]bool{}
	for _, r := range Routes(testConfig(t)) {
		routes[r.Method()] = true
	}
	methods := regexp.MustCompile(`'(apps\.[a-z]+)'`).FindAllSubmatch(block[1], -1)
	if len(methods) != 22 {
		t.Fatalf("page calls %d apps methods", len(methods))
	}
	for _, m := range methods {
		if !routes[string(m[1])] {
			t.Errorf("page calls %s, which is not a route", m[1])
		}
	}
}

func TestResettingAppURLRecordsNeedsAnAdminAndConfirmation(t *testing.T) {
	cfg := testConfig(t)
	if code, _ := call(t, cfg, bob, "apps.resetstate", `{"confirm":true}`); code != "admin_required" {
		t.Fatal("only an administrator may reset the records", code)
	}
	if code, _ := call(t, cfg, alice, "apps.resetstate", `{}`); code != "bad_request" {
		t.Fatal("a reset needs the page's confirmation", code)
	}
}

func TestDSMCertificateRoutesDoNotDependOnAdminRequestOrigin(t *testing.T) {
	for _, origin := range []string{"http://192.0.2.10:5000", "https://192.0.2.10:5001", "http://admin.example:6100", "https://admin.example:6443"} {
		t.Run(origin, func(t *testing.T) {
			cfg := testConfig(t)
			stub := &stubAppsAPI{}
			cfg.Apps = stub
			srv, err := newServer(alice, cfg)
			if err != nil {
				t.Fatal(err)
			}
			for _, input := range []struct{ method, params string }{{"apps.inspectdsm", `{"scope":"public"}`}, {"apps.setdsm", `{"scope":"public","dsm":{"port":6443,"pin":{"mode":"S","spki":"` + strings.Repeat("a", 64) + `","issuer":"Fixture","notAfter":"2030-01-01T00:00:00Z"}},"token":"fixture"}`}} {
				req := httptest.NewRequest(http.MethodPost, origin+"/api.cgi", strings.NewReader(`{"method":"`+input.method+`","params":`+input.params+`}`))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("Origin", origin)
				// HTTP LAN pages may omit Fetch Metadata; the same-origin Origin fallback must work too.
				rec := httptest.NewRecorder()
				srv.ServeHTTP(rec, req)
				var out struct {
					Success bool `json:"success"`
					Error   any  `json:"error"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || !out.Success {
					t.Fatal("valid admin origin rejected", rec.Body.String(), err)
				}
			}
			if stub.calls != 2 {
				t.Fatal("origin altered daemon dispatch", stub.calls)
			}
		})
	}
}
