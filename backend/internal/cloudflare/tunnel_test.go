package cloudflare

import (
	"context"
	"dsmapp/internal/apps"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"dsmapp/internal/connector"
	"dsmapp/internal/publicname"
	"github.com/synopackageland/syno-pkg-land-sdk/supervision"
)

type fakeProc struct {
	mu    sync.Mutex
	alive bool
}

func (p *fakeProc) Exit() supervision.Exit { return supervision.Exit{Code: 0, Reason: "child_exit"} }
func (p *fakeProc) Stop() error            { p.exit(); return nil }
func (p *fakeProc) Alive() bool            { p.mu.Lock(); defer p.mu.Unlock(); return p.alive }
func (p *fakeProc) exit()                  { p.mu.Lock(); p.alive = false; p.mu.Unlock() }
func (p *fakeProc) Ready() bool            { return p.Alive() }
func newFakeProc() *fakeProc               { return &fakeProc{alive: true} }
func argsOf(c Command) string              { return strings.Join(c.Args, " ") }
func hasArgs(c Command, a ...string) bool  { return slices.Equal(c.Args[:min(len(a), len(c.Args))], a) }

type fakeLauncher struct {
	mu      sync.Mutex
	started []Command
	procs   []*fakeProc
	ran     []Command
	onStart func(Command, *fakeProc)
	onRun   func(Command) error
}

func (l *fakeLauncher) Start(_ context.Context, c Command) (Process, error) {
	p := newFakeProc()
	l.mu.Lock()
	l.started = append(l.started, c)
	l.procs = append(l.procs, p)
	on := l.onStart
	l.mu.Unlock()
	if on != nil {
		on(c, p)
	}
	return p, nil
}
func (l *fakeLauncher) Run(_ context.Context, c Command) error {
	l.mu.Lock()
	l.ran = append(l.ran, c)
	on := l.onRun
	l.mu.Unlock()
	if on != nil {
		return on(c)
	}
	return nil
}
func (l *fakeLauncher) runs() []Command {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Command(nil), l.ran...)
}
func (l *fakeLauncher) starts() []Command {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]Command(nil), l.started...)
}

// metrics is a fake cloudflared metrics server.
type metrics struct {
	mu       sync.Mutex
	ready    bool
	hostname string
	server   *httptest.Server
}

func newMetrics(t *testing.T) *metrics {
	m := &metrics{ready: true}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		defer m.mu.Unlock()
		switch r.URL.Path {
		case "/ready":
			if !m.ready {
				w.WriteHeader(503)
			}
		case "/quicktunnel":
			fmt.Fprintf(w, `{"hostname":%q}`, m.hostname)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(m.server.Close)
	return m
}
func (m *metrics) addr() string { return strings.TrimPrefix(m.server.URL, "http://") }

type fixture struct {
	t        *testing.T
	dir      string
	launcher *fakeLauncher
	metrics  *metrics
	tunnel   *Tunnel
	binary   error
	api      *httptest.Server
	zone     string
	inUse    bool
	taken    string // one name already in DNS
}

func newFixture(t *testing.T) *fixture {
	f := &fixture{t: t, dir: filepath.Join(t.TempDir(), "cloudflare"), launcher: &fakeLauncher{}, metrics: newMetrics(t), zone: "example.com"}
	f.api = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret-api-token" {
			w.WriteHeader(401)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/dns_records") {
			if f.inUse || (f.taken != "" && r.URL.Query().Get("name") == f.taken) {
				w.Write([]byte(`{"success":true,"result":[{"id":"r"}]}`))
			} else {
				w.Write([]byte(`{"success":true,"result":[]}`))
			}
			return
		}
		fmt.Fprintf(w, `{"success":true,"result":{"id":%q,"name":%q,"account":{"id":%q}}}`, testZone, f.zone, testAccount)
	}))
	t.Cleanup(f.api.Close)
	f.tunnel = NewTunnel(Config{
		Dir: f.dir,
		Cloudflared: func(context.Context) (string, error) {
			if f.binary != nil {
				return "", f.binary
			}
			return "/opt/cloudflared", nil
		},
		DSMPort: func(context.Context) (int, error) { return 5000, nil },
		DSMTarget: func(_ context.Context, host string) (string, error) {
			if quickHost(host) {
				return apps.PinSocket("dsm-quick"), nil
			}
			return apps.PinSocket("dsm"), nil
		},
		Launcher:     f.launcher,
		API:          API{Base: f.api.URL},
		MetricsAddr:  func() (string, error) { return f.metrics.addr(), nil },
		LoginTimeout: 2 * time.Second,
		Poll:         5 * time.Millisecond,
		RestartDelay: 5 * time.Millisecond,
	})
	t.Cleanup(func() { f.tunnel.Stop() })
	return f
}

