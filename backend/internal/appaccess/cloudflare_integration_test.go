package appaccess

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dsmapp/internal/apps"
	"dsmapp/internal/cloudflare"
	"dsmapp/internal/connector"
)

// Use the production DSM resolver, not just ApprovedDSMTarget: activation of
// a migrated publication calls it while the lifecycle owns the state lock.
func TestNativeEntryReenableWithProductionCloudflareResolver(t *testing.T) {
	f := newFixture(t, false)
	f.dsm = newPeer(t, "ExampleApp", f.dsm.cert.Load())
	_, _, dir := realTunnel(t, f, true)
	ctx := context.Background()
	a := inspect(t, f, "ExampleApp", apps.Public)
	if _, err := f.api.SetPublic(ctx, apps.PublicRequest{Packages: []string{"ExampleApp"}, Enabled: true, Approvals: map[string]apps.Approved{"ExampleApp": a}}); err != nil {
		t.Fatal(err)
	}
	old, _ := f.store.Load()
	// Model the paused publication from a legacy declaration requiring review.
	if err := f.store.Update(ctx, func(s *apps.State) error {
		s.Apps["ExampleApp"].Public.State = apps.Paused
		s.Apps["ExampleApp"].Public.Reason = "entry_changed"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"package": "ExampleApp", "native_ingress": true, "ingress_path": "/synopkgland/ExampleApp/", "mode": "header", "listen": map[string]int{}, "external": map[string]string{"connect": "optional", "origin_change": "none"}})
	if err := os.WriteFile(filepath.Join(f.discovery.PackagesDir, "ExampleApp", "target", "sso", "gateway.json"), raw, 0644); err != nil {
		t.Fatal(err)
	}
	a = inspect(t, f, "ExampleApp", apps.Public)
	if a.Port != f.dsm.port || a.DSM != nil {
		t.Fatal("not a native App approval with retained DSM trust", a)
	}
	if _, err := f.api.Reenable(ctx, apps.ReenableRequest{Package: "ExampleApp", Scope: apps.Public, Approval: &a}); err != nil {
		t.Fatal("unchanged DSM certificate blocked migrated App", err)
	}
	state, err := f.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	scope := state.Apps["ExampleApp"].Public
	if scope.State != apps.Active || scope.Reason != "" || scope.Binding.Port != f.dsm.port || apps.EntryPath(scope.Binding.Entry) != "/synopkgland/ExampleApp/" || scope.Authority != old.Apps["ExampleApp"].Public.Authority || state.DSM.Pin.CertSHA256 != old.DSM.Pin.CertSHA256 {
		t.Fatal("approval or entry lost", scope)
	}
	config, err := os.ReadFile(filepath.Join(dir, "domain/config.yml"))
	if err != nil || !strings.Contains(string(config), "unix:"+apps.PinSocket(scope.Socket)) {
		t.Fatal("migrated App route not installed", err)
	}
	if code, _, err := request(t, f.store.Dir, scope.Socket, scope.Authority, "/synopkgland/ExampleApp/"); err != nil || code != 200 {
		t.Fatal("migrated App not reachable", code, err)
	}
	// Ordinary pause/resume also passes through the same locked callback.
	if err := f.owner.Lifecycle().Close(ctx, "ExampleApp", apps.Public, "admin_paused"); err != nil {
		t.Fatal(err)
	}
	a = inspect(t, f, "ExampleApp", apps.Public)
	if _, err := f.api.Reenable(ctx, apps.ReenableRequest{Package: "ExampleApp", Scope: apps.Public, Approval: &a}); err != nil {
		t.Fatal("retry did not recover", err)
	}
	// A real certificate change must still stop activation and offer the
	// existing replacement-trust review, after which a fresh App retry works.
	if err := f.owner.Lifecycle().Close(ctx, "ExampleApp", apps.Public, "admin_paused"); err != nil {
		t.Fatal(err)
	}
	f.dsm.cert.Store(testCertificate(t))
	a = inspect(t, f, "ExampleApp", apps.Public)
	if _, err := f.api.Reenable(ctx, apps.ReenableRequest{Package: "ExampleApp", Scope: apps.Public, Approval: &a}); connector.ErrorCode(err) != "pin_proxy_required" {
		t.Fatal("real certificate change did not require review", err)
	}
	review, err := f.api.InspectDSM(ctx, apps.DSMInspectRequest{Scope: apps.Public})
	if err != nil || review.DSM == nil || review.DSM.Pin.Previous == nil {
		t.Fatal("no actionable replacement review", review, err)
	}
	if _, err := f.api.SetDSM(ctx, apps.DSMRequest{Scope: apps.Public, DSM: review.DSM, Token: review.Token}); err != nil {
		t.Fatal(err)
	}
	a = inspect(t, f, "ExampleApp", apps.Public)
	if _, err := f.api.Reenable(ctx, apps.ReenableRequest{Package: "ExampleApp", Scope: apps.Public, Approval: &a}); err != nil {
		t.Fatal("review/retry did not recover changed trust", err)
	}
}

