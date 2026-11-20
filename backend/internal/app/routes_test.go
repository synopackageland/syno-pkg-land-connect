package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"context"
	appapi "dsmapp/internal/api"
	"dsmapp/internal/apps"
	"dsmapp/internal/binaries"
	"dsmapp/internal/cfaccess"
	"dsmapp/internal/dsmexternal"
	"dsmapp/internal/dsmsecurity"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

type stubAuth struct {
	identity api.Identity
	err      error
}

func (s stubAuth) Authenticate(*http.Request) (api.Identity, error) { return s.identity, s.err }

func testConfig(t *testing.T) Config {
	dir := t.TempDir()
	service := binaries.NewService(binaries.New(dir+"/binaries", binaryFixtureSource{}), nil)
	t.Cleanup(func() {
		if e := service.Close(); e != nil {
			t.Error(e)
		}
	})
	return Config{Apps: &stubAppsAPI{}, PackageID: "HelloGo", Version: "0.1.0-0001", DataDir: dir, BinaryControl: service, NetBirdServer: &serverControlFixture{}, LANSwitch: &lanSwitchFixture{}, ExternalGet: func(context.Context, string, string) (dsmexternal.External, error) {
		httpPort, httpsPort := 80, 443
		return dsmexternal.External{Hostname: "fixture.example.com", HTTPPort: &httpPort, HTTPSPort: &httpsPort}, nil
	}, SecurityGet: func(context.Context, string, string) dsmsecurity.Status {
		return dsmsecurity.Status{AutoBlock: dsmsecurity.Unknown, TwoFactor: dsmsecurity.Unknown}
	}}
}