func (f *fixture) waitState(want string) connector.Status {
	f.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		s := f.tunnel.Status()
		if s.State == want {
			return s
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("state %q (code %q), want %q", s.State, s.Code, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func private(t *testing.T, path string, perm os.FileMode) {
	t.Helper()
	info, e := os.Lstat(path)
	if e != nil || info.Mode().Perm() != perm {
		t.Fatalf("%s: %v %v, want %v", path, info, e, perm)
	}
}

func TestQuickRunsATemporaryTunnelToDSMAndShowsItsAddress(t *testing.T) {
	f := newFixture(t)
	f.metrics.ready = false
	if e := f.tunnel.Quick(context.Background()); e != nil {
		t.Fatal(e)
	}
	c := f.launcher.starts()[0]
	config := c.Args[3]
	want := "tunnel --no-autoupdate --config " + config + " --metrics " + f.metrics.addr() + " --url http://127.0.0.1:1"
	raw, err := os.ReadFile(config)
	if err != nil || string(raw) != "ingress:\n  - service: \"unix:"+apps.PinSocket(apps.QuickDSMSocketID)+"\"\n" || len(raw) > 256 {
		t.Fatal("unbounded or incorrect Quick ingress", string(raw), err)
	}
	if c.Path != "/opt/cloudflared" || argsOf(c) != want || c.Home != filepath.Join(f.dir, "home") {
		t.Fatalf("command %s %q home %s", c.Path, argsOf(c), c.Home)
	}
	private(t, f.dir, 0700)
	private(t, c.Home, 0700)
	if s := f.tunnel.Status(); s.State != "connecting" || s.Mode != "quick" || len(s.Endpoints) != 0 {
		t.Fatalf("status %+v", s)
	}
	f.metrics.mu.Lock()
	f.metrics.ready, f.metrics.hostname = true, "rest-texts.trycloudflare.com"
	f.metrics.mu.Unlock()
	s := f.waitState("connected")
	if len(s.Endpoints) != 1 || s.Endpoints[0].URL != "https://rest-texts.trycloudflare.com" || !s.Endpoints[0].Public || s.Endpoints[0].LocalPort != 5000 {
		t.Fatalf("endpoints %+v", s.Endpoints)
	}
}

func TestQuickBindsOnlyTheObservedCloudflareHostnameBeforePublishing(t *testing.T) {
	f := newFixture(t)
	f.metrics.hostname = "observed.trycloudflare.com"
	f.tunnel.cfg.DSMTarget = func(_ context.Context, host string) (string, error) {
		if host != "observed.trycloudflare.com" {
			t.Errorf("unobserved authority: %q", host)
		}
		return "", connector.Coded("pin_proxy_required", "certificate changed")
	}
	if err := f.tunnel.Quick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if s := f.waitState("failed"); s.Code != "pin_proxy_required" || len(s.Endpoints) != 0 {
		t.Fatal("published without trusted HTTPS", s)
	}
}

func TestRunningCloudflareReportsChangedDSMPort(t *testing.T) {
	for _, mode := range []string{"quick", "domain"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.metrics.hostname = "observed.trycloudflare.com"
			var port atomic.Int32
			port.Store(5001)
			f.tunnel.cfg.DSMPort = func(context.Context) (int, error) { return int(port.Load()), nil }
			if mode == "quick" {
				if err := f.tunnel.Quick(context.Background()); err != nil {
					t.Fatal(err)
				}
			} else {
				authorize := f.signIn(goodCert())
				f.cloudflaredRuns("")
				if err := f.tunnel.Domain(context.Background(), "nas"); err != nil {
					t.Fatal(err)
				}
				close(authorize)
			}
			f.waitState("connected")
			port.Store(5443)
			if s := f.waitState("failed"); s.Code != "pin_proxy_required" || s.DSMPortChange == nil || s.DSMPortChange.From != 5001 || s.DSMPortChange.To != 5443 || len(s.Endpoints) != 0 {
				t.Fatal("silent stale port", s)
			}
		})
	}
}

func TestQuickFallsBackToTheAddressCloudflaredPrints(t *testing.T) {
	f := newFixture(t)
	f.launcher.onStart = func(c Command, _ *fakeProc) {
		c.Output.Write([]byte("INF |  https://evil.trycloudflare.com.example  |\nINF |  https://from-output.trycloud"))
		c.Output.Write([]byte("flare.com  |\n"))
	}
	if e := f.tunnel.Quick(context.Background()); e != nil {
		t.Fatal(e)
	}
	if s := f.waitState("connected"); s.Endpoints[0].URL != "https://from-output.trycloudflare.com" {
		t.Fatalf("endpoints %+v", s.Endpoints)
	}
}

func TestQuickWithoutAnAddressFailsInsteadOfGuessing(t *testing.T) {
	f := newFixture(t)
	f.tunnel.cfg.AddressTimeout = 20 * time.Millisecond
	if e := f.tunnel.Quick(context.Background()); e != nil {
		t.Fatal(e)
	}
	if s := f.waitState("failed"); s.Code != "cloudflare_quick_address_unavailable" || len(s.Endpoints) != 0 {
		t.Fatalf("status %+v", s)
	}
}

func TestATunnelThatExitsRecoversAndDisableStopsRecovery(t *testing.T) {
	f := newFixture(t)
	f.metrics.hostname = "a.trycloudflare.com"
	f.tunnel.Quick(context.Background())
	f.waitState("connected")
	f.launcher.procs[0].exit()
	deadline := time.Now().Add(time.Second)
	for len(f.launcher.starts()) < 2 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(f.launcher.starts()) != 2 {
		t.Fatal("owned child did not restart within the recovery bound")
	}
	f.waitState("connected")
	if e := f.tunnel.Disable(); e != nil {
		t.Fatal(e)
	}
	time.Sleep(20 * time.Millisecond)
	if len(f.launcher.starts()) != 2 {
		t.Fatal("disabled tunnel restarted")
	}
	for _, p := range f.launcher.procs {
		if p.Alive() {
			t.Fatal("owned child survived disable")
		}
	}
}

func TestStartingNeedsTheProgramAndDSMPort(t *testing.T) {
	f := newFixture(t)
	f.binary = errors.New("missing")
	if e := f.tunnel.Quick(context.Background()); !errors.Is(e, connector.ErrBinaryRequired) {
		t.Fatalf("error %v", e)
	}
	f.binary = nil
	f.tunnel.cfg.DSMPort = func(context.Context) (int, error) { return 0, errors.New("unknown") }
	if e := f.tunnel.Quick(context.Background()); connector.ErrorCode(e) != "port_detection_unavailable" {
		t.Fatalf("error %v", e)
	}
	if len(f.launcher.starts()) != 0 {
		t.Fatal("started without a program or port")
	}
}

// signIn makes the fake `cloudflared tunnel login` print its page, then wait
// for authorize to write cert.pem and exit, as official cloudflared does.
func (f *fixture) signIn(certBytes []byte) chan struct{} {
	authorize := make(chan struct{})
	f.launcher.onStart = func(c Command, p *fakeProc) {
		if !hasArgs(c, "tunnel", "login") {
			return
		}
		c.Output.Write([]byte("Please open the following URL and log in with your Cloudflare account:\n\nhttps://dash.cloudflare.com/argotunnel?aud=&callback=https%3A%2F%2Flogin.cloudflareaccess.org%2Fkey\n\n"))
		go func() {
			<-authorize
			if certBytes != nil && p.Alive() {
				os.MkdirAll(filepath.Join(c.Home, ".cloudflared"), 0700)
				os.WriteFile(filepath.Join(c.Home, ".cloudflared", "cert.pem"), certBytes, 0600)
			}
			p.exit()
		}()
	}
	return authorize
}

const tunnelID = "c0833b13-41ef-41df-bbfd-ca30c11e7011"

// cloudflaredRuns answers create by writing credentials, as cloudflared does.
func (f *fixture) cloudflaredRuns(fail string) {
	f.launcher.onRun = func(c Command) error {
		cert := filepath.Join(f.dir, "home", ".cloudflared", "cert.pem")
		if !hasArgs(c, "tunnel", "--origincert", cert) {
			f.t.Errorf("command without the sign-in certificate: %q", argsOf(c))
		}
		if _, e := os.Stat(cert); e != nil {
			f.t.Errorf("certificate gone before %q", argsOf(c))
		}
		verb := c.Args[3]
		if verb == fail {
			return errors.New("exit status 1")
		}
		if verb == "create" {
			i := slices.Index(c.Args, "--credentials-file")
			raw := fmt.Sprintf(`{"AccountTag":%q,"TunnelSecret":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=","TunnelID":%q,"Endpoint":""}`, testAccount, tunnelID)
			return os.WriteFile(c.Args[i+1], []byte(raw), 0400)
		}
		return nil
	}
}

func (f *fixture) certKept() {
	f.t.Helper()
	file := f.tunnel.certFile()
	if _, e := readCert(file); e != nil {
		f.t.Fatal("retained authorization not private and usable", e)
	}
	private(f.t, file, 0600)
}

func (f *fixture) certGone() {
	f.t.Helper()
	if _, e := os.Lstat(filepath.Join(f.dir, "home", ".cloudflared", "cert.pem")); !os.IsNotExist(e) {
		f.t.Fatalf("cert.pem kept: %v", e)
	}
}

func TestOwnDomainSignsInCreatesRoutesKeepsTheCertificateThenRuns(t *testing.T) {
	f := newFixture(t)
	authorize := f.signIn(goodCert())
	f.cloudflaredRuns("")
	if e := f.tunnel.Domain(context.Background(), "nas"); e != nil {
		t.Fatal(e)
	}
	s := f.waitState("needs-login")
	for s.LoginURL == "" {
		s = f.tunnel.Status()
	}
	if s.Mode != "domain" || !strings.HasPrefix(s.LoginURL, "https://dash.cloudflare.com/argotunnel?") {
		t.Fatalf("status %+v", s)
	}
	close(authorize)
	s = f.waitState("connected")
	if s.Hostname != "nas.example.com" || s.LoginURL != "" || len(s.Endpoints) != 1 || s.Endpoints[0].URL != "https://nas.example.com" {
		t.Fatalf("status %+v", s)
	}
	f.certKept()
	runs := f.launcher.runs()
	if len(runs) != 2 || runs[0].Args[3] != "create" || !strings.HasPrefix(runs[0].Args[len(runs[0].Args)-1], "synopkgconnect-nas-") ||
		argsOf(runs[1]) != "tunnel --origincert "+filepath.Join(f.dir, "home", ".cloudflared", "cert.pem")+" route dns "+tunnelID+" nas.example.com" {
		t.Fatalf("runs %q", runs)
	}
	if slices.Contains(runs[1].Args, "--overwrite-dns") {
		t.Fatal("route may overwrite an existing record")
	}
	starts := f.launcher.starts()
	run := starts[len(starts)-1]
	config := filepath.Join(f.dir, "domain", "config.yml")
	credentials := filepath.Join(f.dir, "domain", "credentials.json")
	if argsOf(run) != "tunnel --no-autoupdate --config "+config+" --metrics "+f.metrics.addr()+" run --credentials-file "+credentials+" "+tunnelID {
		t.Fatalf("run %q", argsOf(run))
	}
	private(t, credentials, 0600)
	private(t, config, 0600)
	raw, _ := os.ReadFile(config)
	if string(raw) != "tunnel: \""+tunnelID+"\"\ncredentials-file: \""+credentials+"\"\ningress:\n  - hostname: \"nas.example.com\"\n    service: \"unix:/var/packages/SynoPkgConnect/var/run/pin-e76f96ad0464976a33e7bfa46ab384521cd0e5229a861bec.sock\"\n  - service: http_status:404\n" {
		t.Fatalf("config %s", raw)
	}
}

func TestOwnDomainNeverOverwritesANameInUse(t *testing.T) {
	f := newFixture(t)
	f.inUse = true
	close(f.signIn(goodCert()))
	f.cloudflaredRuns("")
	f.tunnel.Domain(context.Background(), "nas")
	if s := f.waitState("failed"); s.Code != "cloudflare_name_taken" || s.Hostname != "nas.example.com" {
		t.Fatalf("status %+v", s)
	}
	if len(f.launcher.runs()) != 0 {
		t.Fatalf("ran %q", f.launcher.runs())
	}
	f.certKept()
}

func TestOwnDomainDeletesTheCertificateWhenAnyStepFails(t *testing.T) {
	for verb, code := range map[string]string{"create": "cloudflare_create_failed", "route": "cloudflare_route_failed"} {
		t.Run(verb, func(t *testing.T) {
			f := newFixture(t)
			close(f.signIn(goodCert()))
			f.cloudflaredRuns(verb)
			f.tunnel.Domain(context.Background(), "nas")
			if s := f.waitState("failed"); s.Code != code {
				t.Fatalf("status %+v", s)
			}
			f.certGone()
			runs := f.launcher.runs()
			if verb == "route" && argsOf(runs[len(runs)-1]) != "tunnel --origincert "+filepath.Join(f.dir, "home", ".cloudflared", "cert.pem")+" delete "+tunnelID {
				t.Fatalf("created tunnel kept: %q", runs)
			}
			if _, e := os.Stat(filepath.Join(f.dir, "domain", "credentials.json")); !os.IsNotExist(e) {
				t.Fatalf("credentials kept: %v", e)
			}
		})
	}
}

func TestOwnDomainRefusesAnUnsafeCertificateAndDeletesIt(t *testing.T) {
	f := newFixture(t)
	close(f.signIn(certPEM(`{"zoneID":"x"}`)))
	f.tunnel.Domain(context.Background(), "nas")
	if s := f.waitState("failed"); s.Code != "cloudflare_login_failed" {
		t.Fatalf("status %+v", s)
	}
	f.certGone()
}

func TestOwnDomainSignInThatNeverFinishesTimesOut(t *testing.T) {
	f := newFixture(t)
	f.signIn(goodCert())
	f.tunnel.cfg.LoginTimeout = 30 * time.Millisecond
	f.tunnel.Domain(context.Background(), "nas")
	if s := f.waitState("failed"); s.Code != "cloudflare_login_timeout" {
		t.Fatalf("status %+v", s)
	}
	if f.launcher.procs[0].Alive() {
		t.Fatal("sign-in kept running")
	}
	f.certGone()
}

func TestDisablingDuringSignInStopsItAndDeletesTheCertificate(t *testing.T) {
	f := newFixture(t)
	authorize := f.signIn(goodCert())
	f.cloudflaredRuns("")
	f.tunnel.Domain(context.Background(), "nas")
	f.waitState("needs-login")
	if e := f.tunnel.Disable(); e != nil {
		t.Fatal(e)
	}
	close(authorize)
	time.Sleep(50 * time.Millisecond)
	if s := f.tunnel.Status(); s.State != "disconnected" {
		t.Fatalf("status %+v", s)
	}
	if f.launcher.procs[0].Alive() || len(f.launcher.runs()) != 0 {
		t.Fatal("sign-in continued after disable")
	}
	f.certGone()
}

func TestASavedDomainReconnectsWithoutSigningInAgain(t *testing.T) {
	f := newFixture(t)
	close(f.signIn(goodCert()))
	f.cloudflaredRuns("")
	f.tunnel.Domain(context.Background(), "nas")
	f.waitState("connected")
	f.tunnel.Disable()
	if s := f.tunnel.Status(); s.State != "disconnected" || s.Mode != "domain" || s.Hostname != "nas.example.com" {
		t.Fatalf("status %+v", s)
	}
	if e := f.tunnel.Domain(context.Background(), ""); e != nil {
		t.Fatal(e)
	}
	if s := f.waitState("connected"); s.Hostname != "nas.example.com" {
		t.Fatalf("status %+v", s)
	}
	if n := len(f.launcher.runs()); n != 2 {
		t.Fatalf("reconnect created again: %d runs", n)
	}
	restarted := NewTunnel(f.tunnel.cfg)
	f.tunnel.Stop()
	if e := restarted.Restore(context.Background()); e != nil {
		t.Fatal(e)
	}
	defer restarted.Stop()
	if s := restarted.Status(); s.Mode != "domain" || s.Hostname != "nas.example.com" || s.State == "disconnected" {
		t.Fatalf("restored %+v", s)
	}
}

func TestOwnDomainNeedsOneValidLabel(t *testing.T) {
	f := newFixture(t)
	for _, sub := range []string{"Nas", "a.b", "-x", strings.Repeat("a", 64)} {
		if e := f.tunnel.Domain(context.Background(), sub); e == nil {
			t.Errorf("accepted %q", sub)
		}
	}
	if e := f.tunnel.Domain(context.Background(), ""); e == nil {
		t.Error("reconnected with nothing saved")
	}
}

const testToken = "eyJhIjoiZmVkY2JhOTg3NjU0MzIxMGZlZGNiYTk4NzY1NDMyMTAiLCJ0IjoiYzA4MzNiMTMtNDFlZi00MWRmLWJiZmQtY2EzMGMxMWU3MDExIiwicyI6IlFVRkJRVUZCUVVGQlFVRkJRVUZCUVVGQlFVRkJRVUZCUVVGQlFVRkJRVUZCUVVFPSJ9"

// A dashboard tunnel saved by an earlier version keeps running after an
// update, from its private token file, and Disable deletes the token.
func TestRestoreRunsASavedTokenTunnelFromAPrivateFile(t *testing.T) {
	f := newFixture(t)
	token := filepath.Join(f.dir, "token")
	if e := os.MkdirAll(f.dir, 0700); e != nil {
		t.Fatal(e)
	}
	if e := connector.WritePrivate(token, []byte(testToken)); e != nil {
		t.Fatal(e)
	}
	if e := f.tunnel.saveSettings(settings{Enabled: true, Mode: "token", Hostname: "nas.example.com"}); e != nil {
		t.Fatal(e)
	}
	if e := f.tunnel.Restore(context.Background()); e != nil {
		t.Fatal(e)
	}
	s := f.waitState("connected")
	if s.Mode != "token" || s.Endpoints[0].URL != "https://nas.example.com" || s.KnownLimitation != "cloudflare_remote_config_possible" {
		t.Fatalf("status %+v", s)
	}
	c := f.launcher.starts()[0]
	if argsOf(c) != "tunnel --no-autoupdate --metrics "+f.metrics.addr()+" run --token-file "+token || strings.Contains(argsOf(c), testToken) {
		t.Fatalf("command %q", argsOf(c))
	}
	f.tunnel.Disable()
	if _, e := os.Stat(token); !os.IsNotExist(e) {
		t.Fatalf("token kept after disable: %v", e)
	}
}

// The own domain is browser authorization only: no request starts a token tunnel.
func TestNoRequestStartsATokenTunnel(t *testing.T) {
	f := newFixture(t)
	if e := f.tunnel.Start(context.Background(), Request{Mode: "token", Token: testToken, Hostname: "nas.example.com"}); !errors.Is(e, errMode) {
		t.Fatalf("error %v", e)
	}
	if len(f.launcher.starts()) != 0 {
		t.Fatal("started")
	}
	if _, e := os.Stat(filepath.Join(f.dir, "token")); !os.IsNotExist(e) {
		t.Fatalf("token saved: %v", e)
	}
}

func TestRevokeRemovesEverySecret(t *testing.T) {
	f := newFixture(t)
	close(f.signIn(goodCert()))
	f.cloudflaredRuns("")
	f.tunnel.Domain(context.Background(), "nas")
	f.waitState("connected")
	f.tunnel.Stop()
	os.WriteFile(filepath.Join(f.dir, "token"), []byte(testToken), 0600)
	if e := Revoke(f.dir); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"token", "domain", "home"} {
		if _, e := os.Lstat(filepath.Join(f.dir, name)); !os.IsNotExist(e) {
			t.Errorf("%s kept: %v", name, e)
		}
	}
}