type processFake struct{ alive atomic.Bool }

func (p *processFake) Alive() bool { return p.alive.Load() }
func (p *processFake) Stop() error { p.alive.Store(false); return nil }

type cloudCommands struct {
	mu     sync.Mutex
	starts int
	routes []string
	last   *processFake
}

func (c *cloudCommands) Start(context.Context, cloudflare.Command) (cloudflare.Process, error) {
	p := &processFake{}
	p.alive.Store(true)
	c.mu.Lock()
	c.starts++
	c.last = p
	c.mu.Unlock()
	return p, nil
}
func (c *cloudCommands) Run(_ context.Context, cmd cloudflare.Command) error {
	if len(cmd.Args) > 0 && strings.Contains(strings.Join(cmd.Args, " "), "route dns") {
		c.mu.Lock()
		c.routes = append(c.routes, cmd.Args[len(cmd.Args)-1])
		c.mu.Unlock()
	}
	return nil
}
func (c *cloudCommands) count() int { c.mu.Lock(); defer c.mu.Unlock(); return c.starts }

// realTunnel is the real Cloudflare owner on a saved own domain. Only its DNS
// HTTP API, process launcher and metrics endpoint are fake.
func realTunnel(t *testing.T, f *fixture, bindTrusted ...bool) (*cloudflare.Tunnel, *cloudCommands, string) {
	t.Helper()
	dir := filepath.Join(f.store.Dir, "cloudflare")
	const zone = "0123456789abcdef0123456789abcdef"
	const account = "fedcba9876543210fedcba9876543210"
	const tunnelID = "11111111-2222-4333-8444-555555555555"
	for _, path := range []string{filepath.Join(dir, "domain"), filepath.Join(dir, "home", ".cloudflared")} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, text string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(dir, "settings.json"), `{"enabled":true,"mode":"domain"}`)
	write(filepath.Join(dir, "domain", "domain.json"), `{"hostname":"nas.example.com","tunnelId":"`+tunnelID+`","routed":["nas.example.com"]}`)
	write(filepath.Join(dir, "domain", "credentials.json"), `{"AccountTag":"`+account+`","TunnelID":"`+tunnelID+`","TunnelSecret":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}`)
	// Synthetic local fixture authorization; never usable with Cloudflare.
	cert := pem.EncodeToMemory(&pem.Block{Type: "ARGO TUNNEL TOKEN", Bytes: []byte(`{"zoneID":"` + zone + `","accountID":"` + account + `","apiToken":"synthetic-test-token"}`)})
	write(filepath.Join(dir, "home", ".cloudflared", "cert.pem"), string(cert))
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer synthetic-test-token" {
			w.WriteHeader(401)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/dns_records") {
			_, _ = w.Write([]byte(`{"success":true,"result":[]}`))
			return
		}
		_, _ = fmt.Fprintf(w, `{"success":true,"result":{"id":%q,"name":"example.com","account":{"id":%q}}}`, zone, account)
	}))
	t.Cleanup(api.Close)
	metrics := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ready" {
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(metrics.Close)
	commands := &cloudCommands{}
	port := func(context.Context) (int, error) { return f.dsm.port, nil }
	resolve := cloudflare.ApprovedDSMTarget(f.store, port)
	if len(bindTrusted) > 0 && bindTrusted[0] {
		resolve = func(ctx context.Context, authority string) (string, error) {
			if err := f.owner.BindTrustedDSM(ctx, apps.Public, authority); err != nil {
				return "", err
			}
			return cloudflare.ApprovedDSMTarget(f.store, port)(ctx, authority)
		}
	}
	tunnel := cloudflare.NewTunnel(cloudflare.Config{Dir: dir, API: cloudflare.API{Base: api.URL}, Launcher: commands, Cloudflared: func(context.Context) (string, error) { return "/fixture/cloudflared", nil }, DSMPort: port, DSMTarget: resolve, MetricsAddr: func() (string, error) { return strings.TrimPrefix(metrics.URL, "http://"), nil }, Poll: time.Millisecond, RestartDelay: time.Millisecond})
	t.Cleanup(func() { _ = tunnel.Stop() })
	tunnel.SetAppTargets(f.owner)
	f.owner.cfg.Public = tunnel
	return tunnel, commands, dir
}