// call returns the envelope's error code ("" on success) and the envelope.
func call(t *testing.T, cfg Config, auth api.Authenticator, method, params string) (string, map[string]any) {
	t.Helper()
	srv, err := newServer(auth, cfg)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"method":"` + method + `","params":` + params + `}`
	req := httptest.NewRequest(http.MethodPost, "/api.cgi", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// DSM's window posts from the NAS's own page.
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("not JSON: %s", rec.Body.String())
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("HTTP %d: DSM replaces non-200 CGI bodies", rec.Code)
	}
	code := ""
	if e, ok := out["error"].(map[string]any); ok {
		code, _ = e["code"].(string)
	}
	return code, out
}

var (
	alice  = stubAuth{identity: api.Identity{User: "alice", Admin: true}}
	bob    = stubAuth{identity: api.Identity{User: "bob"}}
	nobody = stubAuth{err: api.ErrNoSession}
)

// sampleParams holds valid params for every route, so the denial test below
// proves the refusal came from the policy and not from bad input.
var sampleParams = map[string]string{
	"ctl.cloudflare.access.status": `{}`,
	"ctl.cloudflare.access.sync":   `{}`,
	"ctl.cloudflare.access.set":    `{"emails":[],"revision":0,"confirm":true}`,
	"cloudflare.access.get":        `{}`,
	"cloudflare.access.sync":       `{}`,
	"cloudflare.access.set":        `{"emails":[],"revision":0,"confirm":true}`,
	"cloudflare.access.token":      `{"token":"private-fixture"}`,
	"apps.setrelay":                `{"packages":["ExampleApp"],"enabled":true}`,
	"apps.relayconnect":            `{"token":"fixture-token"}`,
	"apps.relaydisconnect":         `{}`,
	"apps.cleardsm":                `{"scope":"public"}`,
	"apps.resetstate":              `{"confirm":true}`,
	"apps.workersstatus":           `{}`,
	"apps.workersconnect":          `{"accountId":"example","token":"fixture"}`,
	"apps.workersdisconnect":       `{}`,
	"apps.workerspublishdsm":       `{}`,
	"apps.inspect":                 `{"package":"ExampleApp","scope":"private"}`,
	"apps.cloudflareconnect":       `{}`,
	"apps.cloudflaredisconnect":    `{}`,
	"apps.cleanuporphans":          `{}`,
	"apps.list":                    `{}`,
	"apps.setprivate":              `{"package":"ExampleApp","enabled":true}`,
	"apps.setpublic":               `{"packages":["ExampleApp"],"enabled":true}`,
	"apps.reenable":                `{"package":"ExampleApp","scope":"private"}`,
	"apps.clear":                   `{"package":"ExampleApp","scope":"private"}`,
	"apps.inspectdsm":              `{"scope":"public"}`,
	"apps.setdsm":                  `{"scope":"public","dsm":{"port":5001,"pin":{"mode":"S","spki":"00","issuer":"","notAfter":""}},"token":"t"}`,
	"apps.customadd":               `{"name":"Media","url":"http://127.0.0.1:8096","appSignIn":false,"insecureTls":false}`,
	"apps.customedit":              `{"id":"custom.media","name":"Media","url":"http://127.0.0.1:8096","appSignIn":false,"insecureTls":false}`,
	"apps.customremove":            `{"id":"custom.media"}`,
	"apps.custompause":             `{"id":"custom.media"}`,
	"apps.customcandidates":        `{}`,
	"security.status":              `{}`,
	"ctl.connection.connect":       `{"provider":"tailscale"}`,
	"ctl.connection.forget":        `{"provider":"ipv6"}`,
	"ctl.ports.upsert":             `{"name":"fixture","protocol":"http","port":80}`,
	"ctl.ports.delete":             `{"id":"custom-00000000000000000000000000000000"}`,
	"ctl.ports.followset":          `{"enabled":false}`,
	"ctl.ports.dsmset":             `{"id":"dsm-http","enabled":false}`,
	"ctl.ports.list":               `{}`,
	"ctl.ipv6.status":              `{}`,
	"ctl.diagnose":                 `{}`,
	"advisor.recommend":            `{"goal":"apps"}`,
	"ctl.connection.status":        `{}`,
	"ctl.connection.disconnect":    `{"provider":"all"}`,
	"binaries.status":              `{"product":"cloudflared"}`,
	"binaries.check":               `{"product":"cloudflared"}`,
	"binaries.update":              `{"product":"cloudflared"}`,
	"binaries.rollback":            `{"product":"cloudflared"}`,
	"github.token.status":          `{}`,
	"github.token.set":             `{"token":"not-a-real-token-for-tests-wxyz"}`,
	"github.token.remove":          `{}`,
	"netbirdserver.status":         `{}`,
	"netbirdserver.setup":          `{}`,
	"netbirdserver.disable":        `{"removeData":false}`,
	"netbirdserver.credentials":    `{}`,
	"integration.status":           `{}`,
	"app.status":                   `{}`,
	"phone.addnas":                 `{"protocol":"https:","host":"192.168.1.20","port":5001}`,
	"phone.lanprobe":               `{}`,
	"lanswitch.get":                `{}`,
	"lanswitch.set":                `{"enabled":true}`,
	"connection.status":            `{}`,
	"connection.connect":           `{"provider":"fake","token":"demo-connect"}`,
	"connection.disconnect":        `{}`,
	"connection.check":             `{"provider":"tailscale"}`,
	"connection.forget":            `{"provider":"ipv6"}`,
	"external.get":                 `{}`,
	"external.status":              `{}`,
	"external.set":                 `{"hostname":"fixture.example.com","httpsPort":443}`,
	"ipv6.status":                  `{}`,
	"ports.list":                   `{}`,
	"ports.upsert":                 `{"name":"fixture","protocol":"http","port":80}`,
	"ports.delete":                 `{"id":"custom-00000000000000000000000000000000"}`,
	"ports.followset":              `{"enabled":false}`,
	"ports.dsmset":                 `{"id":"dsm-http","enabled":false}`,
}

func TestIntegrationStatusIsReadableByAdministrators(t *testing.T) {
	code, out := call(t, testConfig(t), alice, "integration.status", `{}`)
	if code != "" || out["data"].(map[string]any)["state"] != "not-enabled" {
		t.Fatalf("%s: %v", code, out)
	}
	code, _ = call(t, testConfig(t), alice, "integration.status", `{"state":"ready"}`)
	if code != "bad_request" {
		t.Fatal("browser cannot supply readiness")
	}
}

func TestEveryRouteHasSampleParamsAndNoneIsPublic(t *testing.T) {
	routes := Routes(testConfig(t))
	if len(routes) != len(sampleParams) {
		t.Fatalf("%d routes but %d sample params: add the new route to sampleParams", len(routes), len(sampleParams))
	}
	for _, r := range routes {
		if _, ok := sampleParams[r.Method()]; !ok {
			t.Errorf("route %s has no sample params", r.Method())
		}
		if r.Public() {
			t.Errorf("route %s is public; this template ships none", r.Method())
		}
	}
}

func TestEveryRouteRefusesCallersWithoutASessionAndChangesNothing(t *testing.T) {
	for _, r := range Routes(testConfig(t)) {
		cfg := testConfig(t)
		code, _ := call(t, cfg, nobody, r.Method(), sampleParams[r.Method()])
		if code != "session_required" {
			t.Errorf("%s: %q, want session_required", r.Method(), code)
		}
		assertDataDirEmpty(t, cfg)
	}
}

func TestEveryRouteRequiresAdministratorAndChangesNothing(t *testing.T) {
	for _, r := range Routes(testConfig(t)) {
		if !r.AdminOnly() {
			t.Fatalf("route %s escaped centralized default", r.Method())
		}
		cfg := testConfig(t)
		code, _ := call(t, cfg, bob, r.Method(), sampleParams[r.Method()])
		if code != "admin_required" {
			t.Errorf("%s: %q, want admin_required", r.Method(), code)
		}
		assertDataDirEmpty(t, cfg)
	}
}

func TestEveryRouteSucceedsForAnAdmin(t *testing.T) {
	for _, r := range Routes(testConfig(t)) {
		cfg := testConfig(t)
		if r.Method() == "binaries.rollback" {
			m := binaries.New(cfg.DataDir+"/binaries", binaryFixtureSource{})
			if e := m.Update(context.Background(), "cloudflared"); e != nil {
				t.Fatal(e)
			}

			m = binaries.New(cfg.DataDir+"/binaries", binaryFixtureSource{version: "1.1.0"})
			if e := m.Update(context.Background(), "cloudflared"); e != nil {
				t.Fatal(e)
			}
		}

		params := sampleParams[r.Method()]
		if r.Method() == "ctl.connection.connect" {
			serveControlFixture(t, &cfg, &controlController{})
		}
		if r.Method() == "github.token.set" {
			cfg.GitHubHTTP = &http.Client{Transport: githubStub(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"resources":{"core":{"limit":5000,"remaining":5000,"reset":1792000000}}}`))}, nil
			})}
		}
		if r.Method() == "ports.delete" || r.Method() == "ctl.ports.delete" {
			code, out := call(t, cfg, alice, "ports.upsert", sampleParams["ports.upsert"])
			if code != "" {
				t.Fatal(code, out)
			}
			id := out["data"].(map[string]any)["entries"].([]any)[2].(map[string]any)["id"]
			raw, _ := json.Marshal(map[string]any{"id": id})
			params = string(raw)
		}
		code, out := call(t, cfg, alice, r.Method(), params)

		if code != "" {
			t.Errorf("%s: %q %v", r.Method(), code, out)
		}
	}
}

