package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"dsmapp/internal/apps"
	"dsmapp/internal/connector"
	"dsmapp/internal/publicname"
	"dsmapp/internal/relay"
)

const fixtureAccount = "0123456789abcdef0123456789abcdef"
const fixtureToken = "fixture-token-private"

type cloud struct {
	mu                     sync.Mutex
	tags                   map[string][]string
	uploads, deletes       int
	deleteFail, permission bool
	subdomain              string
	taken                  map[string]bool
	beforeUpload           func()
}

func cloudFixture(t *testing.T) (*Manager, *cloud) {
	t.Helper()
	c := &cloud{tags: map[string][]string{}, subdomain: "example"}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		defer c.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer "+fixtureToken {
			t.Error("token absent")
		}
		if c.permission {
			w.WriteHeader(403)
			fmt.Fprintf(w, `{"success":false,"errors":[{"message":%q}]}`, fixtureToken)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/accounts/"+fixtureAccount)
		var out any = map[string]any{}
		switch {
		case r.URL.Path == "/accounts":
			out = []map[string]string{{"id": fixtureAccount}}
		case r.URL.Path == "/user/tokens/verify":
			out = map[string]string{"status": "active"}
		case path == "":
			out = map[string]string{"id": fixtureAccount}
		case path == "/workers/subdomain":
			if r.Method == "PUT" {
				var in struct {
					Subdomain string `json:"subdomain"`
				}
				json.NewDecoder(r.Body).Decode(&in)
				if c.taken[in.Subdomain] {
					w.WriteHeader(409)
					w.Write([]byte(`{"success":false}`))
					return
				}
				c.subdomain = in.Subdomain
			}
			out = map[string]string{"subdomain": c.subdomain}
		case strings.HasSuffix(path, "/settings"):
			name := strings.TrimSuffix(strings.TrimPrefix(path, "/workers/scripts/"), "/settings")
			tags, ok := c.tags[name]
			if !ok {
				w.WriteHeader(404)
				w.Write([]byte(`{"success":false}`))
				return
			}
			out = map[string]any{"tags": tags}
		case strings.HasSuffix(path, "/subdomain"):
			if r.Method != "POST" {
				t.Error("bad enable method")
			}
			var in map[string]bool
			json.NewDecoder(r.Body).Decode(&in)
			if !in["enabled"] || in["previews_enabled"] {
				t.Error("bad workers.dev enable")
			}
		case strings.HasPrefix(path, "/workers/scripts/"):
			name := strings.TrimPrefix(path, "/workers/scripts/")
			if r.Method == "DELETE" {
				if c.deleteFail {
					w.WriteHeader(503)
					w.Write([]byte(`{"success":false}`))
					return
				}
				if len(c.tags[name]) == 0 {
					t.Error("deleted unowned worker")
				}
				delete(c.tags, name)
				c.deletes++
				break
			}
			if r.Method != "PUT" {
				t.Error("unexpected script method")
				return
			}
			if c.beforeUpload != nil {
				c.beforeUpload()
			}
			if err := r.ParseMultipartForm(2 << 20); err != nil {
				t.Error(err)
				return
			}
			var meta struct {
				Tags       []string            `json:"tags"`
				Bindings   []map[string]string `json:"bindings"`
				Migrations map[string]any      `json:"migrations"`
				Main       string              `json:"main_module"`
			}
			if json.Unmarshal([]byte(r.FormValue("metadata")), &meta) != nil || meta.Main != "worker.js" || len(meta.Tags) != 1 {
				t.Error("bad metadata")
			}
			if _, exists := c.tags[name]; !exists && meta.Migrations["new_sqlite_classes"] == nil {
				t.Error("missing SQLite migration")
			}
			secret := false
			for _, b := range meta.Bindings {
				if b["name"] == "RELAY_SECRET" && b["type"] == "secret_text" && len(b["text"]) == 64 {
					secret = true
				}
			}
			if !secret {
				t.Error("missing per-worker secret")
			}
			c.tags[name] = meta.Tags
			c.uploads++
		default:
			t.Error("unexpected API endpoint", path)
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": out})
	}))
	t.Cleanup(s.Close)
	m := &Manager{Store: Store{Dir: filepath.Join(t.TempDir(), "private")}, API: API{Base: s.URL, HTTP: s.Client(), Script: []byte("export default {}")}}
	t.Cleanup(m.Close)
	return m, c
}
func connectFixture(t *testing.T, m *Manager) {
	t.Helper()
	if _, err := m.Connect(context.Background(), apps.WorkersConnectRequest{Token: fixtureToken, AccountID: fixtureAccount}); err != nil {
		t.Fatal(err)
	}
}
func TestDeployInventoryRetryAndDisconnectRetainsTokenUntilCleanup(t *testing.T) {
	m, c := cloudFixture(t)
	connectFixture(t, m)
	c.beforeUpload = func() {
		v, err := m.Store.load()
		if err != nil || len(v.Records) == 0 {
			t.Error("upload preceded durable inventory")
		}
	}
	ctx := context.Background()
	if err := m.Ensure(ctx, []string{"", "ExampleApp"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Ensure(ctx, []string{"", "ExampleApp"}); err != nil {
		t.Fatal(err)
	}
	if c.uploads != 2 {
		t.Fatal("repeat deployed again")
	}
	v, err := m.Store.load()
	if err != nil || v.Records[""].Secret == v.Records["ExampleApp"].Secret {
		t.Fatal("secret not isolated")
	}
	for path, mode := range map[string]os.FileMode{m.Store.Dir: 0700, filepath.Join(m.Store.Dir, "state.json"): 0600} {
		st, err := os.Stat(path)
		if err != nil || st.Mode().Perm() != mode {
			t.Fatal("credentials permissions")
		}
	}
	c.deleteFail = true
	if err = m.Disconnect(ctx); err == nil {
		t.Fatal("lost cleanup acknowledged")
	}
	v, err = m.Store.load()
	if err != nil || v.Token == "" || !v.Disconnecting {
		t.Fatal("cleanup discarded credentials")
	}
	s, err := m.Status(ctx)
	raw, _ := json.Marshal(s)
	if err != nil || !s.Connected || !s.Invalid || strings.Contains(string(raw), fixtureToken) {
		t.Fatal("status leaked or reenabled")
	}
	c.deleteFail = false
	if err = m.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if c.deletes != 2 {
		t.Fatal(c.deletes)
	}
	if _, err = os.Stat(filepath.Join(m.Store.Dir, "state.json")); !os.IsNotExist(err) {
		t.Fatal("credential retained")
	}
}
func TestWorkerCollisionDoesNotOverwriteOrDeleteForeignScript(t *testing.T) {
	m, c := cloudFixture(t)
	connectFixture(t, m)
	short := shortName("ExampleApp")
	name := publicname.Candidate(short, publicname.NASName(), 1)
	c.tags[short] = []string{"other-owner"}
	c.tags[name] = []string{"other-owner"}
	selected := publicname.Candidate(short, publicname.NASName(), 2)
	if err := m.Ensure(context.Background(), []string{"ExampleApp"}); err != nil {
		t.Fatal(err)
	}
	if len(c.tags[selected]) != 1 {
		t.Fatal("numeric fallback missing")
	}
	if err := m.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.uploads != 1 || c.deletes != 1 || len(c.tags[name]) != 1 || len(c.tags[short]) != 1 {
		t.Fatal("foreign Worker changed")
	}
}

func TestErrorsAndStatusNeverExposeCredentials(t *testing.T) {
	m, c := cloudFixture(t)
	c.permission = true
	_, err := m.Connect(context.Background(), apps.WorkersConnectRequest{Token: fixtureToken, AccountID: fixtureAccount})
	if connector.ErrorCode(err) != "workers_permission_denied" || strings.Contains(err.Error(), fixtureToken) {
		t.Fatal("unsafe provider error")
	}
	if _, err = os.Stat(filepath.Join(m.Store.Dir, "state.json")); !os.IsNotExist(err) {
		t.Fatal("invalid token was saved")
	}
}
func TestReconcileUsesOnlyApprovedBindingsAndCancelsOnWithdrawal(t *testing.T) {
	m, _ := cloudFixture(t)
	connectFixture(t, m)
	ctx := context.Background()
	if err := m.Ensure(ctx, []string{"ExampleApp"}); err != nil {
		t.Fatal(err)
	}
	host, err := m.Authority(ctx, "ExampleApp")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan relay.Config, 1)
	stopped := make(chan struct{}, 1)
	m.Run = func(ctx context.Context, cfg relay.Config) { started <- cfg; <-ctx.Done(); stopped <- struct{}{} }
	b := apps.PinBinding{Provider: apps.Workers, Scope: apps.Public, Target: apps.TargetApp, Package: "ExampleApp", Authority: host, Socket: "approved"}
	if err = m.Reconcile(ctx, []apps.PinBinding{b}); err != nil {
		t.Fatal(err)
	}
	select {
	case cfg := <-started:
		if cfg.Socket != apps.PinSocket("approved") || cfg.Authority != host || strings.Contains(cfg.URL, fixtureToken) || cfg.Token == "" {
			t.Fatal("unsafe connector configuration")
		}
	case <-time.After(time.Second):
		t.Fatal("not started")
	}
	if err = m.Reconcile(ctx, nil); err != nil {
		t.Fatal(err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("withdrawal did not stop connector")
	}
	if err = m.Reconcile(ctx, []apps.PinBinding{{Provider: apps.Workers, Scope: apps.Public, Package: "ExampleApp", Authority: "unapproved.example", Socket: "other"}}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
		t.Fatal("unapproved route started")
	case <-time.After(20 * time.Millisecond):
	}
}
func TestPrivateStateRefusesSymlinkAndLooseModes(t *testing.T) {
	m, _ := cloudFixture(t)
	connectFixture(t, m)
	path := filepath.Join(m.Store.Dir, "state.json")
	if os.Chmod(path, 0644) != nil {
		t.Fatal("chmod")
	}
	if _, err := m.Store.load(); err == nil {
		t.Fatal("loose credential file accepted")
	}
	os.Remove(path)
	os.Symlink(filepath.Join(t.TempDir(), "absent"), path)
	if _, err := m.Store.load(); err == nil {
		t.Fatal("symlink accepted")
	}
}
func TestConnectCreatesOnlyMissingSubdomain(t *testing.T) {
	m, c := cloudFixture(t)
	c.subdomain = ""
	_, err := m.Connect(context.Background(), apps.WorkersConnectRequest{Token: fixtureToken, AccountID: fixtureAccount, Subdomain: "requested"})
	if err != nil || c.subdomain != "requested" {
		t.Fatal("missing subdomain not created")
	}
	_, err = m.Connect(context.Background(), apps.WorkersConnectRequest{Token: fixtureToken, AccountID: fixtureAccount, Subdomain: "other"})
	if err != nil || c.subdomain != "requested" {
		t.Fatal("existing subdomain renamed")
	}
}

func TestConnectAsksForMissingSubdomainWithoutSaving(t *testing.T) {
	m, c := cloudFixture(t)
	c.subdomain = ""
	out, err := m.Connect(context.Background(), apps.WorkersConnectRequest{Token: fixtureToken, AccountID: fixtureAccount})
	if err != nil || out.Connected || out.Code != "workers_subdomain_required" || c.subdomain != "" {
		t.Fatal("missing subdomain not asked for", out, err)
	}
	if v, err := m.Status(context.Background()); err != nil || v.Connected || v.Code != "" {
		t.Fatal("token saved before a name was chosen", v, err)
	}
}

func TestConnectReportsTakenSubdomainAndAcceptsAnother(t *testing.T) {
	m, c := cloudFixture(t)
	c.subdomain = ""
	c.taken = map[string]bool{"my-nas": true}
	ctx := context.Background()
	_, err := m.Connect(ctx, apps.WorkersConnectRequest{Token: fixtureToken, AccountID: fixtureAccount, Subdomain: "my-nas"})
	if connector.ErrorCode(err) != "workers_subdomain_taken" {
		t.Fatal("taken subdomain not reported", err)
	}
	if v, _ := m.Status(ctx); v.Connected || v.Code != "workers_subdomain_required" {
		t.Fatal("pending name not reported", v)
	}
	out, err := m.Connect(ctx, apps.WorkersConnectRequest{Token: fixtureToken, AccountID: fixtureAccount, Subdomain: "my-nas-2"})
	if err != nil || !out.Connected || out.Subdomain != "my-nas-2" || c.subdomain != "my-nas-2" {
		t.Fatal("second name not claimed", out, err)
	}
}

func TestTokenOnlyOnboardingSelectsSingleAccessibleAccount(t *testing.T) {
	m, _ := cloudFixture(t)
	out, err := m.Connect(context.Background(), apps.WorkersConnectRequest{Token: fixtureToken})
	if err != nil || out.AccountID != fixtureAccount || !out.Connected {
		t.Fatal("token-only onboarding failed", err)
	}
}

func TestCredentialRefreshDuringPendingDisconnectCannotReenableRelays(t *testing.T) {
	m, c := cloudFixture(t)
	connectFixture(t, m)
	ctx := context.Background()
	if err := m.Ensure(ctx, []string{"ExampleApp"}); err != nil {
		t.Fatal(err)
	}
	c.deleteFail = true
	if m.Disconnect(ctx) == nil {
		t.Fatal("cleanup failure missing")
	}
	out, err := m.Connect(ctx, apps.WorkersConnectRequest{Token: fixtureToken, AccountID: fixtureAccount})
	if err != nil || !out.Connected || !out.Invalid || out.Code != "workers_cleanup_required" {
		t.Fatal("cannot replace cleanup credential", err)
	}
	if _, err = m.Authority(ctx, "ExampleApp"); err == nil {
		t.Fatal("pending disconnect revived authority")
	}
	c.deleteFail = false
	if err = m.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
}
func TestClearDeletesOnlyTheSelectedAppWorker(t *testing.T) {
	m, c := cloudFixture(t)
	connectFixture(t, m)
	ctx := context.Background()
	if err := m.Ensure(ctx, []string{"", "ExampleApp"}); err != nil {
		t.Fatal(err)
	}
	if err := m.DeleteApp(ctx, "ExampleApp"); err != nil {
		t.Fatal(err)
	}
	v, err := m.Store.load()
	if err != nil || len(v.Records) != 1 || v.Records[""].Name == "" || c.deletes != 1 {
		t.Fatal("Clear changed the shared DSM Worker", err)
	}
}

func TestCustomWorkerNamesAreCheckedAsABatchAndPreserved(t *testing.T) {
	m, c := cloudFixture(t)
	connectFixture(t, m)
	ctx := apps.WithHostnames(context.Background(), map[string]string{"": "my-nas.example.workers.dev", "ExampleApp": "my-app.example.workers.dev"})
	c.tags["my-app"] = []string{"another-installation"}
	if err := m.Ensure(ctx, []string{"", "ExampleApp"}); connector.ErrorCode(err) != "workers_name_conflict" {
		t.Fatal(err)
	}
	if c.uploads != 0 {
		t.Fatal("created DSM before the App collision check")
	}
	delete(c.tags, "my-app")
	if err := m.Ensure(ctx, []string{"", "ExampleApp"}); err != nil {
		t.Fatal(err)
	}
	for pkg, want := range map[string]string{"": "my-nas.example.workers.dev", "ExampleApp": "my-app.example.workers.dev"} {
		got, err := m.Authority(context.Background(), pkg)
		if err != nil || got != want {
			t.Fatalf("changed saved name: %q %v", got, err)
		}
	}
	v, _ := m.Store.load()
	v.Records["LegacyApp"] = Record{Name: "spl-legacy-1234", Secret: strings.Repeat("a", 64)}
	if err := m.Store.save(v); err != nil {
		t.Fatal(err)
	}
	got, err := m.Authority(context.Background(), "LegacyApp")
	if err != nil || got != "spl-legacy-1234.example.workers.dev" {
		t.Fatalf("migrated legacy name: %q %v", got, err)
	}
}

func TestWorkerDefaultIsTheAppShortNameWithASuffixOnlyWhenTaken(t *testing.T) {
	m, c := cloudFixture(t)
	connectFixture(t, m)
	ctx := context.Background()
	if got, err := m.Authority(ctx, "SynoPkgJellyfin"); err != nil || got != "jellyfin.example.workers.dev" {
		t.Fatalf("short name: %q %v", got, err)
	}
	// Another NAS on the same account already has jellyfin: this one adds its suffix.
	c.tags["jellyfin"] = []string{"other-owner"}
	v, _ := m.Store.load()
	got, err := m.Authority(ctx, "SynoPkgJellyfin")
	if err != nil || got != publicname.Candidate("jellyfin", publicname.NASName(), 1)+".example.workers.dev" || !strings.HasPrefix(got, "jellyfin-") {
		t.Fatalf("suffix on collision: %q %v", got, err)
	}
	// Another App on this NAS that kept the short name also counts as taken.
	delete(c.tags, "jellyfin")
	v.Records["SynoPkgOther"] = Record{Name: "jellyfin", Secret: strings.Repeat("a", 64)}
	if err = m.Store.save(v); err != nil {
		t.Fatal(err)
	}
	if got, err = m.Authority(ctx, "SynoPkgJellyfin"); err != nil || !strings.HasPrefix(got, "jellyfin-") {
		t.Fatalf("local collision: %q %v", got, err)
	}
	if shortName("") != "dsm" || shortName("SynoPkg") != "synopkg" || shortName("Gopeed") != "gopeed" {
		t.Fatal("short names")
	}
}

func TestWorkerDefaultsUseNASNameThenNumbers(t *testing.T) {
	m, c := cloudFixture(t)
	connectFixture(t, m)
	base := "jellyfin-" + publicname.NASName()
	c.tags["jellyfin"] = []string{"other-owner"}
	c.tags[base] = []string{"other-owner"}
	c.tags[base+"-2"] = []string{"other-owner"}
	got, err := m.Authority(context.Background(), "SynoPkgJellyfin")
	if err != nil || got != base+"-3.example.workers.dev" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestWorkerConflictOffersVerifiedAlternative(t *testing.T) {
	m, c := cloudFixture(t)
	connectFixture(t, m)
	c.tags["chosen"] = []string{"foreign"}
	c.tags["chosen-2"] = []string{"foreign"}
	ctx := apps.WithHostnames(context.Background(), map[string]string{"ExampleApp": "chosen.example.workers.dev"})
	err := m.Check(ctx, []string{"ExampleApp"})
	d := connector.Conflict(err)
	if connector.ErrorCode(err) != "workers_name_conflict" || d == nil || d.SuggestedName != "chosen-3.example.workers.dev" || d.DNSURL != "https://dash.cloudflare.com/"+fixtureAccount+"/workers-and-pages" {
		t.Fatalf("missing recovery: %v %+v", err, d)
	}
	if c.uploads != 0 || c.deletes != 0 {
		t.Fatal("conflict modified a Worker")
	}
}

// A rename in the Cloudflare dashboard moves every Worker; Connect follows it
// on the next status read and on a token paste, never asking to disconnect.
func TestFollowPicksUpACloudflareSideRename(t *testing.T) {
	m, c := cloudFixture(t)
	connectFixture(t, m)
	ctx := context.Background()
	if err := m.Ensure(ctx, []string{"", "ExampleApp"}); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	c.subdomain = "renamed"
	c.mu.Unlock()
	sub, err := m.Follow(ctx)
	if err != nil || sub != "renamed" {
		t.Fatalf("follow: %q %v", sub, err)
	}
	if st, _ := m.Status(ctx); st.Subdomain != "renamed" || !st.Connected {
		t.Fatalf("status kept the old name: %+v", st)
	}
	host, err := m.Authority(ctx, "ExampleApp")
	if err != nil || !strings.HasSuffix(host, ".renamed.workers.dev") {
		t.Fatalf("authority: %q %v", host, err)
	}
	v, _ := m.Store.load()
	if len(v.Records) != 2 || c.uploads != 2 {
		t.Fatal("a rename redeployed or dropped Workers")
	}
	c.mu.Lock()
	c.subdomain = "again"
	c.mu.Unlock()
	if st, err := m.Connect(ctx, apps.WorkersConnectRequest{Token: fixtureToken, AccountID: fixtureAccount}); err != nil || st.Subdomain != "again" {
		t.Fatalf("token paste after a rename: %+v %v", st, err)
	}
}

func TestRelayStatusIsIndependentOfAccountAndProxyAndExpiresSetup(t *testing.T) {
	m, _ := cloudFixture(t)
	connectFixture(t, m)
	ctx := context.Background()
	if err := m.Ensure(ctx, []string{""}); err != nil {
		t.Fatal(err)
	}
	authority, err := m.Authority(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan relay.Config, 1)
	m.Run = func(ctx context.Context, c relay.Config) { started <- c; <-ctx.Done() }
	b := apps.PinBinding{Provider: apps.Workers, Scope: apps.Public, Target: apps.TargetDSM, Authority: authority, Socket: "approved"}
	if err := m.Reconcile(ctx, []apps.PinBinding{b}); err != nil {
		t.Fatal(err)
	}
	c := <-started
	read := func(want string) {
		t.Helper()
		s, err := m.Status(ctx)
		if err != nil || !s.Connected || s.RelayState != want {
			t.Fatalf("want %s, got %+v %v", want, s, err)
		}
	}
	read("connecting")
	m.mu.Lock()
	v := m.live[""]
	v.started = time.Now().Add(-2 * time.Minute)
	m.live[""] = v
	m.mu.Unlock()
	read("reconnecting")
	c.Connected(true)
	read("connected")
	c.Connected(false)
	read("reconnecting")
	if err := m.Reconcile(ctx, nil); err != nil {
		t.Fatal(err)
	}
	c.Connected(true) // A withdrawn generation cannot report a live route.
	read("reconnecting")
}

func TestDeletedRelayScriptIsRecreatedFromInventoryWithSameSecret(t *testing.T) {
	m, c := cloudFixture(t)
	connectFixture(t, m)
	ctx := context.Background()
	if err := m.Ensure(ctx, []string{"ExampleApp"}); err != nil {
		t.Fatal(err)
	}
	before, err := m.Store.load()
	if err != nil {
		t.Fatal(err)
	}
	saved := before.Records["ExampleApp"]
	c.mu.Lock()
	delete(c.tags, saved.Name)
	c.mu.Unlock()
	if err = m.Ensure(ctx, []string{"ExampleApp"}); err != nil {
		t.Fatal(err)
	}
	after, err := m.Store.load()
	if err != nil {
		t.Fatal(err)
	}
	savedRaw, _ := json.Marshal(saved)
	afterRaw, _ := json.Marshal(after.Records["ExampleApp"])
	if string(afterRaw) != string(savedRaw) || c.uploads != 2 || len(c.tags[saved.Name]) != 1 {
		t.Fatal("deleted relay was not restored with original identity")
	}
	c.mu.Lock()
	c.tags[saved.Name] = []string{"another-owner"}
	c.mu.Unlock()
	if err = m.Ensure(ctx, []string{"ExampleApp"}); connector.ErrorCode(err) != "workers_name_conflict" || c.uploads != 2 {
		t.Fatal("foreign replacement was overwritten", err)
	}
}
