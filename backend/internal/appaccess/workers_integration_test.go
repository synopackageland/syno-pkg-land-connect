package appaccess

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dsmapp/internal/apps"
	"dsmapp/internal/connector"
	"dsmapp/internal/relay"
	"dsmapp/internal/workers"
)

func withWorkers(t *testing.T, f *fixture, intercept ...func(http.ResponseWriter, *http.Request) bool) *workers.Manager {
	t.Helper()
	tags := map[string][]string{}
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, hook := range intercept {
			if hook(w, r) {
				return
			}
		}
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		var result any = map[string]any{}
		switch {
		case r.URL.Path == "/user/tokens/verify":
			result = map[string]string{"status": "active"}
		case strings.HasSuffix(r.URL.Path, "/workers/subdomain"):
			result = map[string]string{"subdomain": f.subdomain()}
		case strings.HasSuffix(r.URL.Path, "/settings"):
			key := strings.TrimSuffix(r.URL.Path, "/settings")
			v, ok := tags[key]
			if !ok {
				w.WriteHeader(404)
				fmt.Fprint(w, `{"success":false}`)
				return
			}
			result = map[string]any{"tags": v}
		case strings.Contains(r.URL.Path, "/scripts/"):
			switch r.Method {
			case "PUT":
				if err := r.ParseMultipartForm(2 << 20); err != nil {
					t.Error(err)
					return
				}
				var metadata struct {
					Tags []string `json:"tags"`
				}
				json.Unmarshal([]byte(r.FormValue("metadata")), &metadata)
				tags[r.URL.Path] = metadata.Tags
			case "DELETE":
				delete(tags, r.URL.Path)
			}
		default:
			result = map[string]string{"id": "0123456789abcdef0123456789abcdef"}
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": result})
	}))
	t.Cleanup(server.Close)
	m := &workers.Manager{Store: workers.Store{Dir: filepath.Join(f.store.Dir, "workers")}, API: workers.API{Base: server.URL, HTTP: server.Client(), Script: []byte("export default {}")}, Run: func(ctx context.Context, _ relay.Config) { <-ctx.Done() }}
	f.owner.cfg.Workers = m
	t.Cleanup(m.Close)
	// The manager directly: turning the Free link on through the engine also
	// publishes DSM, which TestFreeLinkPublishesDSM covers.
	if _, err := m.Connect(context.Background(), apps.WorkersConnectRequest{Token: "fixture-token", AccountID: "0123456789abcdef0123456789abcdef"}); err != nil {
		t.Fatal(err)
	}
	return m
}
func approveWorker(t *testing.T, f *fixture, pkg string) {
	t.Helper()
	ctx := context.Background()
	in, err := f.api.Inspect(ctx, apps.InspectRequest{Package: pkg, Scope: apps.Public, Provider: apps.Workers})
	if err != nil || !in.Ready || in.Pin == nil {
		t.Fatal("workers inspection unavailable", err)
	}
	approval := apps.Approved{Port: in.Port, Pin: *in.Pin, DSM: in.DSM, Token: in.Token}
	if _, err = f.api.SetPublic(ctx, apps.PublicRequest{Provider: apps.Workers, Packages: []string{pkg}, Enabled: true, Approvals: map[string]apps.Approved{pkg: approval}}); err != nil {
		t.Fatal(err)
	}
}
func TestWorkersApprovePinnedProjectionAndPublicModeExclusivity(t *testing.T) {
	f := newFixture(t, false)
	withWorkers(t, f)
	ctx := context.Background()
	approveWorker(t, f, "ExampleApp")
	s, err := f.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	sc := s.Apps["ExampleApp"].Public
	if sc == nil || sc.Provider != apps.Workers || s.DSM.Public == nil || s.DSM.Public.Provider != apps.Workers {
		t.Fatal("workers approval lost")
	}
	status, _, err := request(t, f.store.Dir, sc.Socket, sc.Authority)
	if err != nil || status != 200 {
		t.Fatal("approved pin socket unavailable", status, err)
	}
	raw, err := os.ReadFile(f.owner.cfg.ProjectionPath)
	if err != nil {
		t.Fatal(err)
	}
	var projection apps.Projection
	if json.Unmarshal(raw, &projection) != nil || projection.DSM.Origin != s.DSM.Origin || projection.Apps["ExampleApp"].Public.Origin != sc.Origin || strings.Contains(string(raw), "provider") {
		t.Fatal("projection schema changed")
	}
	assertLaunchMap(t, f, workerLaunchRoute(s, apps.Active))
	if _, err = f.api.Inspect(ctx, apps.InspectRequest{Package: "OtherApp", Scope: apps.Public}); connector.ErrorCode(err) != "apps_public_mode_conflict" {
		t.Fatal("mixed mode accepted", err)
	}
	if _, err = f.api.InspectDSM(ctx, apps.DSMInspectRequest{Scope: apps.Public}); connector.ErrorCode(err) != "apps_public_mode_conflict" {
		t.Fatal("mixed DSM mode accepted", err)
	}
	// E1 pauses a changed summary, closes the old socket, and never reopens it.
	gateway := filepath.Join(f.discovery.PackagesDir, "ExampleApp", "target", "sso", "gateway.json")
	raw, _ = os.ReadFile(gateway)
	raw = []byte(strings.Replace(string(raw), `"mode":"header"`, `"mode":"none"`, 1))
	if err = os.WriteFile(gateway, raw, 0644); err != nil {
		t.Fatal(err)
	}
	_ = f.owner.Check(ctx)
	s, err = f.store.Load()
	if err != nil || s.Apps["ExampleApp"].Public.State != apps.Paused {
		t.Fatal("E1 did not pause", err)
	}
	assertLaunchMap(t, f, workerLaunchRoute(s, apps.Paused))
	// A paused scope still owns the installation's public DSM mode. Reject
	// direct activation too, even when a caller bypasses inspection in the UI.
	if _, err = f.api.SetPublic(ctx, apps.PublicRequest{Packages: []string{"OtherApp"}, Enabled: true}); connector.ErrorCode(err) != "apps_public_mode_conflict" {
		t.Fatal("mixed activation accepted while workers paused", err)
	}
	status, _, err = request(t, f.store.Dir, sc.Socket, sc.Authority)
	if err == nil && status == 200 {
		t.Fatal("paused scope forwarded")
	}
	_ = f.owner.Check(ctx)
	s, _ = f.store.Load()
	if s.Apps["ExampleApp"].Public.State != apps.Paused {
		t.Fatal("pause resurrected")
	}
	in, err := f.api.Inspect(ctx, apps.InspectRequest{Package: "ExampleApp", Scope: apps.Public, Provider: apps.Workers})
	if err != nil || in.Pin == nil {
		t.Fatal("fresh workers inspection failed", err)
	}
	approval := apps.Approved{Port: in.Port, Pin: *in.Pin, DSM: in.DSM, Token: in.Token}
	if _, err = f.api.Reenable(ctx, apps.ReenableRequest{Package: "ExampleApp", Scope: apps.Public, Provider: apps.Workers, Approval: &approval}); err != nil {
		t.Fatal(err)
	}
	assertLaunchMap(t, f, workerLaunchRoute(s, apps.Active))
	if err = f.api.(apps.WorkersAPI).WorkersDisconnect(ctx); err != nil {
		t.Fatal(err)
	}
	s, err = f.store.Load()
	if err != nil || s.DSM.Public != nil || s.Apps["ExampleApp"].Public != nil {
		t.Fatal("Disconnect retained public approval", err)
	}
	assertLaunchMap(t, f)
	if _, err = f.api.Inspect(ctx, apps.InspectRequest{Package: "OtherApp", Scope: apps.Public}); err != nil {
		t.Fatal("mode not released", err)
	}
}
func TestDomainApprovalRefusesWorkersBeforeDeployment(t *testing.T) {
	f := newFixture(t, false)
	withWorkers(t, f)
	ctx := context.Background()
	approved := inspect(t, f, "ExampleApp", apps.Public)
	if _, err := f.api.SetPublic(ctx, apps.PublicRequest{Packages: []string{"ExampleApp"}, Enabled: true, Approvals: map[string]apps.Approved{"ExampleApp": approved}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.api.Inspect(ctx, apps.InspectRequest{Package: "OtherApp", Scope: apps.Public, Provider: apps.Workers}); connector.ErrorCode(err) != "apps_public_mode_conflict" {
		t.Fatal("domain/workers mixed", err)
	}
	if _, err := f.api.SetPublic(ctx, apps.PublicRequest{Provider: apps.Workers, Packages: []string{"OtherApp"}, Enabled: true}); connector.ErrorCode(err) != "apps_public_mode_conflict" {
		t.Fatal("mixed workers activation accepted", err)
	}
	if _, err := os.Stat(filepath.Join(f.store.Dir, "workers", "state.json")); err != nil {
		t.Fatal(err)
	}
}
func TestWorkersDSMOnlyApprovalAndRevocationWithdrawsAppRoutes(t *testing.T) {
	f := newFixture(t, false)
	m := withWorkers(t, f)
	ctx := context.Background()
	in, err := f.api.InspectDSM(ctx, apps.DSMInspectRequest{Scope: apps.Public, Provider: apps.Workers})
	if err != nil || in.DSM == nil {
		t.Fatal(err)
	}
	if _, err = f.api.SetDSM(ctx, apps.DSMRequest{Provider: apps.Workers, Scope: apps.Public, DSM: in.DSM, Token: in.Token}); err != nil {
		t.Fatal(err)
	}
	dsmState, err := f.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	assertLaunchMap(t, f, apps.LaunchRoute{Provider: apps.Workers, DSM: "https://" + dsmState.DSM.Public.Authority, Apps: map[string]apps.LaunchApp{}})
	if _, err = f.api.SetDSM(ctx, apps.DSMRequest{Scope: apps.Public, DSM: in.DSM, Token: in.Token}); connector.ErrorCode(err) != "apps_public_mode_conflict" {
		t.Fatal("mixed DSM-only activation accepted", err)
	}
	approveWorker(t, f, "ExampleApp")
	s, _ := f.store.Load()
	var dsm apps.PinBinding
	for _, b := range apps.PinBindings(s) {
		if b.Target == apps.TargetDSM {
			dsm = b
		}
	}
	if dsm.Provider != apps.Workers {
		t.Fatal("missing workers DSM")
	}
	state := apps.StorePinState{Store: f.store}
	if err = state.Suspend(ctx, dsm, "port-changed", false); err != nil {
		t.Fatal(err)
	}
	if err = f.owner.Remove(ctx, apps.Target{Kind: apps.Public}); err != nil {
		t.Fatal(err)
	}
	if len(f.owner.workerBindings()) != 0 {
		t.Fatal("App relay survived missing DSM approval")
	}
	_ = m
	select {
	case <-time.After(time.Millisecond):
	}
}

func TestClearPublicDSMRequiresAppWithdrawalAndReleasesMode(t *testing.T) {
	f := newFixture(t, false)
	withWorkers(t, f)
	ctx := context.Background()
	approveWorker(t, f, "ExampleApp")
	clear := f.api.(apps.DSMClearAPI)
	if err := clear.ClearDSM(ctx, apps.DSMInspectRequest{Scope: apps.Public, Provider: apps.Workers}); connector.ErrorCode(err) != "apps_public_apps_remaining" {
		t.Fatal("DSM cleared below App", err)
	}
	if err := f.api.Clear(ctx, apps.ScopedChange{Package: "ExampleApp", Kind: apps.Public}); err != nil {
		t.Fatal(err)
	}
	if err := clear.ClearDSM(ctx, apps.DSMInspectRequest{Scope: apps.Public, Provider: apps.Workers}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.api.InspectDSM(ctx, apps.DSMInspectRequest{Scope: apps.Public}); err != nil {
		t.Fatal("mode remained locked", err)
	}
}

func TestWorkerClearFailureStaysBlockedAndRetriesWithoutBackgroundCloudWrites(t *testing.T) {
	f := newFixture(t, false)
	m := withWorkers(t, f)
	ctx := context.Background()
	approveWorker(t, f, "ExampleApp")
	oldAPI := m.API
	var calls atomic.Int64
	failed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(503)
		fmt.Fprint(w, `{"success":false}`)
	}))
	defer failed.Close()
	m.API = workers.API{Base: failed.URL, HTTP: failed.Client()}
	if err := f.api.Clear(ctx, apps.ScopedChange{Package: "ExampleApp"}); err == nil {
		t.Fatal("failed cloud deletion acknowledged")
	}
	s, err := f.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	sc := s.Apps["ExampleApp"].Public
	if sc == nil || sc.State != apps.Paused || !sc.Cleanup || sc.Reason != "worker-delete-failed" {
		t.Fatal("cloud cleanup row lost")
	}
	before := calls.Load()
	_ = f.owner.Check(ctx)
	_ = f.owner.Check(ctx)
	s, _ = f.store.Load()
	if !s.Apps["ExampleApp"].Public.Cleanup || calls.Load() != before {
		t.Fatal("background changed cloud cleanup")
	}
	m.API = oldAPI
	if err = f.api.Clear(ctx, apps.ScopedChange{Package: "ExampleApp"}); err != nil {
		t.Fatal(err)
	}
	s, _ = f.store.Load()
	if s.Apps["ExampleApp"].Public != nil {
		t.Fatal("retry retained App")
	}
}

