package cfaccess

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

type cloudFixture struct {
	apps                               map[string]Application
	creates, updates, deletes, revokes int
	lostReply                          bool
	foreign                            bool
	readFailure                        bool
	badSession                         bool
	updateFailure                      bool
}

func managerFixture(t *testing.T) (*Manager, *cloudFixture) {
	t.Helper()
	f := &cloudFixture{apps: map[string]Application{}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		reply := func(v any) {
			json.NewEncoder(w).Encode(map[string]any{"success": true, "result": v, "result_info": map[string]int{"total_pages": 1}})
		}
		switch {
		case path == "/user/tokens/verify":
			reply(map[string]string{"id": "token-id", "status": "active"})
		case path == "/user/tokens/token-id":
			w.WriteHeader(403)
			fmt.Fprint(w, `{"success":false}`)
		case strings.HasSuffix(path, "/organizations"):
			reply(Organization{Domain: "fixture.cloudflareaccess.com"})
		case strings.HasSuffix(path, "/identity_providers"):
			reply([]IdentityProvider{{ID: "otp-id", Type: "onetimepin"}})
		case strings.HasSuffix(path, "/apps") && r.Method == "GET":
			out := []Application{}
			for _, a := range f.apps {
				out = append(out, a)
			}
			if f.foreign {
				out = append(out, Application{ID: "foreign", Name: "not ours", Domain: "web.example.test/private"})
			}
			reply(out)
		case strings.HasSuffix(path, "/apps") && r.Method == "POST":
			var a Application
			json.NewDecoder(r.Body).Decode(&a)
			f.creates++
			a.ID = fmt.Sprint(f.creates)
			a.AUD = "aud-" + a.ID
			a.Policies[0].ID = "policy-" + a.ID
			f.apps[a.ID] = a
			if f.lostReply {
				f.lostReply = false
				w.WriteHeader(500)
				fmt.Fprint(w, `{"success":false}`)
				return
			}
			reply(a)
		case strings.HasSuffix(path, "/revoke_tokens"):
			f.revokes++
			reply(map[string]bool{})
		default:
			id := path[strings.LastIndex(path, "/")+1:]
			a, ok := f.apps[id]
			if !ok {
				w.WriteHeader(404)
				fmt.Fprint(w, `{"success":false}`)
				return
			}
			switch r.Method {
			case "GET":
				if f.readFailure {
					w.WriteHeader(503)
					fmt.Fprint(w, `{"success":false}`)
					return
				}
				if f.badSession {
					raw, _ := json.Marshal(a)
					var out map[string]any
					json.Unmarshal(raw, &out)
					out["session_duration"] = map[string]any{"invalid": true}
					reply(out)
					return
				}
				reply(a)
			case "PUT":
				if f.updateFailure {
					w.WriteHeader(503)
					fmt.Fprint(w, `{"success":false}`)
					return
				}
				// PUT replaces the application; decoding into the old nested maps
				// would merge removed include rules into the fake response.
				f.updates++
				a = Application{}
				json.NewDecoder(r.Body).Decode(&a)
				a.ID = id
				a.AUD = "aud-" + id
				a.Policies[0].ID = "policy-" + id
				f.apps[id] = a
				reply(a)
			case "DELETE":
				f.deletes++
				delete(f.apps, id)
				reply(map[string]bool{})
			default:
				t.Errorf("unexpected request %s %s", r.Method, path)
			}
		}
	}))
	t.Cleanup(srv.Close)
	m := New(Config{Path: filepath.Join(t.TempDir(), "private", "access.json"), API: API{Base: srv.URL}, Credential: func() (Credential, error) { return Credential{Account: "account-id", Token: "secret"}, nil }, Endpoints: func() (Inventory, error) {
		return Inventory{Account: "account-id", DSMHost: "web.example.test", Hosts: []string{"web.example.test"}}, nil
	}})
	return m, f
}
func TestEmailValidation(t *testing.T) {
	got, e := Emails([]string{" reader@EXAMPLE.test ", "reader@example.test"})
	if e != nil || len(got) != 1 {
		t.Fatal(got, e)
	}
	for _, v := range []string{"", "*@example.test", "Name <reader@example.test>", "a@example.test\nb@example.test", "example.test", "a@*.example.test"} {
		if _, e := Emails([]string{v}); e == nil {
			t.Fatal("accepted invalid email")
		}
	}
}
func TestApplyDriftRemovalAndBorrowedOTP(t *testing.T) {
	m, f := managerFixture(t)
	ctx := context.Background()
	if m.InUse() {
		t.Fatal("unused feature active")
	}
	if _, e := m.Apply(ctx, Request{Emails: []string{"reader@example.test"}}); e != nil {
		t.Fatal(Code(e), e)
	}
	if m.Status().State != "synced" || f.creates != 1 {
		t.Fatal(m.Status(), f.creates)
	}
	for id, a := range f.apps {
		a.Policies = append(a.Policies, Policy{Decision: "bypass"})
		f.apps[id] = a
	}
	if e := m.Sync(ctx); e != nil || f.updates != 1 {
		t.Fatal(e, f.updates)
	}
	if _, e := m.Apply(ctx, Request{Revision: m.Status().Revision, Emails: []string{"editor@example.test"}}); e != nil || f.revokes == 0 {
		t.Fatal(e, f.revokes)
	}
	if _, e := m.Apply(ctx, Request{Revision: m.Status().Revision, Emails: []string{}}); e != nil || f.deletes != 1 || m.InUse() {
		t.Fatal(e, f.deletes, m.Status())
	}
}
func TestLostCreateReplyRecoversExactlyOneObjectAcrossRestart(t *testing.T) {
	m, f := managerFixture(t)
	f.lostReply = true
	ctx := context.Background()
	if _, e := m.Apply(ctx, Request{Emails: []string{"reader@example.test"}}); e == nil {
		t.Fatal("missing failure")
	}
	if m.Status().State == "synced" {
		t.Fatal("unverified success")
	}
	restored := New(m.cfg)
	if e := restored.Sync(ctx); e != nil || f.creates != 1 {
		t.Fatal(e, f.creates)
	}
}
func TestForeignOverlapAndOwnershipDriftNeverMutate(t *testing.T) {
	m, f := managerFixture(t)
	ctx := context.Background()
	f.foreign = true
	if _, e := m.Apply(ctx, Request{Emails: []string{"reader@example.test"}}); Code(e) != "access_conflict" || f.creates != 0 || m.InUse() {
		t.Fatal(Code(e), f.creates, m.Status())
	}
	f.foreign = false
	if _, e := m.Apply(ctx, Request{Emails: []string{"reader@example.test"}}); e != nil {
		t.Fatal(Code(e), e)
	}
	for id, a := range f.apps {
		a.Name = "someone else"
		f.apps[id] = a
	}
	if e := m.Sync(ctx); Code(e) != "access_conflict" || f.updates != 0 || f.deletes != 0 {
		t.Fatal(Code(e))
	}
}
func TestPreflightFailureKeepsIntentAndExistingPublishing(t *testing.T) {
	m, _ := managerFixture(t)
	m.cfg.Credential = func() (Credential, error) { return Credential{}, nil }
	if _, e := m.Apply(context.Background(), Request{Emails: []string{"reader@example.test"}}); e == nil || m.InUse() {
		t.Fatal(e, m.Status())
	}
}
func TestUnsupportedAliasAndStaleRevisionRefused(t *testing.T) {
	m, _ := managerFixture(t)
	m.cfg.Endpoints = func() (Inventory, error) { return Inventory{Account: "account-id", Unsupported: true}, nil }
	if _, e := m.Apply(context.Background(), Request{Emails: []string{"reader@example.test"}}); Code(e) != "access_alias_unsupported" || m.InUse() {
		t.Fatal(Code(e))
	}
	m.cfg.Endpoints = func() (Inventory, error) {
		return Inventory{Account: "account-id", DSMHost: "web.example.test", Hosts: []string{"web.example.test"}}, nil
	}
	if _, e := m.Apply(context.Background(), Request{Revision: 99, Emails: []string{"reader@example.test"}}); Code(e) != "access_revision_changed" {
		t.Fatal(Code(e))
	}
}