// Exercise the real batch owner and ingress renderer; proxy traffic uses real TLS.
func TestWiringRealCloudflareOwnerOneRestartAndPinnedDSM(t *testing.T) {
	f := newFixture(t, false)
	_, commands, dir := realTunnel(t, f)
	a := inspect(t, f, "ExampleApp", apps.Public)
	b := inspect(t, f, "OtherApp", apps.Public)
	result, err := f.api.SetPublic(context.Background(), apps.PublicRequest{Packages: []string{"ExampleApp", "OtherApp"}, Enabled: true, Approvals: map[string]apps.Approved{"ExampleApp": a, "OtherApp": b}})
	if err != nil || len(result.Results) != 2 || !result.Results[0].OK || !result.Results[1].OK {
		t.Fatal(result, err)
	}
	if commands.count() != 1 {
		t.Fatal("batch restarted cloudflared more than once", commands.count())
	}
	state, err := f.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "domain", "config.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, socket := range []string{state.Apps["ExampleApp"].Public.Socket, state.Apps["OtherApp"].Public.Socket, state.DSM.Public.Socket} {
		if !strings.Contains(string(raw), "unix:"+apps.PinSocket(socket)) {
			t.Fatal("ingress bypasses approved pin socket", string(raw))
		}
	}
	if code, _, err := request(t, f.store.Dir, state.DSM.Public.Socket, "nas.example.com"); err != nil || code != 200 {
		t.Fatal("DSM did not use its approved TLS target", code, err)
	}
	if _, err = f.api.SetPublic(context.Background(), apps.PublicRequest{Packages: []string{"ExampleApp", "OtherApp"}, Enabled: false}); err != nil {
		t.Fatal(err)
	}
	if commands.count() != 2 {
		t.Fatal("withdrawal restarted more than once", commands.count())
	}
}

