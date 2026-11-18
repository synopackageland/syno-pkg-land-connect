package appaccess

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"dsmapp/internal/apps"
	"dsmapp/internal/connector"
)

func customBackend(t *testing.T) *httptest.Server {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "custom ok") }))
	t.Cleanup(s.Close)
	return s
}

func approveCustom(t *testing.T, f *fixture, id, provider string) {
	t.Helper()
	ctx := context.Background()
	in, err := f.api.Inspect(ctx, apps.InspectRequest{Package: id, Scope: apps.Public, Provider: provider})
	if err != nil || !in.Ready || in.Pin == nil || in.Pin.Mode != "C" || in.Custom == nil {
		t.Fatalf("custom inspection: %+v %v", in, err)
	}
	approval := apps.Approved{Port: in.Port, Pin: *in.Pin, DSM: in.DSM, Token: in.Token}
	if _, err = f.api.SetPublic(ctx, apps.PublicRequest{Provider: provider, Packages: []string{id}, Enabled: true, Approvals: map[string]apps.Approved{id: approval}}); err != nil {
		t.Fatal(err)
	}
}

func TestCustomAppWorkersApproveGateEditPauseRemove(t *testing.T) {
	f := newFixture(t, false)
	var deletes atomic.Int32
	m := withWorkers(t, f, func(_ http.ResponseWriter, r *http.Request) bool {
		if r.Method == "DELETE" && strings.Contains(r.URL.Path, "/scripts/media") {
			deletes.Add(1)
		}
		return false
	})
	f.owner.cfg.Login = func(context.Context, string, string, string) (string, string) { return "", "invalid_credentials" }
	backend := customBackend(t)
	ctx := context.Background()
	if _, err := f.api.(apps.CustomAPI).CustomAdd(ctx, apps.CustomRequest{Name: "Media", URL: "http://169.254.169.254"}); connector.ErrorCode(err) != "custom_target_refused" {
		t.Fatal("metadata address saved", err)
	}
	if _, err := f.api.(apps.CustomAPI).CustomAdd(ctx, apps.CustomRequest{Name: "Media", URL: backend.URL, AppSignIn: true}); connector.ErrorCode(err) != "custom_risk_unconfirmed" {
		t.Fatal("gate off saved without confirmation", err)
	}
	added, err := f.api.(apps.CustomAPI).CustomAdd(ctx, apps.CustomRequest{Name: "Media", URL: backend.URL})
	if err != nil || added.ID != "custom.media" {
		t.Fatal(added, err)
	}
	list, err := f.api.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var row *apps.App
	for i := range list.Apps {
		if list.Apps[i].Package == added.ID {
			row = &list.Apps[i]
		}
	}
	if row == nil || row.Custom == nil || row.Custom.Reachable == nil || !*row.Custom.Reachable || row.Name != "Media" {
		t.Fatalf("custom row: %+v", row)
	}
	if _, err = f.api.Inspect(ctx, apps.InspectRequest{Package: added.ID, Scope: apps.Private}); err == nil {
		t.Fatal("custom App offered a private scope")
	}
	approveCustom(t, f, added.ID, apps.Workers)
	s, _ := f.store.Load()
	sc := s.Apps[added.ID].Public
	if sc == nil || sc.State != apps.Active || sc.Authority != "media.example.workers.dev" || sc.Binding.Pin.Mode != "C" {
		t.Fatalf("scope %+v", sc)
	}
	// Gate on: the App answers only after DSM sign-in.
	status, _, err := request(t, f.store.Dir, sc.Socket, sc.Authority)
	if err != nil || status != http.StatusUnauthorized {
		t.Fatal("gate did not refuse", status, err)
	}
	// Gate off after confirmation: same socket and address, new proxy.
	if _, err = f.api.(apps.CustomAPI).CustomEdit(ctx, apps.CustomRequest{ID: added.ID, Name: "Media", URL: backend.URL, AppSignIn: true, AcceptRisk: true}); err != nil {
		t.Fatal(err)
	}
	status, body, err := request(t, f.store.Dir, sc.Socket, sc.Authority)
	if err != nil || status != 200 || body != "custom ok" {
		t.Fatal("edited App unreachable", status, body, err)
	}
	if err = f.api.(apps.CustomAPI).CustomPause(ctx, apps.CustomID{ID: added.ID}); err != nil {
		t.Fatal(err)
	}
	if status, _, err = request(t, f.store.Dir, sc.Socket, sc.Authority); err == nil && status == 200 {
		t.Fatal("paused App still forwarded")
	}
	s, _ = f.store.Load()
	if p := s.Apps[added.ID].Public; p == nil || p.State != apps.Paused || p.Reason != "admin_paused" {
		t.Fatalf("pause %+v", p)
	}
	if _, err = m.Authority(ctx, added.ID); err != nil {
		t.Fatal("pause dropped the Worker name", err)
	}
	if err = f.api.(apps.CustomAPI).CustomRemove(ctx, apps.CustomID{ID: added.ID}); err != nil {
		t.Fatal(err)
	}
	s, _ = f.store.Load()
	if _, ok := s.Apps[added.ID]; ok || s.Custom[added.ID].URL != "" {
		t.Fatal("removed App kept", s.Apps[added.ID])
	}
	if deletes.Load() != 1 {
		t.Fatal("Worker not deleted exactly once", deletes.Load())
	}
}