func TestRemovedWorkerCleanupRetriesAcrossRestartAndNeverDuringUpgrade(t *testing.T) {
	for _, retry := range []bool{false, true} {
		t.Run(fmt.Sprintf("retry-%t", retry), func(t *testing.T) {
			f := newFixture(t, false)
			ctx := context.Background()
			var fail atomic.Bool
			var deletes, successes atomic.Int64
			withWorkers(t, f, func(w http.ResponseWriter, r *http.Request) bool {
				if r.Method != http.MethodDelete {
					return false
				}
				state, err := f.store.Load()
				scope := state.Apps["ExampleApp"].Public
				if err != nil || scope == nil || scope.State != apps.Paused || scope.Reason != "removed" {
					t.Error("cloud deletion before durable removal")
				}
				deletes.Add(1)
				if fail.Load() {
					w.WriteHeader(503)
					fmt.Fprint(w, `{"success":false}`)
					return true
				}
				successes.Add(1)
				return false
			})
			approveWorker(t, f, "ExampleApp")
			pkgDir := filepath.Join(f.discovery.PackagesDir, "ExampleApp")
			// An extended upgrade gap must not remove either the Worker or approval.
			if err := os.Remove(filepath.Join(pkgDir, "INFO")); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(pkgDir, "target", "sso", "gateway.json")); err != nil {
				t.Fatal(err)
			}
			for range 3 {
				_ = f.owner.Check(ctx)
			}
			state, _ := f.store.Load()
			if deletes.Load() != 0 || state.Apps["ExampleApp"].Public.State != apps.Active {
				t.Fatal("upgrade deleted Worker")
			}
			writeMetadata(t, f.discovery.PackagesDir, "ExampleApp", f.app.port)
			_ = f.owner.Check(ctx)
			if err := os.RemoveAll(pkgDir); err != nil {
				t.Fatal(err)
			}
			_ = f.owner.Check(ctx)
			if deletes.Load() != 0 {
				t.Fatal("first missing-package poll deleted Worker")
			}
			fail.Store(retry)
			_ = f.owner.Check(ctx)
			state, _ = f.store.Load()
			scope := state.Apps["ExampleApp"].Public
			if scope.State != apps.Paused || scope.Reason != "removed" || scope.Cleanup != retry {
				t.Fatal("removed row or retry intent lost")
			}
			list, err := f.api.List(ctx)
			if err != nil {
				t.Fatal(err)
			}
			removed := false
			for _, app := range list.Apps {
				if app.Package == "ExampleApp" {
					removed = app.Removed
				}
			}
			if !removed {
				t.Fatal("removed row not exposed by API")
			}

			if retry {
				if deletes.Load() == 0 || successes.Load() != 0 {
					t.Fatal("deletion failure not exercised")
				}
				// Recreate the engine with no live pin proxy; only durable intent remains.
				cfg := f.owner.cfg
				f.owner.Close()
				f.owner = New(ctx, cfg)
				t.Cleanup(f.owner.Close)
				f.public.owner = f.owner
				f.private.engine = f.owner
				f.api = f.owner.Service()
				fail.Store(false)
				if err := f.owner.Check(ctx); err != nil {
					t.Fatal(err)
				}
			}
			after := deletes.Load()
			for range 2 {
				if err := f.owner.Check(ctx); err != nil {
					t.Fatal(err)
				}
			}
			if successes.Load() != 1 || deletes.Load() != after {
				t.Fatal("Worker not deleted exactly once")
			}
			state, _ = f.store.Load()
			if state.Apps["ExampleApp"].Public.Cleanup || state.DSM.Public == nil {
				t.Fatal("cleanup not finished or shared DSM lost")
			}
			writeMetadata(t, f.discovery.PackagesDir, "ExampleApp", f.app.port)
			if err := f.owner.Check(ctx); err != nil {
				t.Fatal(err)
			}
			state, _ = f.store.Load()
			if state.Apps["ExampleApp"].Public.State != apps.Paused || state.Apps["ExampleApp"].Public.Reason != "removed" {
				t.Fatal("reinstall reused approval")
			}
		})
	}
}