// DSM-only remote access: no App approval ever wrote the DSM target, so the
// saved domain fails with pin_proxy_required until the administrator reviews
// DSM's certificate. Approval writes state.DSM and brings the route up.
func TestWiringDSMOnlyApprovalBringsUpCloudflareDomain(t *testing.T) {
	f := newFixture(t, false)
	tunnel, commands, dir := realTunnel(t, f)
	ctx := context.Background()
	if err := tunnel.Restore(ctx); err == nil {
		t.Fatal("domain served DSM without an approved target")
	}
	if s := tunnel.Status(); s.State != "failed" || s.Code != "pin_proxy_required" || s.Hostname != "nas.example.com" {
		t.Fatalf("restore hid why DSM is not served: %+v", s)
	}
	if commands.count() != 0 {
		t.Fatal("cloudflared started without an approved DSM target")
	}
	review, err := f.api.InspectDSM(ctx, apps.DSMInspectRequest{Scope: apps.Public})
	if err != nil || review.DSM == nil || review.Token == "" || review.DSM.Port != f.dsm.port || review.DSM.Pin.Mode != "S" || review.DSM.Pin.SPKI == "" || review.DSM.Pin.CertSHA256 == "" || review.DSM.Pin.Issuer == "" {
		t.Fatalf("review: %+v %v", review, err)
	}
	if state, _ := f.store.Load(); state.DSM != nil {
		t.Fatal("inspection wrote an approval")
	}
	// A revision written after the review was displayed needs a new review.
	if err = f.store.Update(ctx, func(s *apps.State) error { _, err := s.Revision(); return err }); err != nil {
		t.Fatal(err)
	}
	if _, err = f.api.SetDSM(ctx, apps.DSMRequest{Scope: apps.Public, DSM: review.DSM, Token: review.Token}); apps.ErrorCode(err) != "apps_confirmation_changed" {
		t.Fatal("stale review approved DSM", err)
	}
	if review, err = f.api.InspectDSM(ctx, apps.DSMInspectRequest{Scope: apps.Public}); err != nil || review.DSM == nil {
		t.Fatal(review, err)
	}
	changed := *review.DSM
	changed.Port++
	if _, err = f.api.SetDSM(ctx, apps.DSMRequest{Scope: apps.Public, DSM: &changed, Token: review.Token}); apps.ErrorCode(err) != "apps_confirmation_changed" {
		t.Fatal("approved a DSM target that was not displayed", err)
	}
	out, err := f.api.SetDSM(ctx, apps.DSMRequest{Scope: apps.Public, DSM: review.DSM, Token: review.Token})
	if err != nil || !out.Approved {
		t.Fatal(out, err)
	}
	state, err := f.store.Load()
	if err != nil || state.DSM == nil || state.DSM.Public == nil || state.DSM.Public.Authority != "nas.example.com" || state.DSM.Origin != "https://nas.example.com" || state.DSM.Private != nil || len(state.Apps) != 0 {
		t.Fatalf("approval: %+v %v", state.DSM, err)
	}
	if commands.count() != 1 {
		t.Fatal("approval did not start the domain exactly once", commands.count())
	}
	if s := tunnel.Status(); s.State == "failed" || s.Code != "" {
		t.Fatalf("route stayed failed: %+v", s)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "domain", "config.yml"))
	if err != nil || !strings.Contains(string(raw), "unix:"+apps.PinSocket(state.DSM.Public.Socket)) {
		t.Fatal("ingress bypasses approved DSM socket", string(raw), err)
	}
	if code, _, err := request(t, f.store.Dir, state.DSM.Public.Socket, "nas.example.com"); err != nil || code != 200 {
		t.Fatal("DSM did not use its approved TLS target", code, err)
	}
	if review, err = f.api.InspectDSM(ctx, apps.DSMInspectRequest{Scope: apps.Public}); err != nil || review.DSM != nil {
		t.Fatal("an approved, verifying DSM target still asks for review", review, err)
	}
	if _, err = f.api.SetDSM(ctx, apps.DSMRequest{Scope: apps.Public, DSM: &changed, Token: "replayed"}); apps.ErrorCode(err) != "apps_confirmation_changed" {
		t.Fatal("approval replayed", err)
	}
	// An App approved afterwards reuses the DSM target instead of asking again.
	if a := inspect(t, f, "ExampleApp", apps.Public); a.DSM != nil {
		t.Fatal("App approval asked for DSM again", a.DSM)
	}
}

