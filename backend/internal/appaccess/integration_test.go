package appaccess

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"dsmapp/internal/ipctest"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dsmapp/internal/apps"
	"dsmapp/internal/cloudflare"
	"dsmapp/internal/provider"
	"dsmapp/internal/publicname"
)

func testCertificate(t *testing.T) *tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(time.Now().UnixNano()), Subject: pkix.Name{CommonName: "nas.example.com"}, DNSNames: []string{"nas.example.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
}

type peer struct {
	server   *httptest.Server
	port     int
	cert     atomic.Pointer[tls.Certificate]
	requests atomic.Int64
}

func newPeer(t *testing.T, pkg string, cert *tls.Certificate) *peer {
	t.Helper()
	p := &peer{}
	p.cert.Store(cert)
	p.server = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_spl/ping" || r.URL.Path == "/synopkgland/"+pkg+"/_spl/ping" {
			raw, _ := json.Marshal(map[string]string{"package": pkg})
			_, _ = w.Write(raw)
			return
		}
		p.requests.Add(1)
		_, _ = io.WriteString(w, r.Host)
	}))
	p.server.TLS = &tls.Config{GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
		return &tls.Config{Certificates: []tls.Certificate{*p.cert.Load()}, MinVersion: tls.VersionTLS12}, nil
	}}
	p.server.StartTLS()
	t.Cleanup(p.server.Close)
	u, _ := url.Parse(p.server.URL)
	p.port, _ = strconv.Atoi(u.Port())
	return p
}

type privateFake struct {
	mu      sync.Mutex
	engine  *Engine
	network provider.AppNetwork
	routes  []provider.PortMapping
	calls   int
	fail    bool
}

func (p *privateFake) AppAccessNetwork(context.Context) (provider.AppNetwork, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.network, nil
}
func (p *privateFake) RefreshApps(ctx context.Context) error {
	routes, err := p.engine.PrivateMappings(ctx)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.fail {
		return apps.ErrCleanup
	}
	p.routes = routes
	return nil
}
func (p *privateFake) count() int { p.mu.Lock(); defer p.mu.Unlock(); return len(p.routes) }

type publicFake struct {
	deleteCalls    int
	deleteFail     bool
	mu             sync.Mutex
	owner          cloudflare.AppTargets
	routes         []apps.Target
	restarts       int
	connected      bool
	fail           bool
	dnsFail        string
	authCalls      int
	beforeActivate func()
}

func (p *publicFake) CheckPublicApps(context.Context, []apps.Target) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.connected {
		return apps.ErrUnavailable
	}
	return nil
}

func (p *publicFake) DefaultAppHostname(ctx context.Context, pkg string, occupied []string) (string, error) {
	label, err := publicname.Choose(ctx, publicname.ShortName(pkg), publicname.NASName(), func(label string) (bool, error) {
		for _, host := range occupied {
			if host == label+".example.com" {
				return false, nil
			}
		}
		return true, nil
	})
	return label + ".example.com", err
}
func (p *publicFake) DeleteAppRoute(context.Context, string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.deleteCalls++
	if p.deleteFail {
		return apps.ErrCleanup
	}
	return nil
}

func (p *publicFake) AppDomain() (cloudflare.AppDomain, error) {
	return cloudflare.AppDomain{Hostname: "nas.example.com", Zone: "example.com", Enabled: true}, nil
}
func (p *publicFake) Authorization(context.Context) (cloudflare.Authorization, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.connected {
		return cloudflare.Authorization{}, apps.ErrUnavailable
	}
	return cloudflare.Authorization{Connected: true, Zone: "example.com"}, nil
}
func (p *publicFake) ConnectAuthorization(context.Context) (cloudflare.AuthorizationResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.authCalls++
	p.connected = true
	return cloudflare.AuthorizationResult{LoginURL: "https://dash.cloudflare.com/argotunnel"}, nil
}
func (p *publicFake) DisconnectAuthorization() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.connected = false
	return nil
}
func (p *publicFake) SetPublicApps(ctx context.Context, targets []apps.Target) (cloudflare.PublicResult, error) {
	p.mu.Lock()
	connected, fail, dnsFail := p.connected, p.fail, p.dnsFail
	p.mu.Unlock()
	if !connected {
		return cloudflare.PublicResult{}, apps.ErrUnavailable
	}
	out := cloudflare.PublicResult{Results: []cloudflare.RouteResult{}}
	successful := []apps.Target{}
	for _, target := range targets {
		result := cloudflare.RouteResult{Package: target.Package}
		if target.Package == dnsFail {
			result.Code = "dns-error"
		} else {
			successful = append(successful, target)
		}
		out.Results = append(out.Results, result)
	}
	if len(successful) == 0 {
		return out, nil
	}
	p.mu.Lock()
	before := p.beforeActivate
	p.mu.Unlock()
	if before != nil {
		before()
	}
	if err := p.owner.ActivatePublic(ctx, successful); err != nil {
		return out, err
	}
	if fail {
		_ = p.owner.PausePublic(ctx, successful, "route-failed")
		return out, apps.ErrUnavailable
	}
	if err := p.ReconcileAppRoutes(ctx); err != nil {
		return out, err
	}
	for i := range out.Results {
		out.Results[i].OK = out.Results[i].Code == ""
	}
	return out, nil
}
func (p *publicFake) ReconcileAppRoutes(ctx context.Context) error {
	routes, err := p.owner.ActivePublic(ctx)
	if err != nil {
		return err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail {
		return apps.ErrCleanup
	}
	p.restarts++
	p.routes = routes
	return nil
}
func (p *publicFake) counts() (int, int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.routes), p.restarts
}