func (f *fixture) subdomain() string {
	if v, ok := f.workersSubdomain.Load().(string); ok {
		return v
	}
	return "example"
}

// Turning the Free link on publishes DSM on it at once: no certificate review,
// no sign-in of Connect's, and the launch map and status name its address.
func TestFreeLinkPublishesDSM(t *testing.T) {
	f := newFixture(t, false)
	m := withWorkers(t, f)
	if err := m.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var fresh atomic.Int32
	f.owner.cfg.WorkersAddress = func() { fresh.Add(1) }
	if url := f.owner.WorkersDSMURL(); url != "" {
		t.Fatal("Free link check address before DSM is public there:", url)
	}
	out, err := f.api.(apps.WorkersAPI).WorkersConnect(ctx, apps.WorkersConnectRequest{Token: "fixture-token", AccountID: "0123456789abcdef0123456789abcdef"})
	if err != nil || !out.Connected || out.DSMCode != "" || out.DSM == nil || out.DSM.Provider != apps.Workers {
		t.Fatalf("connect: %+v %v", out, err)
	}
	s, err := f.store.Load()
	if err != nil || s.DSM == nil || s.DSM.Public == nil || s.DSM.Public.Provider != apps.Workers || !strings.HasSuffix(s.DSM.Public.Authority, ".example.workers.dev") {
		t.Fatalf("DSM not public on the Free link: %+v %v", s.DSM, err)
	}
	if out.DSM.URL != s.DSM.Origin || f.owner.WorkersDSMURL() != s.DSM.Origin {
		t.Fatal("status or the Free link check names another DSM address", out.DSM.URL)
	}
	// A new address settles: its connection check reads Setting up, not Failed.
	if fresh.Load() != 1 {
		t.Fatal("new Free link address not announced to the check:", fresh.Load())
	}
	assertLaunchMap(t, f, apps.LaunchRoute{Provider: apps.Workers, DSM: s.DSM.Origin, Apps: map[string]apps.LaunchApp{}})
	status, _, err := request(t, f.store.Dir, s.DSM.Public.Socket, s.DSM.Public.Authority)
	if err != nil || status != 200 {
		t.Fatal("DSM socket not serving", status, err)
	}
	// The one-click Add DSM is idempotent once DSM is on the Free link.
	if again, err := f.api.(apps.WorkersDSMAPI).WorkersPublishDSM(ctx); err != nil || again.DSM == nil || again.DSM.URL != s.DSM.Origin {
		t.Fatalf("publish again: %+v %v", again, err)
	}
}