func TestValidateAcceptsOnlyTheFieldsOfOneMode(t *testing.T) {
	ok := []Request{
		{Mode: "quick"},
		{Mode: "domain", Subdomain: "nas"},
		{Mode: "domain"},
	}
	for _, r := range ok {
		if e := Validate(r); e != nil {
			t.Errorf("%+v: %v", r, e)
		}
	}
	bad := []Request{
		{},
		{Mode: "sealed"},
		{Mode: "quick", Subdomain: "nas"},
		{Mode: "quick", Hostname: "nas.example.com"},
		{Mode: "domain", Subdomain: "a.b"},
		{Mode: "domain", Subdomain: "nas", Token: testToken},
		{Mode: "token", Token: testToken},
		{Mode: "token", Token: testToken, Hostname: "nas.example.com"},
		{Mode: "token", Token: testToken, Hostname: "nas.example.com", Subdomain: "nas"},
	}
	for _, r := range bad {
		if Validate(r) == nil {
			t.Errorf("accepted %+v", r)
		}
	}
}

func TestStartRefusesAMixedRequest(t *testing.T) {
	f := newFixture(t)
	if e := f.tunnel.Start(context.Background(), Request{Mode: "quick", Hostname: "x.example.com"}); e == nil {
		t.Fatal("started a mixed request")
	}
}