type fixture struct {
	owner           *Engine
	service         *apps.Service
	api             apps.API
	store           apps.Store
	discovery       apps.Discovery
	private         *privateFake
	public          *publicFake
	app, other, dsm *peer
	roots           *x509.CertPool
	// workersSubdomain is the workers.dev name the Cloudflare fake reports.
	workersSubdomain atomic.Value
}

func newFixture(t *testing.T, webPKI bool) *fixture {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "app-wiring-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	f := &fixture{store: apps.Store{Dir: dir}, roots: x509.NewCertPool()}
	cert := testCertificate(t)
	if webPKI {
		f.roots.AddCert(cert.Leaf)
	}
	f.app = newPeer(t, "ExampleApp", cert)
	f.other = newPeer(t, "OtherApp", cert)
	f.dsm = newPeer(t, "DSM", cert)
	root := t.TempDir()
	f.discovery = apps.Discovery{PackagesDir: root, DetectPorts: func(context.Context) (int, int, error) { return 5000, f.dsm.port, nil }}
	for pkg, port := range map[string]int{"ExampleApp": f.app.port, "OtherApp": f.other.port} {
		writeMetadata(t, root, pkg, port)
	}
	f.private = &privateFake{network: provider.AppNetwork{Host: "nas.example.ts.net", Available: true}}
	f.public = &publicFake{connected: true}
	f.owner = New(context.Background(), Config{Store: f.store, Discovery: f.discovery, ProjectionPath: filepath.Join(dir, "spl", "external.json"), DSMPort: func(context.Context) (int, error) { return f.dsm.port, nil }, Verifier: apps.NewVerifier(apps.Options{Roots: f.roots, ReferencePort: func() (int, error) { return f.dsm.port, nil }}), Private: f.private, Public: f.public})
	f.private.engine = f.owner
	f.public.owner = f.owner
	t.Cleanup(f.owner.Close)
	ln, err := ipctest.Listen(t, ipctest.Path(t, filepath.Join(dir, "control.sock")))
	if err != nil {
		t.Fatal(err)
	}
	f.service = f.owner.Service()
	srv := &http.Server{Handler: apps.Handler(f.service)}
	go srv.Serve(ln)
	t.Cleanup(func() { _ = srv.Close() })
	f.api = apps.Client{Socket: ipctest.Path(t, filepath.Join(dir, "control.sock"))}
	return f
}
func writeMetadata(t *testing.T, root, pkg string, port int) {
	t.Helper()
	dir := filepath.Join(root, pkg, "target", "sso")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"package": pkg, "native_ingress": true, "mode": "header", "listen": map[string]int{"https": port}, "external": map[string]string{"connect": "optional", "origin_change": "none"}})
	if err := os.WriteFile(filepath.Join(dir, "gateway.json"), raw, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, pkg, "INFO"), []byte("displayname=\"Example App\"\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestAppExistsDistinguishesUpgradeFromUninstall(t *testing.T) {
	f := newFixture(t, false)
	ctx := context.Background()
	approved := inspect(t, f, "ExampleApp", apps.Public)
	if _, err := f.api.SetPublic(ctx, apps.PublicRequest{Packages: []string{"ExampleApp"}, Enabled: true, Approvals: map[string]apps.Approved{"ExampleApp": approved}}); err != nil {
		t.Fatal(err)
	}
	pkgDir := filepath.Join(f.discovery.PackagesDir, "ExampleApp")
	if err := os.RemoveAll(filepath.Join(pkgDir, "target")); err != nil {
		t.Fatal(err)
	}
	if !f.owner.AppExists(ctx, "ExampleApp") {
		t.Fatal("upgrade metadata gap treated as an uninstall")
	}
	if err := os.RemoveAll(pkgDir); err != nil {
		t.Fatal(err)
	}
	if f.owner.AppExists(ctx, "ExampleApp") {
		t.Fatal("removed package retained as an existing App")
	}
}