func TestCustomAppOwnDomainRemovalDeletesDNSAndRetries(t *testing.T) {
	f := newFixture(t, false)
	backend := customBackend(t)
	ctx := context.Background()
	added, err := f.api.(apps.CustomAPI).CustomAdd(ctx, apps.CustomRequest{Name: "Media", URL: backend.URL, AppSignIn: true, AcceptRisk: true})
	if err != nil {
		t.Fatal(err)
	}
	approveCustom(t, f, added.ID, "")
	s, _ := f.store.Load()
	sc := s.Apps[added.ID].Public
	if sc == nil || sc.Authority != "media.example.com" {
		t.Fatalf("%+v", sc)
	}
	if status, body, err := request(t, f.store.Dir, sc.Socket, sc.Authority); err != nil || status != 200 || body != "custom ok" {
		t.Fatal(status, body, err)
	}
	// Emulate a custom App published by a version before publication receipts
	// existed. The first close after upgrade must migrate the active scope.
	if err = f.store.Update(ctx, func(state *apps.State) error {
		def := state.Custom[added.ID]
		def.Published = nil
		state.Custom[added.ID] = def
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// Turning public access off intentionally keeps the DNS name. Deleting the
	// custom App afterwards must use its publication ledger, not a live scope.
	if _, err = f.api.SetPublic(ctx, apps.PublicRequest{Packages: []string{added.ID}, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	s, _ = f.store.Load()
	if s.Apps[added.ID].Public != nil || s.Custom[added.ID].Published == nil || s.Custom[added.ID].Published.Authority != sc.Authority {
		t.Fatal("public withdrawal lost cleanup evidence", s.Custom[added.ID].Published)
	}
	f.public.mu.Lock()
	f.public.deleteFail = true
	f.public.mu.Unlock()
	if err = f.api.(apps.CustomAPI).CustomRemove(ctx, apps.CustomID{ID: added.ID}); err == nil {
		t.Fatal("failed DNS cleanup reported success")
	}
	s, _ = f.store.Load()
	if p := s.Apps[added.ID].Public; p == nil || p.Reason != "removed" || !p.Cleanup {
		t.Fatal("removal intent lost", p)
	}
	if _, ok := s.Custom[added.ID]; ok {
		t.Fatal("definition kept")
	}
	f.public.mu.Lock()
	f.public.deleteFail = false
	f.public.mu.Unlock()
	_ = f.owner.Check(ctx)
	s, _ = f.store.Load()
	if _, ok := s.Apps[added.ID]; ok || f.public.deleteCalls < 2 {
		t.Fatal("safety check did not finish removal", s.Apps[added.ID], f.public.deleteCalls)
	}
}