func TestRevokeReportsATokenItCannotRemove(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "token"), 0700)
	os.WriteFile(filepath.Join(dir, "token", "owned"), []byte("x"), 0600)
	if Revoke(dir) == nil {
		t.Fatal("an unexpected token directory was removed silently")
	}
}

func TestDSMSuggestedNameUsesTheNASLabel(t *testing.T) {
	one, two := newFixture(t), newFixture(t)
	a, b := one.tunnel.Status().SuggestedName, two.tunnel.Status().SuggestedName
	if a != b || a != publicname.NASName() || !dnsLabel.MatchString(a) {
		t.Fatalf("colliding defaults: %q %q", a, b)
	}
	again := NewTunnel(Config{Dir: one.dir})
	if got := again.Status().SuggestedName; got != a {
		t.Fatalf("changed on restart: %q %q", a, got)
	}
}

func TestQuickDNSGraceDoesNotRestartOrRenewOnReadinessPoll(t *testing.T) {
	f := newFixture(t)
	f.metrics.hostname = "new.trycloudflare.com"
	if err := f.tunnel.Quick(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := f.waitState("connected")
	if s.AddressCreatedAt.IsZero() {
		t.Fatal("new hostname has no creation time")
	}
	client := &http.Client{Transport: &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
		return nil, &net.DNSError{Err: "no such host", Name: s.Hostname, IsNotFound: true}
	}}}
	probe := (Probe{Client: client}).For(func(string) connector.Status { return f.tunnel.Status() }, nil)
	if _, err := probe(context.Background(), "cloudflare"); !errors.Is(err, connector.ErrCheckStarting) {
		t.Fatal(err)
	}
	f.metrics.mu.Lock()
	f.metrics.ready = false
	f.metrics.mu.Unlock()
	f.waitState("connecting")
	f.metrics.mu.Lock()
	f.metrics.ready = true
	f.metrics.mu.Unlock()
	later := f.waitState("connected")
	if !s.AddressCreatedAt.Equal(later.AddressCreatedAt) || len(f.launcher.starts()) != 1 || !f.launcher.procs[0].Alive() {
		t.Fatalf("renewed or restarted: before %v after %v starts %d", s.AddressCreatedAt, later.AddressCreatedAt, len(f.launcher.starts()))
	}
}

func TestOwnDomainCreationTimeSurvivesReconnect(t *testing.T) {
	f := newFixture(t)
	authorize := f.signIn(goodCert())
	f.cloudflaredRuns("")
	if err := f.tunnel.Domain(context.Background(), "nas"); err != nil {
		t.Fatal(err)
	}
	close(authorize)
	first := f.waitState("connected")
	saved, err := f.tunnel.savedDomain()
	if err != nil || first.AddressCreatedAt.IsZero() || !first.AddressCreatedAt.Equal(saved.AddressCreatedAt) {
		t.Fatalf("creation time: %+v %v", saved, err)
	}
	if err := f.tunnel.Domain(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	if next := f.waitState("connected"); !next.AddressCreatedAt.Equal(first.AddressCreatedAt) {
		t.Fatalf("reconnect renewed creation time: %v -> %v", first.AddressCreatedAt, next.AddressCreatedAt)
	}
}