// A DSM pin revoked while cloudflared serves ends the tunnel at once, with
// the review the UI offers, instead of an ingress that still names the
// revoked socket until the next restart. Approving the new key recovers.
func TestWiringRevokedDSMPinFailsRunningCloudflareDomain(t *testing.T) {
	f := newFixture(t, false)
	tunnel, commands, _ := realTunnel(t, f)
	ctx := context.Background()
	_ = tunnel.Restore(ctx)
	review, err := f.api.InspectDSM(ctx, apps.DSMInspectRequest{Scope: apps.Public})
	if err != nil || review.DSM == nil {
		t.Fatal(review, err)
	}
	if _, err = f.api.SetDSM(ctx, apps.DSMRequest{Scope: apps.Public, DSM: review.DSM, Token: review.Token}); err != nil {
		t.Fatal(err)
	}
	if commands.count() != 1 || tunnel.Status().State == "failed" {
		t.Fatal("approval did not start the domain", commands.count(), tunnel.Status())
	}
	state, err := f.store.Load()
	if err != nil || state.DSM == nil || state.DSM.Public == nil {
		t.Fatal(state.DSM, err)
	}
	// DSM moving its HTTPS port revokes the pinned endpoint on the next check.
	port := f.dsm.port
	f.dsm.port++
	_ = f.owner.Check(ctx)
	f.dsm.port = port
	if state, _ = f.store.Load(); state.DSM != nil && state.DSM.Public != nil {
		t.Fatal("changed DSM port kept its public endpoint")
	}
	if s := tunnel.Status(); s.State != "failed" || s.Code != "pin_proxy_required" || s.Hostname != "nas.example.com" {
		t.Fatalf("revocation not surfaced while running: %+v", s)
	}
	commands.mu.Lock()
	alive := commands.last.Alive()
	commands.mu.Unlock()
	if alive || commands.count() != 1 {
		t.Fatal("cloudflared kept the revoked ingress or restarted", alive, commands.count())
	}
	// A replacement certificate needs review; an unchanged certificate now
	// repairs the route without asking for a second trust decision.
	f.dsm.cert.Store(testCertificate(t))
	if review, err = f.api.InspectDSM(ctx, apps.DSMInspectRequest{Scope: apps.Public}); err != nil || review.DSM == nil {
		t.Fatal("changed DSM did not ask for review", review, err)
	}
	if _, err = f.api.SetDSM(ctx, apps.DSMRequest{Scope: apps.Public, DSM: review.DSM, Token: review.Token}); err != nil {
		t.Fatal(err)
	}
	if s := tunnel.Status(); s.State == "failed" || s.Code != "" || commands.count() != 2 {
		t.Fatalf("approval left the domain failed: %+v %d", s, commands.count())
	}
}

// The proxy owner can withdraw a stale route before or after the tunnel
// watcher's port poll. Reconciliation and subsequent restores must retain
// the real cause, rather than replacing it with a certificate-change warning.
func TestNamedDomainPortChangeSurvivesWithdrawalAndRestore(t *testing.T) {
	for _, bindTrusted := range []bool{false, true} {
		t.Run(fmt.Sprintf("bind-trusted-%v", bindTrusted), func(t *testing.T) {
			ctx := context.Background()
			f := newFixture(t, false)
			tunnel, commands, _ := realTunnel(t, f, bindTrusted)
			_ = tunnel.Restore(ctx)
			review, err := f.api.InspectDSM(ctx, apps.DSMInspectRequest{Scope: apps.Public})
			if err != nil || review.DSM == nil {
				t.Fatal(review, err)
			}
			if _, err = f.api.SetDSM(ctx, apps.DSMRequest{Scope: apps.Public, DSM: review.DSM, Token: review.Token}); err != nil {
				t.Fatal(err)
			}
			original := f.dsm.port
			for _, next := range []int{original + 1, original - 1} {
				state, err := f.store.Load()
				if err != nil || state.DSM == nil {
					t.Fatal(state.DSM, err)
				}
				from := state.DSM.Port
				f.dsm.port = next
				_ = f.owner.Check(ctx)
				if err := tunnel.ReconcileAppRoutes(ctx); connector.ErrorCode(err) != "pin_proxy_required" {
					t.Fatal("reconciliation admitted stale DSM trust", err)
				}
				assertChanged := func() {
					t.Helper()
					s := tunnel.Status()
					if s.State != "failed" || s.Code != "pin_proxy_required" || s.Hostname != "nas.example.com" || s.DSMPortChange == nil || s.DSMPortChange.From != from || s.DSMPortChange.To != next || len(s.Endpoints) != 0 {
						t.Fatalf("port cause lost after withdrawal/restore: %+v", s)
					}
				}
				assertChanged()
				before := commands.count()
				for i := 0; i < 2; i++ {
					if err := tunnel.Restore(ctx); connector.ErrorCode(err) != "pin_proxy_required" {
						t.Fatal("restore admitted stale DSM trust", err)
					}
					assertChanged()
				}
				if commands.count() != before {
					t.Fatal("withdrawal restarted an unapproved DSM route")
				}
				// This fixture's listener remains at original. Restoring the
				// original detection repairs the route with unchanged trust.
				f.dsm.port = original
				if review, err := f.api.InspectDSM(ctx, apps.DSMInspectRequest{Scope: apps.Public}); err != nil || review.DSM != nil {
					t.Fatal("unchanged trust did not repair the route", review, err)
				}
				if s := tunnel.Status(); s.State == "failed" || s.DSMPortChange != nil {
					t.Fatal("recovery retained stale port cause", s)
				}
			}
		})
	}
}

