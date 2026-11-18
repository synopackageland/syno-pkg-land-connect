package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"dsmapp/internal/apps"
	"dsmapp/internal/connector"
)

type appTargetsFixture struct {
	store          apps.Store
	beforeActivate func()
	fail           bool
}

func (a *appTargetsFixture) ActivePublic(context.Context) ([]AppRoute, error) {
	s, e := a.store.Load()
	if e != nil {
		return nil, e
	}
	out := []AppRoute{}
	for _, t := range apps.Targets(s) {
		if t.Kind == apps.Public && t.Scope.State == apps.Active {
			out = append(out, t)
		}
	}
	return out, nil
}
func (a *appTargetsFixture) AppExists(_ context.Context, pkg string) bool {
	state, err := a.store.Load()
	if err != nil {
		return true
	}
	if _, ok := state.Custom[pkg]; ok {
		return true
	}
	_, ok := state.Apps[pkg]
	return ok
}
func (a *appTargetsFixture) ActivatePublic(ctx context.Context, targets []AppRoute) error {
	if a.beforeActivate != nil {
		a.beforeActivate()
	}
	e := a.store.Update(ctx, func(s *apps.State) error {
		for _, target := range targets {
			app := s.Apps[target.Package]
			rev, e := s.Revision()
			if e != nil {
				return e
			}
			app.Rev = rev
			scope := target.Scope
			app.Public = &scope
			s.Apps[target.Package] = app
		}
		return nil
	})
	if e != nil {
		return e
	}
	if a.fail {
		return errors.New("listener failed")
	}
	return nil
}
func (a *appTargetsFixture) PausePublic(ctx context.Context, targets []AppRoute, reason string) error {
	return a.store.Update(ctx, func(s *apps.State) error {
		for _, target := range targets {
			app := s.Apps[target.Package]
			if app.Public == nil {
				continue
			}
			rev, e := s.Revision()
			if e != nil {
				return e
			}
			app.Rev = rev
			app.Public.State = apps.Paused
			app.Public.Reason = reason
			s.Apps[target.Package] = app
		}
		return nil
	})
}
func appTarget(pkg, host, socket string) AppRoute {
	return apps.Target{Package: pkg, Kind: apps.Public, Scope: apps.Scope{State: apps.Active, Origin: "https://" + host, Authority: host, Socket: socket, Binding: apps.Binding{Port: 7443, Summary: strings.Repeat("a", 64), Pin: apps.Pin{Mode: "S", SPKI: strings.Repeat("b", 64)}}}}
}
func installTargets(f *fixture) *appTargetsFixture {
	a := &appTargetsFixture{store: apps.Store{Dir: filepath.Join(f.dir, "apps")}}
	if e := os.MkdirAll(a.store.Dir, 0700); e != nil {
		f.t.Fatal(e)
	}
	f.tunnel.cfg.Apps = a
	return a
}
func TestAppBatchDNSBeforeDurableStateBeforeUnixIngressAndOneRestart(t *testing.T) {
	f := newFixture(t)
	f.ownDomain()
	a := installTargets(f)
	ctx := context.Background()
	old := f.serving()
	before := len(f.launcher.starts())
	a.beforeActivate = func() {
		if !old.Alive() {
			t.Fatal("tunnel stopped before persisting intent")
		}
		if !containsName(f.routed(), "one.example.com") || !containsName(f.routed(), "two.example.com") {
			t.Fatal("state preceded DNS")
		}
		if strings.Contains(f.config(), "one.example.com") {
			t.Fatal("ingress preceded state")
		}
	}
	targets := []AppRoute{appTarget("ExampleOne", "one.example.com", "one"), appTarget("ExampleTwo", "two.example.com", "two")}
	results, e := f.tunnel.PublishApps(ctx, targets)
	if e != nil || len(results) != 2 || results[0].Code != "" || results[1].Code != "" || !results[0].OK || !results[1].OK {
		t.Fatal(results, e)
	}
	f.waitState("connected")
	if len(f.launcher.starts()) != before+1 || old.Alive() {
		t.Fatal("batch did not restart exactly once")
	}
	config := f.config()
	for _, id := range []string{"one", "two"} {
		if !strings.Contains(config, "service: \"unix:"+apps.PinSocket(id)+"\"") {
			t.Fatal(config)
		}
	}
	if !strings.Contains(config, "service: \"unix:"+apps.PinSocket("dsm")+"\"") || strings.Contains(config, "noTLSVerify") || strings.Contains(config, "protocol: http2") {
		t.Fatal(config)
	}
	state, e := a.store.Load()
	if e != nil || len(state.Apps) != 2 {
		t.Fatal(state, e)
	}
	f.certKept()
}