// A token saved for the NetBird server's Signal bridge keeps DSM private;
// the Free link must be explicitly enabled before it can publish DSM.
func TestTokenForTheNetBirdServerLeavesDSMPrivate(t *testing.T) {
	f := newFixture(t, false)
	m := withWorkers(t, f)
	if err := m.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	out, err := f.api.(apps.WorkersAPI).WorkersConnect(ctx, apps.WorkersConnectRequest{Token: "fixture-token", AccountID: "0123456789abcdef0123456789abcdef", NoDSM: true})
	if err != nil || !out.Token || out.DSM != nil || out.DSMCode != "" {
		t.Fatalf("connect: %+v %v", out, err)
	}
	if s, err := f.store.Load(); err != nil || s.DSM != nil && s.DSM.Public != nil {
		t.Fatalf("DSM published by a token for the NetBird server: %+v %v", s.DSM, err)
	}
	if _, err := f.api.(apps.WorkersDSMAPI).WorkersPublishDSM(ctx); apps.ErrorCode(err) != "workers_not_connected" {
		t.Fatalf("Add DSM must not enable a token-only Free link: %v", err)
	}
	if s, err := f.store.Load(); err != nil || s.DSM != nil && s.DSM.Public != nil {
		t.Fatalf("refused Add DSM published DSM: %+v %v", s.DSM, err)
	}
	if again, err := f.api.(apps.WorkersAPI).WorkersConnect(ctx, apps.WorkersConnectRequest{Token: "fixture-token", AccountID: "0123456789abcdef0123456789abcdef"}); err != nil || !again.Connected || again.DSM == nil || again.DSM.Provider != apps.Workers {
		t.Fatalf("explicit Free link enable: %+v %v", again, err)
	}
}