func inspect(t *testing.T, f *fixture, pkg, kind string) apps.Approved {
	t.Helper()
	out, err := f.api.Inspect(context.Background(), apps.InspectRequest{Package: pkg, Scope: kind})
	if err != nil || !out.Ready || out.Pin == nil {
		_, directErr := f.owner.Service().Inspect(context.Background(), apps.InspectRequest{Package: pkg, Scope: kind})
		t.Fatalf("inspection: %+v %v (direct: %v)", out, err, directErr)
	}
	return apps.Approved{Port: out.Port, Pin: *out.Pin, DSM: out.DSM, Token: out.Token}
}
func request(t *testing.T, dir, socket, authority string, paths ...string) (int, string, error) {
	t.Helper()
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", apps.PinSocket(socket))
	}}
	defer tr.CloseIdleConnections()
	client := &http.Client{Transport: tr, Timeout: 3 * time.Second}
	path := "/data"
	if len(paths) > 0 {
		path = paths[0]
	}
	req, _ := http.NewRequest("GET", "http://proxy"+path, nil)
	req.Host = authority
	req.Header.Set("X-Forwarded-Host", "attacker.example.com")
	resp, err := client.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw), err
}

func TestWiringDiscoverInspectApprovePrivatePublicPauseRevoke(t *testing.T) {
	for _, webPKI := range []bool{false, true} {
		name := "S"
		if webPKI {
			name = "W"
		}
		t.Run(name, func(t *testing.T) {
			f := newFixture(t, webPKI)
			ctx := context.Background()
			list, err := f.api.List(ctx)
			if err != nil || len(list.Apps) != 2 || !list.Private.Available || !list.Public.Available {
				t.Fatal(list, err)
			}
			approved := inspect(t, f, "ExampleApp", apps.Private)
			if approved.Pin.Mode != name || approved.DSM == nil {
				t.Fatal("first App must show DSM approval", approved)
			}
			if _, err = os.Stat(filepath.Join(f.store.Dir, "apps-state.json")); !os.IsNotExist(err) {
				t.Fatal("inspect persisted intent")
			}
			if _, err = f.api.SetPrivate(ctx, apps.PrivateRequest{Package: "ExampleApp", Enabled: true, Approval: &approved}); err != nil {
				t.Fatal(err)
			}
			state, err := f.store.Load()
			if err != nil || state.DSM == nil || state.DSM.Private == nil || f.private.count() != 2 {
				t.Fatal("App and DSM were not routed together", state, err)
			}
			private := state.Apps["ExampleApp"].Private
			code, body, err := request(t, f.store.Dir, private.Socket, "ignored.example.com")
			if err != nil || code != 200 || body != private.Authority {
				t.Fatal("private proxy Host/TLS path", code, body, err)
			}
			code, body, err = request(t, f.store.Dir, state.DSM.Private.Socket, "ignored.example.com")
			if err != nil || code != 200 || body != state.DSM.Private.Authority {
				t.Fatal("DSM proxy", code, body, err)
			}
			public := inspect(t, f, "ExampleApp", apps.Public)
			other := inspect(t, f, "OtherApp", apps.Public)
			_, before := f.public.counts()
			result, err := f.api.SetPublic(ctx, apps.PublicRequest{Packages: []string{"ExampleApp", "OtherApp"}, Enabled: true, Approvals: map[string]apps.Approved{"ExampleApp": public, "OtherApp": other}})
			if err != nil || len(result.Results) != 2 || !result.Results[0].OK || !result.Results[1].OK {
				t.Fatal(result, err)
			}
			n, after := f.public.counts()
			if n != 2 || after-before != 1 {
				t.Fatal("public batch must restart once", n, before, after)
			}
			state, err = f.store.Load()
			if err != nil || state.DSM.Public == nil {
				t.Fatal("public DSM approval missing", state, err)
			}
			scope := state.Apps["ExampleApp"].Public
			code, body, err = request(t, f.store.Dir, scope.Socket, scope.Authority)
			if err != nil || code != 200 || body != scope.Authority {
				t.Fatal(code, body, err)
			}
			requests := f.app.requests.Load()
			code, _, err = request(t, f.store.Dir, scope.Socket, "attacker.example.com")
			if err != nil || code != 421 || f.app.requests.Load() != requests {
				t.Fatal("foreign Host reached upstream", code, err)
			}
			if err = f.owner.Lifecycle().Close(ctx, "ExampleApp", apps.Private, "certificate-changed"); err != nil {
				t.Fatal(err)
			}
			if code, _, err = request(t, f.store.Dir, private.Socket, private.Authority); err == nil && code == 200 {
				t.Fatal("paused route still forwards")
			}
			approved = inspect(t, f, "ExampleApp", apps.Private)
			if _, err = f.api.Reenable(ctx, apps.ReenableRequest{Package: "ExampleApp", Scope: apps.Private, Approval: &approved}); err != nil {
				t.Fatal(err)
			}
			_, before = f.public.counts()
			if _, err = f.api.SetPublic(ctx, apps.PublicRequest{Packages: []string{"ExampleApp", "OtherApp"}, Enabled: false}); err != nil {
				t.Fatal(err)
			}
			n, after = f.public.counts()
			if n != 0 || after-before != 1 {
				t.Fatal("batch withdrawal must restart once", n, before, after)
			}
			_, before = f.public.counts()
			if err = f.owner.Check(ctx); err != nil {
				t.Fatal(err)
			}
			_, after = f.public.counts()
			if after != before {
				t.Fatal("unchanged safety check restarted provider")
			}
			if err = f.api.Clear(ctx, apps.ScopedChange{Package: "ExampleApp"}); err != nil {
				t.Fatal(err)
			}
			state, err = f.store.Load()
			if err != nil || len(state.Apps) != 0 {
				t.Fatal("revocation retained App scope", state, err)
			}
		})
	}
}