func TestDeletedAppLostRecreateReplyDoesNotDuplicate(t *testing.T) {
	m, f := managerFixture(t)
	ctx := context.Background()
	if _, e := m.Apply(ctx, Request{Emails: []string{"reader@example.test"}}); e != nil {
		t.Fatal(Code(e))
	}
	for id := range f.apps {
		delete(f.apps, id)
	}
	f.lostReply = true
	if e := m.Sync(ctx); e == nil {
		t.Fatal("lost reply unexpectedly successful")
	}
	restored := New(m.cfg)
	if e := restored.Sync(ctx); e != nil || f.creates != 2 || len(f.apps) != 1 {
		t.Fatal(Code(e), f.creates, len(f.apps))
	}
}
func TestAcceptedJobReportsPreflightFailureWithoutChangingIntent(t *testing.T) {
	m, _ := managerFixture(t)
	defer m.Close()
	m.cfg.Credential = func() (Credential, error) { return Credential{}, nil }
	if _, e := m.Submit(Request{Emails: []string{"reader@example.test"}}); e != nil {
		t.Fatal(Code(e))
	}
	m.wg.Wait()
	st := m.Status()
	if st.Working || st.Code != "access_token_required" || m.InUse() || st.Revision != 0 {
		t.Fatal(st)
	}
}
func TestInventoryChangeCannotCommitStaleCoverage(t *testing.T) {
	m, _ := managerFixture(t)
	reads := 0
	m.cfg.Endpoints = func() (Inventory, error) {
		reads++
		hosts := []string{"web.example.test"}
		if reads > 1 {
			hosts = append(hosts, "new.example.test")
		}
		return Inventory{Account: "account-id", Hosts: hosts}, nil
	}
	if _, e := m.Apply(context.Background(), Request{Emails: []string{"reader@example.test"}}); Code(e) != "access_revision_changed" || m.Status().State == "synced" {
		t.Fatal(Code(e), m.Status())
	}
}
func TestOriginAdmissionRejectsUnsupportedConnectionsAfterEnabling(t *testing.T) {
	m, _ := managerFixture(t)
	if _, e := m.Apply(context.Background(), Request{Emails: []string{"reader@example.test"}}); e != nil {
		t.Fatal(Code(e))
	}
	called := false
	if e := m.Unrestricted(func() error { called = true; return nil }); Code(e) != "access_alias_unsupported" || called {
		t.Fatal(Code(e), called)
	}
}