func TestAppRouteLedgerReclaimsAnOrphanAndForgetsOwnership(t *testing.T) {
	f, cloud := recoveryFixture(t)
	a := installTargets(f)
	ctx := context.Background()
	if _, err := f.tunnel.PublishApps(ctx, []AppRoute{appTarget("ExampleOne", "one.example.com", "one")}); err != nil {
		t.Fatal(err)
	}
	d, err := f.tunnel.savedDomain()
	if err != nil || d.AppRoutes["ExampleOne"] != "one.example.com" {
		t.Fatal("package route ledger missing", d.AppRoutes, err)
	}
	// The package route ledger remains sufficient cleanup evidence even if the
	// older flat route inventory was lost before the App disappeared.
	d.Routed = slices.DeleteFunc(d.Routed, func(name string) bool { return name == "one.example.com" })
	if err = f.tunnel.saveRecord(d); err != nil {
		t.Fatal(err)
	}
	if err = a.store.Update(ctx, func(state *apps.State) error {
		delete(state.Apps, "ExampleOne")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = f.tunnel.ReconcileAppRoutes(ctx); err != nil {
		t.Fatal(err)
	}
	d, err = f.tunnel.savedDomain()
	if err != nil || containsName(d.Routed, "one.example.com") || d.AppRoutes["ExampleOne"] != "" {
		t.Fatal("orphan route retained", d, err)
	}
	c, err := readCert(f.tunnel.certFile())
	if err != nil {
		t.Fatal(err)
	}
	owned, err := f.tunnel.ownership(c.accountID)
	if err != nil || owned.DNS["one.example.com"].ID != "" {
		t.Fatal("orphan ownership retained", owned.DNS, err)
	}
	cloud.mu.Lock()
	defer cloud.mu.Unlock()
	if _, ok := cloud.records["one.example.com"]; ok {
		t.Fatal("orphan DNS retained", cloud.records, cloud.deletes)
	}
}

func TestOwnershipReceiptWithoutRouteLedgerIsReclaimed(t *testing.T) {
	f, cloud := recoveryFixture(t)
	a := installTargets(f)
	ctx := context.Background()
	if _, err := f.tunnel.PublishApps(ctx, []AppRoute{appTarget("ExampleOne", "one.example.com", "one")}); err != nil {
		t.Fatal(err)
	}
	d, err := f.tunnel.savedDomain()
	if err != nil {
		t.Fatal(err)
	}
	delete(d.AppRoutes, "ExampleOne")
	d.Routed = slices.DeleteFunc(d.Routed, func(name string) bool { return name == "one.example.com" })
	if err = f.tunnel.saveRecord(d); err != nil {
		t.Fatal(err)
	}
	if err = a.store.Update(ctx, func(state *apps.State) error {
		delete(state.Apps, "ExampleOne")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = f.tunnel.ReconcileAppRoutes(ctx); err != nil {
		t.Fatal(err)
	}
	signed, err := readCert(f.tunnel.certFile())
	if err != nil {
		t.Fatal(err)
	}
	owned, err := f.tunnel.ownership(signed.accountID)
	if err != nil || owned.DNS["one.example.com"].ID != "" {
		t.Fatal("ownership-only orphan retained", owned.DNS, err)
	}
	cloud.mu.Lock()
	defer cloud.mu.Unlock()
	if _, ok := cloud.records["one.example.com"]; ok {
		t.Fatal("ownership-only DNS retained", cloud.records, cloud.deletes)
	}
}
func TestAppBatchCollisionRefusesAllAndPartialDNSPersistsOnlySuccess(t *testing.T) {
	for _, partial := range []bool{false, true} {
		t.Run(map[bool]string{false: "collision", true: "partial"}[partial], func(t *testing.T) {
			f := newFixture(t)
			f.ownDomain()
			a := installTargets(f)
			before := len(f.launcher.starts())
			if !partial {
				f.taken = "two.example.com"
			} else {
				prior := f.launcher.onRun
				f.launcher.onRun = func(c Command) error {
					if strings.HasSuffix(argsOf(c), " two.example.com") {
						return errRoute
					}
					return prior(c)
				}
			}
			results, e := f.tunnel.PublishApps(context.Background(), []AppRoute{appTarget("One", "one.example.com", "one"), appTarget("Two", "two.example.com", "two")})
			state, se := a.store.Load()
			if se != nil {
				t.Fatal(se)
			}
			if !partial {
				if connector.ErrorCode(e) != "cloudflare_name_taken" || len(state.Apps) != 0 || len(f.launcher.starts()) != before || len(f.routed()) != 1 {
					t.Fatal(results, e, state)
				}
				return
			}
			if e != nil || len(results) != 2 || results[1].Code != "cloudflare_route_failed" || len(state.Apps) != 1 || state.Apps["One"].Public == nil || state.Apps["Two"].Public != nil {
				t.Fatal(results, e, state)
			}
		})
	}
}
func TestAppActivationFailurePausesDurablyWithoutStartingIngress(t *testing.T) {
	f := newFixture(t)
	f.ownDomain()
	a := installTargets(f)
	a.fail = true
	before := len(f.launcher.starts())
	_, e := f.tunnel.PublishApps(context.Background(), []AppRoute{appTarget("One", "one.example.com", "one")})
	s, se := a.store.Load()
	if e == nil || se != nil || s.Apps["One"].Public.State != apps.Paused || len(f.launcher.starts()) != before || strings.Contains(f.config(), "one.example.com") {
		t.Fatal(e, se, s)
	}
}
func TestDisconnectWaitsForDNSAndPreservesExistingRoutes(t *testing.T) {
	f := newFixture(t)
	f.ownDomain()
	installTargets(f)
	entered, release := make(chan struct{}), make(chan struct{})
	prior := f.launcher.onRun
	f.launcher.onRun = func(c Command) error {
		if strings.HasSuffix(argsOf(c), " one.example.com") {
			close(entered)
			<-release
		}
		return prior(c)
	}
	publish := make(chan error, 1)
	go func() {
		_, e := f.tunnel.PublishApps(context.Background(), []AppRoute{appTarget("One", "one.example.com", "one")})
		publish <- e
	}()
	<-entered
	disconnected := make(chan error, 1)
	go func() { disconnected <- f.tunnel.DisconnectAuthorization() }()
	select {
	case e := <-disconnected:
		t.Fatal("disconnect passed a DNS operation", e)
	case <-time.After(20 * time.Millisecond):
	}
	f.certKept()
	close(release)
	if e := <-publish; e != nil {
		t.Fatal(e)
	}
	if e := <-disconnected; e != nil {
		t.Fatal(e)
	}
	f.certGone()
	f.waitState("connected")
	if !strings.Contains(f.config(), "one.example.com") {
		t.Fatal("disconnect changed ingress")
	}
}
func TestAuthorizationReconnectAndDisable(t *testing.T) {
	f := newFixture(t)
	f.ownDomain()
	old := f.serving()
	before := len(f.launcher.starts())
	if e := f.tunnel.DisconnectAuthorization(); e != nil {
		t.Fatal(e)
	}
	f.certGone()
	authorize := f.signIn(goodCert())
	if e := f.tunnel.ReconnectAuthorization(context.Background()); e != nil {
		t.Fatal(e)
	}
	f.waitState("needs-login")
	if !old.Alive() {
		t.Fatal("reconnect stopped live tunnel")
	}
	close(authorize)
	f.waitState("connected")
	f.certKept()
	if len(f.launcher.starts()) != before+1 || !old.Alive() {
		t.Fatal("reconnect changed tunnel")
	}
	if e := f.tunnel.Disable(); e != nil {
		t.Fatal(e)
	}
	f.certGone()
}
func TestRevokedAuthorizationDoesNotAutomaticallyLogin(t *testing.T) {
	f := newFixture(t)
	f.ownDomain()
	installTargets(f)
	before := len(f.launcher.starts())
	if e := os.WriteFile(f.tunnel.certFile(), []byte("invalid authorization"), 0600); e != nil {
		t.Fatal(e)
	}
	_, e := f.tunnel.PublishApps(context.Background(), []AppRoute{appTarget("One", "one.example.com", "one")})
	if connector.ErrorCode(e) != "cloudflare_reconnect_required" || len(f.launcher.starts()) != before {
		t.Fatal(e)
	}
}
func TestDisconnectRefusesUnsettledCompensation(t *testing.T) {
	f := newFixture(t)
	f.ownDomain()
	f.tunnel.safe = &domainUpdate{}
	if e := f.tunnel.DisconnectAuthorization(); connector.ErrorCode(e) != "cloudflare_busy" {
		t.Fatal(e)
	}
	f.certKept()
	f.tunnel.safe = nil
}
func TestDSMRequiresApprovedProxy(t *testing.T) {
	f := newFixture(t)
	f.ownDomain()
	d, _ := f.tunnel.savedDomain()
	f.tunnel.cfg.DSMTarget = nil
	if _, e := f.tunnel.domainRun(d, 5000); connector.ErrorCode(e) != "pin_proxy_required" {
		t.Fatal(e)
	}
}

func TestDSMResolverPreservesFailureCause(t *testing.T) {
	for _, cause := range []error{
		context.Canceled, context.DeadlineExceeded,
		connector.Coded("apps_dsm_https_unavailable", "DSM HTTPS unavailable"),
		connector.Coded("pin_proxy_required", "DSM certificate changed"),
	} {
		t.Run(cause.Error(), func(t *testing.T) {
			f := newFixture(t)
			f.ownDomain()
			f.tunnel.cfg.DSMTarget = func(context.Context, string) (string, error) { return "", cause }
			d, _ := f.tunnel.savedDomain()
			if _, err := f.tunnel.domainRun(d, 5001); !errors.Is(err, cause) {
				t.Fatalf("DSM resolver failure replaced by certificate review: got %v, want %v", err, cause)
			}
		})
	}
}

func TestAuthorizationResponseMatchesPageAndNeverLeaksCertificate(t *testing.T) {
	f := newFixture(t)
	f.ownDomain()
	authorize := f.signIn(goodCert())
	defer close(authorize)
	result, e := f.tunnel.ConnectAuthorization(context.Background())
	if e != nil || !strings.HasPrefix(result.LoginURL, "https://dash.cloudflare.com/argotunnel?") {
		t.Fatal(result, e)
	}
	raw, e := json.Marshal(result)
	if e != nil || !strings.Contains(string(raw), `"loginUrl":`) || strings.Contains(string(raw), "secret-api-token") {
		t.Fatal("invalid authorization response")
	}
	if e := f.tunnel.DisconnectAuthorization(); e != nil {
		t.Fatal(e)
	}
}

func TestFirstAuthorizationThenFirstAppBatchCreatesNamedTunnel(t *testing.T) {
	f := newFixture(t)
	installTargets(f)
	authorize := f.signIn(goodCert())
	f.cloudflaredRuns("")
	result, e := f.tunnel.ConnectAuthorization(context.Background())
	if e != nil || result.LoginURL == "" {
		t.Fatal(result, e)
	}
	close(authorize)
	deadline := time.Now().Add(time.Second)
	for {
		f.tunnel.mu.Lock()
		done := f.tunnel.authDone
		f.tunnel.mu.Unlock()
		select {
		case <-done:
			goto ready
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("authorization did not finish")
		}
		time.Sleep(time.Millisecond)
	}
ready:
	results, e := f.tunnel.PublishApps(context.Background(), []AppRoute{appTarget("One", "one.example.com", "one")})
	if e != nil || len(results) != 1 || !results[0].OK {
		t.Fatal(results, e)
	}
	f.waitState("connected")
	f.certKept()
	label, err := f.tunnel.defaultName()
	if err != nil {
		t.Fatal(err)
	}
	if len(f.routed()) != 2 || !containsName(f.routed(), label+".example.com") || !strings.Contains(f.config(), "one.example.com") {
		t.Fatal(f.config(), f.routed())
	}
}

func TestSavedAppNameCannotReuseForeignDNS(t *testing.T) {
	f := newFixture(t)
	f.ownDomain()
	installTargets(f)
	d, err := f.tunnel.savedDomain()
	if err != nil {
		t.Fatal(err)
	}
	d.Routed = append(d.Routed, "one.example.com")
	if err = f.tunnel.saveRecord(d); err != nil {
		t.Fatal(err)
	}
	f.taken = "one.example.com"
	target := appTarget("One", "one.example.com", "one")
	if err = f.tunnel.CheckPublicApps(context.Background(), []AppRoute{target}); connector.ErrorCode(err) != "cloudflare_name_taken" {
		t.Fatalf("foreign DNS accepted: %v", err)
	}
	if _, err = f.tunnel.PublishApps(context.Background(), []AppRoute{target}); connector.ErrorCode(err) != "cloudflare_name_taken" {
		t.Fatalf("foreign DNS published: %v", err)
	}
}