func TestWiringStalePinAndPortNeverBecomeApproval(t *testing.T) {
	for _, change := range []string{"pin", "port"} {
		t.Run(change, func(t *testing.T) {
			f := newFixture(t, false)
			a := inspect(t, f, "ExampleApp", apps.Private)
			if change == "pin" {
				f.app.cert.Store(testCertificate(t))
			} else {
				writeMetadata(t, f.discovery.PackagesDir, "ExampleApp", f.other.port)
			}
			if _, err := f.api.SetPrivate(context.Background(), apps.PrivateRequest{Package: "ExampleApp", Enabled: true, Approval: &a}); err == nil {
				t.Fatal("stale approval accepted")
			}
			state, err := f.store.Load()
			if err != nil || len(state.Apps) != 0 || state.DSM != nil {
				t.Fatal("stale inspection wrote authority", state, err)
			}
		})
	}
}

func TestWiringPartialDNSCleanupRetryAndExplicitAuthorization(t *testing.T) {
	f := newFixture(t, false)
	ctx := context.Background()
	a := inspect(t, f, "ExampleApp", apps.Public)
	b := inspect(t, f, "OtherApp", apps.Public)
	f.public.dnsFail = "OtherApp"
	result, err := f.api.SetPublic(ctx, apps.PublicRequest{Packages: []string{"ExampleApp", "OtherApp"}, Enabled: true, Approvals: map[string]apps.Approved{"ExampleApp": a, "OtherApp": b}})
	if err != nil || len(result.Results) != 2 || !result.Results[0].OK || result.Results[1].OK {
		t.Fatal(result, err)
	}
	state, err := f.store.Load()
	if err != nil || state.Apps["OtherApp"].Public != nil {
		t.Fatal("failed DNS gained approval", state, err)
	}
	f.public.mu.Lock()
	f.public.fail = true
	f.public.mu.Unlock()
	if _, err = f.api.SetPublic(ctx, apps.PublicRequest{Packages: []string{"ExampleApp"}, Enabled: false}); err == nil {
		t.Fatal("cleanup failure hidden")
	}
	f.public.mu.Lock()
	f.public.fail = false
	f.public.mu.Unlock()
	if err = f.owner.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if n, _ := f.public.counts(); n != 0 {
		t.Fatal("orphan route did not clean up")
	}
	if err = f.api.CloudflareDisconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = f.api.CloudflareConnect(ctx); err != nil {
		t.Fatal(err)
	}
	f.public.mu.Lock()
	calls := f.public.authCalls
	f.public.mu.Unlock()
	if calls != 1 {
		t.Fatal("authorization was not explicit", calls)
	}
}

