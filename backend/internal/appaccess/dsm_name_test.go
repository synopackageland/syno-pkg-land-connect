package appaccess

import (
	"context"
	"dsmapp/internal/apps"
	"dsmapp/internal/connector"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func renameDSM(t *testing.T, f *fixture, provider, host string) error {
	t.Helper()
	ctx := context.Background()
	in, err := f.api.Inspect(ctx, apps.InspectRequest{Package: "dsm", Scope: apps.Public, Provider: provider, Hostname: host})
	if err != nil {
		return err
	}
	_, err = f.api.SetPublic(ctx, apps.PublicRequest{Provider: provider, Enabled: true, Packages: []string{"dsm"}, Approvals: map[string]apps.Approved{"dsm": {Hostname: in.Hostname, Port: in.Port, Pin: *in.Pin, Token: in.Token, ReuseName: in.ReuseName}}})
	return err
}
func TestDSMRowRenameFreeLinkAndOldWorkerCleanup(t *testing.T) {
	f := newFixture(t, false)
	var deleted atomic.Int32
	withWorkers(t, f, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "DELETE" && strings.Contains(r.URL.Path, "/scripts/") {
			deleted.Add(1)
		}
		return false
	})
	ctx := context.Background()
	if _, err := f.api.(apps.WorkersDSMAPI).WorkersPublishDSM(ctx); err != nil {
		t.Fatal(err)
	}
	before, _ := f.store.Load()
	if err := renameDSM(t, f, apps.Workers, "my-dsm.example.workers.dev"); err != nil {
		t.Fatal(err)
	}
	state, _ := f.store.Load()
	if state.DSM.Public.Authority != "my-dsm.example.workers.dev" || state.DSM.Public.Socket == before.DSM.Public.Socket || deleted.Load() != 1 {
		t.Fatal("rename did not replace the old Worker", state.DSM, deleted.Load())
	}
	view, err := f.api.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var dsm *apps.App
	for i := range view.Apps {
		if view.Apps[i].DSM {
			dsm = &view.Apps[i]
		}
	}
	if dsm == nil || dsm.Package != "dsm" || dsm.Relay == nil || dsm.Relay.URL != "https://my-dsm.example.workers.dev" {
		t.Fatal("no DSM row", view.Apps)
	}
	if f.owner.WorkersDSMURL() != dsm.Relay.URL {
		t.Fatal("check address stayed stale")
	}
	// DSM opens directly through its pinned proxy without the Connect gate.
	if code, _, err := request(t, f.store.Dir, state.DSM.Public.Socket, state.DSM.Public.Authority); err != nil || code != 200 {
		t.Fatal("DSM got a sign-in gate", code, err)
	}
	if _, err := f.api.SetPublic(ctx, apps.PublicRequest{Provider: apps.Workers, Packages: []string{"dsm"}, Enabled: false}); err == nil {
		t.Fatal("implicit DSM removal accepted")
	}
}
func TestDSMRowRenameOwnDomainAndConfirmation(t *testing.T) {
	f := newFixture(t, false)
	ctx := context.Background()
	if err := f.owner.TrustDSMFirstUse(ctx); err != nil {
		t.Fatal(err)
	}
	tunnel, _, _ := realTunnel(t, f)
	f.owner.cfg.Public = tunnel
	if err := f.owner.BindTrustedDSM(ctx, apps.Public, "nas.example.com"); err != nil {
		t.Fatal(err)
	}
	before, _ := f.store.Load()
	preview, err := f.api.Inspect(ctx, apps.InspectRequest{Package: "dsm", Scope: apps.Public, Hostname: "my-dsm.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	unchanged, _ := f.store.Load()
	if unchanged.NextRev != before.NextRev {
		t.Fatal("inspection changed approval")
	}
	domain, _ := tunnel.AppDomain()
	if domain.Hostname != "nas.example.com" {
		t.Fatal("preview changed domain")
	}
	if _, err = f.api.SetPublic(ctx, apps.PublicRequest{Enabled: true, Packages: []string{"dsm"}, Approvals: map[string]apps.Approved{"dsm": {Hostname: preview.Hostname, Port: preview.Port, Pin: *preview.Pin, Token: "stale"}}}); err != apps.ErrConfirmationChanged {
		t.Fatal("stale confirmation accepted", err)
	}
	if err = renameDSM(t, f, "", "my-dsm.example.com"); err != nil {
		t.Fatal(err)
	}
	state, _ := f.store.Load()
	domain, _ = tunnel.AppDomain()
	if domain.Hostname != "my-dsm.example.com" || state.DSM.Origin != "https://my-dsm.example.com" || tunnel.Status().Hostname != "my-dsm.example.com" {
		t.Fatal("domain/status not synchronized", domain, state.DSM, tunnel.Status())
	}
	if code, _, err := request(t, f.store.Dir, state.DSM.Public.Socket, state.DSM.Public.Authority); err != nil || code != 200 {
		t.Fatal("DSM got a sign-in gate", code, err)
	}
	if err = renameDSM(t, f, "", "escape.other.example"); err == nil {
		t.Fatal("foreign suffix accepted")
	}
}
func TestDSMFreeLinkRenameUsesAppConflictContract(t *testing.T) {
	f := newFixture(t, false)
	withWorkers(t, f, func(w http.ResponseWriter, r *http.Request) bool {
		if strings.HasSuffix(r.URL.Path, "/taken/settings") {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"success":true,"result":{"tags":["foreign"]}}`))
			return true
		}
		return false
	})
	if _, err := f.api.(apps.WorkersDSMAPI).WorkersPublishDSM(context.Background()); err != nil {
		t.Fatal(err)
	}
	err := renameDSM(t, f, apps.Workers, "taken.example.workers.dev")
	if connector.ErrorCode(err) != "workers_name_conflict" {
		t.Fatal("missing conflict", err)
	}
}

func TestDSMWorkerRenameCleanupRetriesAfterLostDelete(t *testing.T) {
	f := newFixture(t, false)
	var fail atomic.Bool
	fail.Store(true)
	var deletes atomic.Int32
	withWorkers(t, f, func(w http.ResponseWriter, r *http.Request) bool {
		if r.Method == "DELETE" && strings.Contains(r.URL.Path, "/scripts/") {
			deletes.Add(1)
			if fail.Load() {
				w.WriteHeader(503)
				return true
			}
		}
		return false
	})
	ctx := context.Background()
	if _, err := f.api.(apps.WorkersDSMAPI).WorkersPublishDSM(ctx); err != nil {
		t.Fatal(err)
	}
	if err := renameDSM(t, f, apps.Workers, "renamed.example.workers.dev"); err == nil {
		t.Fatal("failed cleanup was hidden")
	}
	state, _ := f.store.Load()
	if state.DSM.Public.Authority != "renamed.example.workers.dev" {
		t.Fatal("new route was lost")
	}
	fail.Store(false)
	// A later reconciliation uses the durable inventory, without admitting a new rename.
	if err := f.owner.Remove(ctx, apps.Target{Kind: apps.Public}); err != nil {
		t.Fatal(err)
	}
	if deletes.Load() != 2 {
		t.Fatal("old Worker cleanup did not retry", deletes.Load())
	}
	if err := f.owner.Remove(ctx, apps.Target{Kind: apps.Public}); err != nil {
		t.Fatal(err)
	}
	if deletes.Load() != 2 {
		t.Fatal("cleaned Worker was deleted again")
	}
}