func TestUpgradeDomainWithNoPinRecoversOnAdminReconnect(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, false)
	tunnel, commands, _ := realTunnel(t, f, true)
	if err := tunnel.Restore(ctx); connector.ErrorCode(err) != "pin_proxy_required" {
		t.Fatal("missing-pin upgrade fixture did not fail closed", err)
	}
	if state, _ := f.store.Load(); state.DSM != nil {
		t.Fatal("restore or page status created trust")
	}
	// The same explicit admin request calls TrustDSMFirstUse before Start in
	// managedSelection. Both sides here are real, with a fake cloud API/launcher.
	if err := f.owner.TrustDSMFirstUse(ctx); err != nil {
		t.Fatal(err)
	}
	if err := tunnel.Start(ctx, cloudflare.Request{Mode: "domain"}); err != nil {
		t.Fatal(err)
	}
	state, err := f.store.Load()
	if err != nil || state.DSM == nil || state.DSM.Public == nil || state.DSM.Pin.CertSHA256 == "" {
		t.Fatal("reconnect left no trusted target", state.DSM, err)
	}
	if got := tunnel.Status(); got.State == "failed" || got.Code != "" || commands.count() != 1 {
		t.Fatal("reconnect stayed stuck", got, commands.count())
	}
	if code, _, err := request(t, f.store.Dir, state.DSM.Public.Socket, "nas.example.com"); err != nil || code != 200 {
		t.Fatal("reconnect did not serve the trusted DSM", code, err)
	}
}

func TestDSMOnlyOwnDomainRestartsFromRetainedAppData(t *testing.T) {
	f := newFixture(t, false)
	ctx := context.Background()
	if err := f.owner.TrustDSMFirstUse(ctx); err != nil {
		t.Fatal(err)
	}
	tunnel, _, _ := realTunnel(t, f, true)
	if err := tunnel.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	original, err := f.store.Load()
	if err != nil || original.DSM == nil || original.DSM.Public == nil || len(original.Apps) != 0 {
		t.Fatal("not DSM-only", err)
	}
	for _, generation := range []string{"stop-start", "upgrade", "boot"} {
		t.Run(generation, func(t *testing.T) {
			if err := tunnel.Stop(); err != nil {
				t.Fatal(err)
			}
			f.owner.Close()
			cfg := f.owner.cfg
			cfg.Registry = nil
			f.owner = New(ctx, cfg)
			t.Cleanup(f.owner.Close)
			f.private.engine = f.owner
			var commands *cloudCommands
			var dir string
			tunnel, commands, dir = realTunnel(t, f, true)
			// This is the production startup order, before accepting mutations.
			if err := tunnel.Restore(ctx); err != nil {
				t.Fatal(err)
			}
			if err := f.owner.Lifecycle().Recover(ctx); err != nil {
				t.Fatal(err)
			}
			state, err := f.store.Load()
			if err != nil {
				t.Fatal(err)
			}
			ep := state.DSM.Public
			if commands.count() == 0 || !f.owner.allowed[ep.Socket] || ep.Socket != original.DSM.Public.Socket {
				t.Fatal("lost connector or retained approval")
			}
			raw, err := os.ReadFile(filepath.Join(dir, "domain/config.yml"))
			if err != nil || !strings.Contains(string(raw), "unix:"+apps.PinSocket(ep.Socket)) {
				t.Fatal("ingress lost DSM", err)
			}
			if code, _, err := request(t, f.store.Dir, ep.Socket, ep.Authority); err != nil || code != 200 {
				t.Fatal("restored DSM route", code, err)
			}
		})
	}
}