func TestStatusReportsPackageAndCaller(t *testing.T) {
	code, out := call(t, testConfig(t), alice, "app.status", `{}`)
	data := out["data"].(map[string]any)
	if code != "" || data["package"] != "HelloGo" || data["version"] != "0.1.0-0001" || data["user"] != "alice" || data["admin"] != true {
		t.Fatalf("got %q %v", code, out)
	}
	// Without a hint and without Accept-Language the entry page falls back to
	// the browser; existing Connect consumers still receive the NAS and
	// runtime metadata.
	if data["lang"] != "" || data["locale"] != "" || data["nasName"] == nil || data["process"] == nil {
		t.Fatalf("missing entry language hints or Connect metadata: %v", data)
	}
}

// app.status answers in the DSM display language the entry page reports.
func TestStatusFollowsTheDSMDisplayLanguage(t *testing.T) {
	for params, want := range map[string][2]string{
		`{"dsmLanguage":"cht"}`: {"cht", "zh-TW"},
		`{"dsmLanguage":"jpn"}`: {"jpn", "ja"},
	} {
		code, out := call(t, testConfig(t), alice, "app.status", params)
		data, _ := out["data"].(map[string]any)
		if code != "" || data["lang"] != want[0] || data["locale"] != want[1] || data["nasName"] == nil {
			t.Fatalf("%s: got %q %v", params, code, out)
		}
	}
}