// DSM is public on one way at a time: a Free link turned on while the own
// domain holds public Apps stays on, leaves DSM where it is, and says why.
func TestFreeLinkLeavesDSMOnTheOwnDomain(t *testing.T) {
	f := newFixture(t, false)
	m := withWorkers(t, f)
	ctx := context.Background()
	approved := inspect(t, f, "ExampleApp", apps.Public)
	if _, err := f.api.SetPublic(ctx, apps.PublicRequest{Packages: []string{"ExampleApp"}, Enabled: true, Approvals: map[string]apps.Approved{"ExampleApp": approved}}); err != nil {
		t.Fatal(err)
	}
	before, _ := f.store.Load()
	if err := m.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	out, err := f.api.(apps.WorkersAPI).WorkersConnect(ctx, apps.WorkersConnectRequest{Token: "fixture-token", AccountID: "0123456789abcdef0123456789abcdef"})
	if err != nil || !out.Connected || out.DSMCode != "apps_dsm_public_elsewhere" {
		t.Fatalf("connect: %+v %v", out, err)
	}
	after, _ := f.store.Load()
	if before.DSM == nil || after.DSM == nil || !reflect.DeepEqual(before.DSM.Public, after.DSM.Public) {
		t.Fatal("the Free link took DSM from the own domain")
	}
	if out.DSM == nil || out.DSM.Provider != "cloudflare" {
		t.Fatalf("status does not name the own domain: %+v", out.DSM)
	}
	if _, err := f.api.(apps.WorkersDSMAPI).WorkersPublishDSM(ctx); connector.ErrorCode(err) != "apps_dsm_public_elsewhere" {
		t.Fatal("Add DSM took DSM from the own domain", err)
	}
}