func TestWiringRecoveryProjectionAndCorruption(t *testing.T) {
	f := newFixture(t, false)
	ctx := context.Background()
	approved := inspect(t, f, "ExampleApp", apps.Private)
	if _, err := f.api.SetPrivate(ctx, apps.PrivateRequest{Package: "ExampleApp", Enabled: true, Approval: &approved}); err != nil {
		t.Fatal(err)
	}
	state, _ := f.store.Load()
	scope := state.Apps["ExampleApp"].Private
	f.owner.stopAll()
	_ = os.Remove(f.owner.cfg.ProjectionPath)
	if err := f.owner.Lifecycle().Recover(ctx); err != nil {
		t.Fatal(err)
	}
	code, _, err := request(t, f.store.Dir, scope.Socket, scope.Authority)
	if err != nil || code != 200 {
		t.Fatal("active approval did not recover", code, err)
	}
	if _, err = os.Stat(f.owner.cfg.ProjectionPath); err != nil {
		t.Fatal("projection not reconstructed", err)
	}
	if err = os.WriteFile(filepath.Join(f.store.Dir, "apps-state.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = f.owner.Lifecycle().Recover(ctx); !errors.Is(err, apps.ErrCorrupt) {
		t.Fatal(err)
	}
	if code, _, err = request(t, f.store.Dir, scope.Socket, scope.Authority); err == nil && code == 200 {
		t.Fatal("corrupt state forwarded")
	}
	if f.private.count() != 0 {
		t.Fatal("corrupt state retained App routes")
	}
}

func TestWiringCheckerPausesChangedMetadataAndNeverResumesIt(t *testing.T) {
	f := newFixture(t, false)
	ctx := context.Background()
	a := inspect(t, f, "ExampleApp", apps.Private)
	if _, err := f.api.SetPrivate(ctx, apps.PrivateRequest{Package: "ExampleApp", Enabled: true, Approval: &a}); err != nil {
		t.Fatal(err)
	}
	writeMetadata(t, f.discovery.PackagesDir, "ExampleApp", f.other.port)
	_ = f.owner.Check(ctx)
	state, err := f.store.Load()
	if err != nil || state.Apps["ExampleApp"].Private.State != apps.Paused {
		t.Fatal("checker did not persist a refusal", state, err)
	}
	writeMetadata(t, f.discovery.PackagesDir, "ExampleApp", f.app.port)
	if err = f.owner.Check(ctx); err != nil {
		t.Fatal(err)
	}
	state, err = f.store.Load()
	if err != nil || state.Apps["ExampleApp"].Private.State != apps.Paused || f.private.count() != 1 {
		t.Fatal("checker revived paused approval", state, err)
	}
}

func TestWiringProviderFailurePausesAndCleanupRetries(t *testing.T) {
	f := newFixture(t, false)
	ctx := context.Background()
	a := inspect(t, f, "ExampleApp", apps.Public)
	f.public.mu.Lock()
	f.public.fail = true
	f.public.mu.Unlock()
	if _, err := f.api.SetPublic(ctx, apps.PublicRequest{Packages: []string{"ExampleApp"}, Enabled: true, Approvals: map[string]apps.Approved{"ExampleApp": a}}); err == nil {
		t.Fatal("provider failure hidden")
	}
	state, err := f.store.Load()
	if err != nil || state.Apps["ExampleApp"].Public.State != apps.Paused || !state.Apps["ExampleApp"].Public.Cleanup {
		t.Fatal("failed public activation not paused", state, err)
	}
	f.public.mu.Lock()
	f.public.fail = false
	f.public.mu.Unlock()
	if err = f.owner.Check(ctx); err != nil {
		t.Fatal(err)
	}
	state, err = f.store.Load()
	if err != nil || state.Apps["ExampleApp"].Public.State != apps.Paused || state.Apps["ExampleApp"].Public.Cleanup {
		t.Fatal("cleanup did not settle", state, err)
	}
}

func TestWiringSReenableConfirmsNewKeyAndPublicReenableNeedsNoLogin(t *testing.T) {
	f := newFixture(t, false)
	ctx := context.Background()
	a := inspect(t, f, "ExampleApp", apps.Public)
	if _, err := f.api.SetPublic(ctx, apps.PublicRequest{Packages: []string{"ExampleApp"}, Enabled: true, Approvals: map[string]apps.Approved{"ExampleApp": a}}); err != nil {
		t.Fatal(err)
	}
	if err := f.owner.Lifecycle().Close(ctx, "ExampleApp", apps.Public, "certificate-changed"); err != nil {
		t.Fatal(err)
	}
	f.app.cert.Store(testCertificate(t))
	if err := f.api.CloudflareDisconnect(ctx); err != nil {
		t.Fatal(err)
	}
	b := inspect(t, f, "ExampleApp", apps.Public)
	if b.Pin.Mode != "S" || b.Pin.PreviousSPKI != a.Pin.SPKI || b.Pin.SPKI == a.Pin.SPKI {
		t.Fatal("new S key was not disclosed", b)
	}
	if _, err := f.api.Reenable(ctx, apps.ReenableRequest{Package: "ExampleApp", Scope: apps.Public, Approval: &b}); err != nil {
		t.Fatal(err)
	}
	f.public.mu.Lock()
	calls := f.public.authCalls
	f.public.mu.Unlock()
	if calls != 0 {
		t.Fatal("reenable required Cloudflare authorization")
	}
}

func TestWiringWReenableCannotDowngradeToS(t *testing.T) {
	f := newFixture(t, true)
	ctx := context.Background()
	a := inspect(t, f, "ExampleApp", apps.Private)
	if _, err := f.api.SetPrivate(ctx, apps.PrivateRequest{Package: "ExampleApp", Enabled: true, Approval: &a}); err != nil {
		t.Fatal(err)
	}
	if err := f.owner.Lifecycle().Close(ctx, "ExampleApp", apps.Private, "certificate-mismatch"); err != nil {
		t.Fatal(err)
	}
	f.app.cert.Store(testCertificate(t))
	if _, err := f.api.Inspect(ctx, apps.InspectRequest{Package: "ExampleApp", Scope: apps.Private}); err == nil {
		t.Fatal("W approval downgraded")
	}
	state, err := f.store.Load()
	if err != nil || state.Apps["ExampleApp"].Private.Binding.Pin.Mode != "W" || state.Apps["ExampleApp"].Private.State != apps.Paused {
		t.Fatal(state, err)
	}
}

func TestWiringRefusesRevisionChangeBetweenDNSAndDurableApproval(t *testing.T) {
	f := newFixture(t, false)
	ctx := context.Background()
	a := inspect(t, f, "ExampleApp", apps.Public)
	f.public.beforeActivate = func() {
		if err := f.store.Update(ctx, func(s *apps.State) error { _, err := s.Revision(); return err }); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.api.SetPublic(ctx, apps.PublicRequest{Packages: []string{"ExampleApp"}, Enabled: true, Approvals: map[string]apps.Approved{"ExampleApp": a}}); !errors.Is(err, apps.ErrConfirmationChanged) {
		t.Fatal("intervening refusal revision was overwritten", err)
	}
	state, err := f.store.Load()
	if err != nil || len(state.Apps) != 0 || state.DSM != nil {
		t.Fatal("stale compound approval became durable", state, err)
	}
}

func TestRemovedOwnDomainAppDNSCleanupRetries(t *testing.T) {
	f := newFixture(t, false)
	ctx := context.Background()
	approval := inspect(t, f, "ExampleApp", apps.Public)
	if _, err := f.api.SetPublic(ctx, apps.PublicRequest{Packages: []string{"ExampleApp"}, Enabled: true, Approvals: map[string]apps.Approved{"ExampleApp": approval}}); err != nil {
		t.Fatal(err)
	}
	f.public.deleteFail = true
	if err := os.RemoveAll(filepath.Join(f.discovery.PackagesDir, "ExampleApp")); err != nil {
		t.Fatal(err)
	}
	_ = f.owner.Check(ctx)
	if f.public.deleteCalls != 0 {
		t.Fatal("first observation deleted DNS")
	}
	_ = f.owner.Check(ctx)
	state, _ := f.store.Load()
	if scope := state.Apps["ExampleApp"].Public; scope.Reason != "removed" || !scope.Cleanup {
		t.Fatal("cleanup retry intent lost")
	}
	f.public.deleteFail = false
	if err := f.owner.Check(ctx); err != nil {
		t.Fatal(err)
	}
	state, _ = f.store.Load()
	if scope := state.Apps["ExampleApp"].Public; scope.Reason != "removed" || scope.Cleanup {
		t.Fatal("cleanup did not finish")
	}
	before := f.public.deleteCalls
	if err := f.owner.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if f.public.deleteCalls != before {
		t.Fatal("DNS deleted twice")
	}
}