func TestTemplateGreetingRoutesAreGone(t *testing.T) {
	for _, method := range []string{"greeting.get", "greeting.set"} {
		if code, _ := call(t, testConfig(t), alice, method, `{}`); code == "" {
			t.Errorf("%s still answers", method)
		}
	}
}

func assertDataDirEmpty(t *testing.T, cfg Config) {
	t.Helper()
	entries, err := os.ReadDir(cfg.DataDir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("data dir changed: %v %v", entries, err)
	}
}

// No network access in API tests; production verification has TLS server fixtures.
type binaryFixtureSource struct{ version string }

func (s binaryFixtureSource) Latest(_ context.Context, p string) (binaries.Release, error) {
	v := s.version
	if v == "" {
		v = "1.0.0"
	}
	return binaries.Release{Product: p, Version: v}, nil
}
func (binaryFixtureSource) Download(_ context.Context, _ binaries.Release, path string) (string, error) {
	return "sha256", os.WriteFile(path, []byte("fixture"), 0600)
}

// newServer serves Routes(cfg) under the shipped permissions.json.
func newServer(auth api.Authenticator, cfg Config) (*api.Server, error) {
	permissions, err := appapi.Permissions()
	if err != nil {
		return nil, err
	}
	return api.NewServerWithOperations(auth, appapi.Operations(), permissions, Routes(cfg)...)
}