func TestCanonicalComparisonNeverChangesPolicyIdentity(t *testing.T) {
	a := desiredApp("owned", "web.example.test", "otp", []string{"reader@example.test"}, "policy-id")
	a.ID = "app-id"
	a.AUD = "aud"
	canonical(a)
	if a.ID != "app-id" || a.AUD != "aud" || a.Policies[0].ID != "policy-id" {
		t.Fatal("comparison mutated provider identity")
	}
	m, _ := managerFixture(t)
	if _, e := m.Apply(context.Background(), Request{Emails: []string{"reader@example.test"}}); e != nil {
		t.Fatal(Code(e))
	}
	for _, r := range m.state.Records {
		if r.Policy == "" || r.Digest == "" {
			t.Fatal("missing readback evidence")
		}
	}
}
func TestOwnedAppMovedToAdditionalHostnameIsAConflict(t *testing.T) {
	m, f := managerFixture(t)
	ctx := context.Background()
	if _, e := m.Apply(ctx, Request{Emails: []string{"reader@example.test"}}); e != nil {
		t.Fatal(Code(e))
	}
	for id, a := range f.apps {
		a.Destinations = []map[string]any{{"type": "public", "uri": "other.example.test"}}
		f.apps[id] = a
	}
	if e := m.Sync(ctx); Code(e) != "access_conflict" || f.updates != 0 {
		t.Fatal(Code(e), f.updates)
	}
}

func TestAmbiguousCreateWithoutVisibleReceiptNeverReplaysPOST(t *testing.T) {
	m, f := managerFixture(t)
	f.lostReply = true
	ctx := context.Background()
	if _, e := m.Apply(ctx, Request{Emails: []string{"reader@example.test"}}); e == nil {
		t.Fatal("expected ambiguous delivery")
	}
	// An empty list after an ambiguous POST is not proof it was never delivered.
	for id := range f.apps {
		delete(f.apps, id)
	}
	restored := New(m.cfg)
	if e := restored.Sync(ctx); Code(e) != "access_conflict" || f.creates != 1 {
		t.Fatal(Code(e), f.creates)
	}
}

func TestAmbiguousOTPCreateWaitsForExactNonceReceipt(t *testing.T) {
	m, _ := managerFixture(t)
	m.state.Owner = strings.Repeat("a", 32)
	created := IdentityProvider{}
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Error("unexpected request")
		}
		posts++
		json.NewDecoder(r.Body).Decode(&created)
		created.ID = "created-otp"
		w.WriteHeader(500)
		fmt.Fprint(w, `{"success":false}`)
	}))
	defer server.Close()
	m.cfg.API = API{Base: server.URL}
	ctx := context.Background()
	c := Credential{Account: "account-id", Token: "private-fixture"}
	if e := m.ensureOTP(ctx, c, inventory{}); e == nil {
		t.Fatal("expected lost reply")
	}
	if e := m.ensureOTP(ctx, c, inventory{}); Code(e) != "access_conflict" || posts != 1 {
		t.Fatal(Code(e), posts)
	}
	if e := m.ensureOTP(ctx, c, inventory{IDPs: []IdentityProvider{created}}); e != nil || m.state.OTP != created.ID || m.state.OTPPending {
		t.Fatal(Code(e))
	}
}