// A workers.dev rename in Cloudflare moves DSM, every App address, the launch
// map and the projection to the new name on the next status read.
func TestWorkersStatusFollowsARenamedSubdomain(t *testing.T) {
	f := newFixture(t, false)
	withWorkers(t, f)
	ctx := context.Background()
	approveWorker(t, f, "ExampleApp")
	old, _ := f.store.Load()
	var fresh atomic.Int32
	f.owner.cfg.WorkersAddress = func() { fresh.Add(1) }
	f.workersSubdomain.Store("renamed")
	out, err := f.api.(apps.WorkersAPI).WorkersStatus(ctx)
	if err != nil || out.Subdomain != "renamed" || out.Code != "" {
		t.Fatalf("status: %+v %v", out, err)
	}
	s, err := f.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	sc := s.Apps["ExampleApp"].Public
	if sc.Authority != strings.Replace(old.Apps["ExampleApp"].Public.Authority, ".example.", ".renamed.", 1) || sc.Origin != "https://"+sc.Authority || sc.State != apps.Active {
		t.Fatalf("App address not moved: %+v", sc)
	}
	if s.DSM.Public.Authority != strings.Replace(old.DSM.Public.Authority, ".example.", ".renamed.", 1) || s.DSM.Origin != "https://"+s.DSM.Public.Authority || out.DSM == nil || out.DSM.URL != s.DSM.Origin {
		t.Fatalf("DSM address not moved: %+v %+v", s.DSM.Public, out.DSM)
	}
	// The renamed address settles like a new one; a second read moves nothing.
	if _, err := f.api.(apps.WorkersAPI).WorkersStatus(ctx); err != nil || fresh.Load() != 1 {
		t.Fatal("rename not announced once to the check:", fresh.Load(), err)
	}
	assertLaunchMap(t, f, workerLaunchRoute(s, apps.Active))
	status, _, err := request(t, f.store.Dir, sc.Socket, sc.Authority)
	if err != nil || status != 200 {
		t.Fatal("moved App socket not serving", status, err)
	}
	raw, _ := os.ReadFile(f.owner.cfg.ProjectionPath)
	if strings.Contains(string(raw), ".example.workers.dev") {
		t.Fatal("projection kept the old name")
	}
	// Inspection builds new addresses on the new name.
	in, err := f.api.Inspect(ctx, apps.InspectRequest{Package: "OtherApp", Scope: apps.Public, Provider: apps.Workers})
	if err != nil || !strings.HasSuffix(in.Hostname, ".renamed.workers.dev") {
		t.Fatalf("inspect: %q %v", in.Hostname, err)
	}
}