// A post from another site or one that names no origin never reaches a route.
func TestPostsMustComeFromTheNASPage(t *testing.T) {
	srv, err := newServer(alice, testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	for header, want := range map[string]string{"": "origin_required", "https://evil.example": "cross_origin"} {
		req := httptest.NewRequest(http.MethodPost, "/api.cgi", strings.NewReader(`{"method":"app.status","params":{}}`))
		req.Header.Set("Content-Type", "application/json")
		if header != "" {
			req.Header.Set("Origin", header)
		}
		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)
		var out struct {
			Error struct{ Code string } `json:"error"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out.Error.Code != want {
			t.Fatalf("Origin %q: %s, want %s", header, rec.Body.String(), want)
		}
	}
}

// stubAppsAPI isolates the shared policy suite; real state/provider contracts
// are exercised by internal/apps and the App route tests below.
type stubAppsAPI struct{ calls int }

func (s *stubAppsAPI) List(context.Context) (apps.List, error) { s.calls++; return apps.List{}, nil }
func (s *stubAppsAPI) SetPrivate(context.Context, apps.PrivateRequest) (apps.Mutation, error) {
	s.calls++
	return apps.Mutation{}, nil
}
func (s *stubAppsAPI) SetPublic(context.Context, apps.PublicRequest) (apps.Mutation, error) {
	s.calls++
	return apps.Mutation{}, nil
}
func (s *stubAppsAPI) Reenable(context.Context, apps.ReenableRequest) (apps.Mutation, error) {
	s.calls++
	return apps.Mutation{}, nil
}
func (s *stubAppsAPI) Clear(context.Context, apps.ScopedChange) error { s.calls++; return nil }

func (s *stubAppsAPI) Inspect(context.Context, apps.InspectRequest) (apps.Inspection, error) {
	s.calls++
	return apps.Inspection{}, nil
}
func (s *stubAppsAPI) CloudflareConnect(context.Context) (apps.LoginResult, error) {
	s.calls++
	return apps.LoginResult{}, nil
}
func (s *stubAppsAPI) CloudflareDisconnect(context.Context) error { s.calls++; return nil }
func (s *stubAppsAPI) CleanupPublicOrphans(context.Context) (apps.OrphanCleanup, error) {
	s.calls++
	return apps.OrphanCleanup{}, nil
}
func (s *stubAppsAPI) InspectDSM(context.Context, apps.DSMInspectRequest) (apps.DSMInspection, error) {
	s.calls++
	return apps.DSMInspection{}, nil
}
func (s *stubAppsAPI) SetDSM(context.Context, apps.DSMRequest) (apps.DSMResult, error) {
	s.calls++
	return apps.DSMResult{}, nil
}

func (s *stubAppsAPI) WorkersStatus(context.Context) (apps.WorkersStatus, error) {
	s.calls++
	return apps.WorkersStatus{}, nil
}
func (s *stubAppsAPI) WorkersConnect(context.Context, apps.WorkersConnectRequest) (apps.WorkersStatus, error) {
	s.calls++
	return apps.WorkersStatus{}, nil
}
func (s *stubAppsAPI) WorkersDisconnect(context.Context) error { s.calls++; return nil }
func (s *stubAppsAPI) WorkersPublishDSM(context.Context) (apps.WorkersStatus, error) {
	s.calls++
	return apps.WorkersStatus{}, nil
}

func (s *stubAppsAPI) ClearDSM(context.Context, apps.DSMInspectRequest) error { s.calls++; return nil }
func (s *stubAppsAPI) ResetState(context.Context) (apps.StateReset, error) {
	s.calls++
	return apps.StateReset{}, nil
}

func (s *stubAppsAPI) CustomAdd(context.Context, apps.CustomRequest) (apps.CustomResult, error) {
	s.calls++
	return apps.CustomResult{}, nil
}
func (s *stubAppsAPI) CustomEdit(context.Context, apps.CustomRequest) (apps.CustomResult, error) {
	s.calls++
	return apps.CustomResult{}, nil
}
func (s *stubAppsAPI) CustomRemove(context.Context, apps.CustomID) error { s.calls++; return nil }
func (s *stubAppsAPI) CustomPause(context.Context, apps.CustomID) error  { s.calls++; return nil }
func (s *stubAppsAPI) CustomCandidates(context.Context) (apps.Candidates, error) {
	s.calls++
	return apps.Candidates{}, nil
}

func (s *stubAppsAPI) AccessGet(context.Context) (cfaccess.Status, error) {
	s.calls++
	return cfaccess.Status{}, nil
}
func (s *stubAppsAPI) AccessApply(context.Context, apps.AccessRequest) (cfaccess.Status, error) {
	s.calls++
	return cfaccess.Status{}, nil
}
func (s *stubAppsAPI) AccessSync(context.Context) (cfaccess.Status, error) {
	s.calls++
	return cfaccess.Status{}, nil
}
func (s *stubAppsAPI) AccessToken(context.Context, apps.AccessTokenRequest) (cfaccess.Status, error) {
	s.calls++
	return cfaccess.Status{}, nil
}

func init() {
	var registry struct {
		Operations map[string]struct{ Preview string }
	}
	if err := json.Unmarshal(appapi.Operations(), &registry); err != nil {
		panic(err)
	}
	for name, op := range registry.Operations {
		if op.Preview != "" {
			sampleParams[op.Preview] = sampleParams[name]
		}
	}
}

func TestCustomCandidatesAcceptsProjectedPortalMetadataOnly(t *testing.T) {
	params := `{"portals":[{"id":"file","display_name":"File Station","alias":"files","https_port":7443,"additional":{"default_setting":{"alias":"file"}}}]}`
	code, out := call(t, testConfig(t), alice, "apps.customcandidates", params)
	if code != "" {
		t.Fatalf("projected portal metadata refused: %s %+v", code, out)
	}
	candidates := out["data"].(map[string]any)["candidates"].([]any)
	if len(candidates) != 1 || candidates[0].(map[string]any)["url"] != "https://127.0.0.1:7443" {
		t.Fatal(out)
	}
	code, _ = call(t, testConfig(t), alice, "apps.customcandidates", `{"portals":[{"id":"file","display_name":"File Station","https_port":7443,"acl":null}]}`)
	if code != "bad_request" {
		t.Fatalf("unexpected fields must remain refused: %s", code)
	}
}
