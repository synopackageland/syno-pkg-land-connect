package nbserver

import (
	"context"
	"crypto/tls"
	"dsmapp/internal/ipctest"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"dsmapp/internal/connector"
	"github.com/synopackageland/syno-pkg-land-sdk/supervision"
)

// fakeNB stands in for the official server: the HTTP API on the management
// port with the generated certificate, and /health on the health port.
type fakeNB struct {
	t          *testing.T
	port       int
	mu         sync.Mutex
	owner      bool
	setups     int
	keys       int
	starts     int
	exitAtOnce bool
	procs      []*fakeProc
	auth       []string
	// tokens maps each valid API token to its ID.
	tokens  map[string]string
	minted  int
	deleted []string
}

func (f *fakeNB) authorized(r *http.Request) bool {
	_, ok := f.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Token ")]
	return ok && strings.HasPrefix(r.Header.Get("Authorization"), "Token ")
}

type fakeProc struct {
	mu      sync.Mutex
	alive   bool
	servers []*http.Server
}

func (p *fakeProc) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.alive {
		for _, s := range p.servers {
			s.Close()
		}
	}
	p.alive = false
	return nil
}
func (p *fakeProc) Exit() supervision.Exit { return supervision.Exit{Code: 0, Reason: "child_exit"} }
func (p *fakeProc) Alive() bool            { p.mu.Lock(); defer p.mu.Unlock(); return p.alive }
func (p *fakeProc) Pid() int               { return os.Getpid() }

func (f *fakeNB) Start(_ context.Context, c Command) (Process, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.starts++
	if !slices.Equal(c.Args, []string{"--config", filepath.Join(c.Dir, "config.yaml")}) || !slices.Contains(c.Env, "NB_DISABLE_GEOLOCATION=true") || !slices.Contains(c.Env, "NB_SETUP_PAT_ENABLED=true") || len(c.Env) != 4 {
		f.t.Errorf("command %+v", c)
	}
	p := &fakeProc{alive: !f.exitAtOnce}
	f.procs = append(f.procs, p)
	if f.exitAtOnce {
		return p, nil
	}
	var st state
	if e := readJSON(filepath.Join(c.Dir, "state.json"), &st); e != nil {
		return nil, e
	}
	cert, e := tls.LoadX509KeyPair(filepath.Join(c.Dir, "tls", "cert.pem"), filepath.Join(c.Dir, "tls", "key.pem"))
	if e != nil {
		return nil, e
	}
	listen := func(port int, h http.Handler, tlsConfig *tls.Config) error {
		l, e := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		if e != nil {
			return e
		}
		s := &http.Server{Handler: h}
		p.servers = append(p.servers, s)
		if tlsConfig != nil {
			l = tls.NewListener(l, tlsConfig)
		}
		go s.Serve(l)
		return nil
	}
	if e = listen(f.port, http.HandlerFunc(f.api), &tls.Config{Certificates: []tls.Certificate{cert}}); e != nil {
		return nil, e
	}
	// Like the real combined server, /health never reads healthy: it counts
	// only standalone relay listeners.
	if e = listen(st.Health, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(503)
	}), nil); e != nil {
		return nil, e
	}
	return p, nil
}

func (f *fakeNB) api(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auth = append(f.auth, r.URL.Path+" "+r.Header.Get("Authorization"))
	switch r.Method + " " + r.URL.Path {
	case "GET /api/instance":
		json.NewEncoder(w).Encode(map[string]bool{"setup_required": !f.owner})
	case "POST /api/setup":
		var in map[string]any
		json.NewDecoder(r.Body).Decode(&in)
		if f.owner || in["create_pat"] != true || !strings.HasPrefix(in["email"].(string), "admin@netbird.") || len(in["password"].(string)) < 20 {
			w.WriteHeader(412)
			return
		}
		f.owner = true
		f.setups++
		f.tokens = map[string]string{"nbp_token": "t0"}
		json.NewEncoder(w).Encode(map[string]any{"user_id": "u", "email": in["email"], "personal_access_token": "nbp_token"})
	case "POST /api/setup-keys":
		var in map[string]any
		json.NewDecoder(r.Body).Decode(&in)
		if !f.authorized(r) || in["type"] != "one-off" || in["usage_limit"] != float64(1) {
			w.WriteHeader(401)
			return
		}
		f.keys++
		json.NewEncoder(w).Encode(map[string]any{"key": "A1B2C3D4-0000-1111-2222-333344445555"})
	case "GET /api/peers":
		if !f.authorized(r) {
			w.WriteHeader(401)
			return
		}
		w.Write([]byte(`[{"connected":true},{"connected":false}]`))
	case "GET /api/users/u/tokens":
		if !f.authorized(r) {
			w.WriteHeader(401)
			return
		}
		list := []map[string]any{}
		for _, id := range f.tokens {
			list = append(list, map[string]any{"id": id, "name": "Connect", "expiration_date": time.Now().Add(365 * 24 * time.Hour)})
		}
		json.NewEncoder(w).Encode(list)
	case "POST /api/users/u/tokens":
		var in map[string]any
		json.NewDecoder(r.Body).Decode(&in)
		if !f.authorized(r) || in["expires_in"] != float64(365) {
			w.WriteHeader(401)
			return
		}
		f.minted++
		plain, id := "nbp_token_"+strconv.Itoa(f.minted), "t"+strconv.Itoa(f.minted)
		f.tokens[plain] = id
		json.NewEncoder(w).Encode(map[string]any{"plain_token": plain, "personal_access_token": map[string]any{"id": id, "name": "Connect", "expiration_date": time.Now().Add(365 * 24 * time.Hour)}})
	default:
		if id, ok := strings.CutPrefix(r.URL.Path, "/api/users/u/tokens/"); ok && r.Method == "DELETE" && f.authorized(r) {
			for plain, have := range f.tokens {
				if have == id {
					delete(f.tokens, plain)
					f.deleted = append(f.deleted, id)
					w.Write([]byte("{}"))
					return
				}
			}
		}
		w.WriteHeader(404)
	}
}
func (f *fakeNB) proc() *fakeProc {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.procs[len(f.procs)-1]
}
func (f *fakeNB) startCount() int { f.mu.Lock(); defer f.mu.Unlock(); return f.starts }

type harness struct {
	t      *testing.T
	nb     *fakeNB
	s      *Server
	dir    string
	mu     sync.Mutex
	calls  []string
	zone   error
	check  error
	join   error
	unrt   error
	bridge error
	unbrg  error
	ready  error
	member bool
	joined []string
	leave  error
}

func (h *harness) call(c string) { h.mu.Lock(); h.calls = append(h.calls, c); h.mu.Unlock() }
func (h *harness) seen() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.calls)
}

func freeTCP(t *testing.T) int {
	l, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func newHarness(t *testing.T) *harness {
	h := &harness{t: t, dir: filepath.Join(t.TempDir(), "netbird-server")}
	h.nb = &fakeNB{t: t, port: freeTCP(t)}
	h.s = h.server()
	t.Cleanup(h.s.Close)
	return h
}

func (h *harness) server() *Server {
	return New(Deps{
		Dir:      h.dir,
		Launcher: h.nb,
		Port:     h.nb.port,
		Poll:     5 * time.Millisecond, First: 5 * time.Millisecond,
		StartWindow: 2 * time.Second, CheckWindow: 20 * time.Millisecond, GuideWindow: 3 * time.Second,
		Zone: func() (string, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			return "example.com", h.zone
		},
		Install: func(context.Context) error { h.call("install"); return nil },
		Binary:  func(context.Context) (string, string, error) { return "/opt/netbird-server", "0.81.5", nil },
		Name:    func(context.Context) (string, error) { h.call("name"); return "netbird.example.com", nil },
		Route: func(_ context.Context, host string) error {
			o, ok := h.s.Origin()
			if !ok || o.Hostname != host || o.Port != h.nb.port || o.CAFile != filepath.Join(h.dir, "tls", "cert.pem") {
				h.t.Errorf("route before the origin is published: %+v %v", o, ok)
			}
			h.call("route " + host)
			return nil
		},
		Unroute: func(_ context.Context, host string) error {
			if _, ok := h.s.Origin(); ok {
				h.t.Error("unroute while the origin is still served")
			}
			h.call("unroute " + host)
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.unrt
		},
		Ready: func(_ context.Context, zone string) error {
			if zone != "example.com" {
				h.t.Error("ready for", zone)
			}
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.ready
		},
		Bridge: func(_ context.Context, zone, host string) error {
			h.call("bridge " + zone + " " + host)
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.bridge
		},
		Unbridge: func(context.Context) error {
			h.call("unbridge")
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.unbrg
		},
		Check: func(_ context.Context, url string) error {
			h.call("check " + url)
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.check
		},
		Join: func(_ context.Context, url, key string) error {
			h.call("join " + url)
			h.mu.Lock()
			defer h.mu.Unlock()
			h.joined = append(h.joined, key)
			return h.join
		},
		Member: func(string) bool { h.mu.Lock(); defer h.mu.Unlock(); return h.member },
		Leave: func(_ context.Context, url string) error {
			h.call("leave " + url)
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.leave
		},
	})
}

func (h *harness) wait(want string) Status {
	h.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		st := h.s.Status(context.Background())
		if st.State == want {
			return st
		}
		if time.Now().After(deadline) {
			h.t.Fatalf("state %q, want %q: %+v", st.State, want, st)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func stepStates(st Status) string {
	out := []string{}
	for _, s := range st.Steps {
		out = append(out, s.ID+"="+s.State+s.Code)
	}
	return strings.Join(out, " ")
}

func TestSetupRunsEveryStepThenJoinsThisNAS(t *testing.T) {
	h := newHarness(t)
	if st := h.s.Status(context.Background()); st.State != "off" || st.Zone != "example.com" || len(st.Steps) != 0 {
		t.Fatalf("%+v", st)
	}
	if e := h.s.Setup(); e != nil {
		t.Fatal(e)
	}
	st := h.wait("running")
	for _, s := range st.Steps {
		if s.State != "done" {
			t.Fatal(stepStates(st))
		}
	}
	want := []string{"install", "name", "route netbird.example.com", "bridge example.com netbird.example.com", "check https://netbird.example.com", "join https://netbird.example.com"}
	if !slices.Equal(h.seen(), want) {
		t.Fatal(h.seen())
	}
	if h.joined[0] != "A1B2C3D4-0000-1111-2222-333344445555" || h.nb.setups != 1 || h.nb.keys != 1 {
		t.Fatal("setup key", h.joined, h.nb.setups, h.nb.keys)
	}
	if !st.Joined || st.Hostname != "netbird.example.com" || st.ManagementURL != "https://netbird.example.com" || st.AdminEmail != "admin@netbird.example.com" || st.Version != "0.81.5" || st.DataBytes < 0 {
		t.Fatalf("%+v", st)
	}
	if st.Peers == nil || *st.Peers != 2 || *st.PeersOnline != 1 {
		t.Fatal("peers", st.Peers, st.PeersOnline)
	}
	raw, _ := json.Marshal(st)
	var k secrets
	if e := readJSON(filepath.Join(h.dir, "secrets.json"), &k); e != nil || k.Token != "nbp_token" || k.UserID != "u" || k.TokenID != "t0" || time.Until(k.TokenExpires) < 300*24*time.Hour {
		t.Fatal(k, e)
	}
	for _, secret := range []string{k.Token, k.AdminPassword, k.StoreKey, k.AuthSecret, "A1B2C3D4"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("status leaks a secret")
		}
	}
	for _, f := range []string{"secrets.json", "state.json", "config.yaml", "tls/key.pem"} {
		if info, e := os.Stat(filepath.Join(h.dir, f)); e != nil || info.Mode().Perm() != 0600 {
			t.Fatal(f, info, e)
		}
	}
	if email, password, e := h.s.Credentials(); e != nil || email != k.AdminEmail || password != k.AdminPassword {
		t.Fatal("credentials", e)
	}
}

func TestMissingPrerequisiteStopsBeforeAnyChange(t *testing.T) {
	h := newHarness(t)
	h.zone = connector.Coded("cloudflare_own_domain_required", "x")
	if st := h.s.Status(context.Background()); st.Prerequisite != "cloudflare_own_domain_required" || st.Zone != "" {
		t.Fatalf("%+v", st)
	}
	h.s.Setup()
	st := h.wait("failed")
	if st.Code != "cloudflare_own_domain_required" || st.Steps[0].State != "failed" || st.Steps[1].State != "pending" || len(h.seen()) != 0 {
		t.Fatal(stepStates(st), h.seen())
	}
	if _, e := os.Stat(h.dir); !os.IsNotExist(e) {
		t.Fatal("files written without the prerequisites")
	}
}

func TestTokenWithoutTheBridgePermissionIsNamedBeforeAnyChange(t *testing.T) {
	h := newHarness(t)
	h.ready = connector.Coded("workers_routes_permission", "x")
	if st := h.s.Status(context.Background()); st.Prerequisite != "workers_routes_permission" || st.Zone != "example.com" {
		t.Fatalf("%+v", st)
	}
	h.s.Setup()
	st := h.wait("failed")
	if st.Code != "workers_routes_permission" || st.Steps[0].State != "failed" || len(h.seen()) != 0 {
		t.Fatal(stepStates(st), h.seen())
	}
	// The permission added to the same token: setup goes through.
	h.mu.Lock()
	h.ready = nil
	h.mu.Unlock()
	if st := h.s.Status(context.Background()); st.Prerequisite != "" {
		t.Fatalf("%+v", st)
	}
	h.s.Setup()
	h.wait("running")
}

func TestFailedCheckKeepsServerAndRetryContinuesWithoutNewOwner(t *testing.T) {
	h := newHarness(t)
	h.check = connector.Coded("netbird_server_signal_blocked", "x")
	h.s.Setup()
	st := h.wait("failed")
	if st.Code != "netbird_server_signal_blocked" || !strings.Contains(stepStates(st), "check=failednetbird_server_signal_blocked join=pending") {
		t.Fatal(stepStates(st))
	}
	if slices.ContainsFunc(h.seen(), func(c string) bool { return strings.HasPrefix(c, "join") }) {
		t.Fatal("joined without a passing check")
	}
	if !h.nb.proc().Alive() {
		t.Fatal("server stopped by a failed check")
	}
	if e := h.s.Setup(); e != nil {
		t.Fatal(e)
	}
	h.wait("working")
	h.mu.Lock()
	h.check = nil
	h.mu.Unlock()
	h.s.stopJob()
	h.s.Setup()
	h.wait("running")
	if h.nb.setups != 1 || h.nb.startCount() != 1 || countOf(h.seen(), "name") != 1 {
		t.Fatal("retry redid finished work", h.nb.setups, h.nb.startCount(), h.seen())
	}
}

func TestAlreadyJoinedNASIsNotJoinedAgain(t *testing.T) {
	h := newHarness(t)
	h.member = true
	h.s.Setup()
	h.wait("running")
	if h.nb.keys != 0 || len(h.joined) != 0 {
		t.Fatal("joined twice")
	}
}

func TestOwnerMadeElsewhereIsReported(t *testing.T) {
	h := newHarness(t)
	h.nb.owner = true
	h.s.Setup()
	if st := h.wait("failed"); st.Code != "netbird_server_owner_exists" {
		t.Fatal(stepStates(st))
	}
}

func TestServerThatKeepsExitingFailsStartButStaysSupervised(t *testing.T) {
	h := newHarness(t)
	h.nb.exitAtOnce = true
	h.s.Setup()
	st := h.wait("failed")
	if st.Code != "netbird_server_start_failed" {
		t.Fatal(stepStates(st))
	}
	h.nb.mu.Lock()
	h.nb.exitAtOnce = false
	h.nb.mu.Unlock()
	// Supervision keeps restarting with backoff and the server comes up.
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.s.mu.Lock()
		ok := h.s.healthy
		h.s.mu.Unlock()
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("supervision gave up")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestOffServerLeavesNoFootprint(t *testing.T) {
	h := newHarness(t)
	if e := h.s.Restore(context.Background()); e != nil {
		t.Fatal(e)
	}
	if _, ok := h.s.Origin(); ok || h.nb.startCount() != 0 || h.s.watching != nil || h.s.restoring.Active() {
		t.Fatal("off server runs something")
	}
	if _, e := os.Stat(h.dir); !os.IsNotExist(e) {
		t.Fatal("off server wrote files")
	}
}

func TestRestoreRestartsAndCleanExitBacksOffAndRestartsAgain(t *testing.T) {
	h := newHarness(t)
	h.s.Setup()
	h.wait("running")
	h.s.Close()
	if h.nb.proc().Alive() {
		t.Fatal("close left the server running")
	}
	h.s = h.server()
	h.s.restoring.First = 40 * time.Millisecond
	t.Cleanup(h.s.Close)
	if _, ok := h.s.Origin(); ok {
		t.Fatal("origin before restore")
	}
	if e := h.s.Restore(context.Background()); e != nil {
		t.Fatal(e)
	}
	if o, ok := h.s.Origin(); !ok || o.Hostname != "netbird.example.com" {
		t.Fatal("restore did not publish the origin")
	}
	h.wait("running")
	before := h.nb.startCount()
	h.nb.proc().Stop()
	deadline := time.Now().Add(5 * time.Second)
	for {
		h.s.mu.Lock()
		snapshot := h.s.restoring.Snapshot()
		h.s.mu.Unlock()
		if snapshot.State == supervision.Backoff {
			if snapshot.LastExit == nil || snapshot.LastExit.Code != 0 || snapshot.Attempts != 1 || snapshot.NextRetry.IsZero() || h.nb.startCount() != before {
				t.Fatal(snapshot)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("server clean exit bypassed backoff", snapshot)
		}
		time.Sleep(time.Millisecond)
	}
	for h.nb.startCount() == before || !h.nb.proc().Alive() {
		if time.Now().After(deadline) {
			t.Fatal("exited server not restarted")
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.wait("running")
}

func TestDisableStopsUnroutesAndKeepsOrRemovesData(t *testing.T) {
	h := newHarness(t)
	h.s.Setup()
	h.wait("running")
	h.unrt = connector.Coded("cloudflare_reconnect_required", "x")
	if e := h.s.Disable(context.Background(), true); connector.ErrorCode(e) != "cloudflare_reconnect_required" {
		t.Fatal(e)
	}
	st := h.s.Status(context.Background())
	if st.State != "off" || st.CleanupHostname != "netbird.example.com" || h.nb.proc().Alive() {
		t.Fatalf("%+v", st)
	}
	if _, e := os.Stat(filepath.Join(h.dir, "secrets.json")); e != nil {
		t.Fatal("data removed before its name")
	}
	h.unrt = nil
	if e := h.s.Disable(context.Background(), false); e != nil {
		t.Fatal(e)
	}
	if st = h.s.Status(context.Background()); st.CleanupHostname != "" || st.State != "off" {
		t.Fatalf("%+v", st)
	}
	if _, e := os.Stat(filepath.Join(h.dir, "secrets.json")); e != nil {
		t.Fatal("kept data removed")
	}
	// Turning it on again reuses the name, owner and data.
	h.s.Setup()
	h.wait("running")
	if h.nb.setups != 1 {
		t.Fatal("owner made again")
	}
	if e := h.s.Disable(context.Background(), true); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(h.dir); !os.IsNotExist(e) {
		t.Fatal("data kept", e)
	}
	if n := len(h.seen()); !slices.Equal(h.seen()[n-3:], []string{"leave https://netbird.example.com", "unroute netbird.example.com", "unbridge"}) {
		t.Fatal(h.seen())
	}
}

func TestDisableRemovesTheBridgeEvenWhenTheNameStays(t *testing.T) {
	h := newHarness(t)
	h.s.Setup()
	h.wait("running")
	h.unrt = connector.Coded("cloudflare_reconnect_required", "x")
	if e := h.s.Disable(context.Background(), false); connector.ErrorCode(e) != "cloudflare_reconnect_required" {
		t.Fatal(e)
	}
	if n := len(h.seen()); h.seen()[n-1] != "unbridge" {
		t.Fatal("bridge kept while the name failed", h.seen())
	}
	var st state
	readJSON(filepath.Join(h.dir, "state.json"), &st)
	if !st.Routed || st.Bridged {
		t.Fatalf("%+v", st)
	}
	// A retry withdraws only what is left.
	h.unrt = nil
	if e := h.s.Disable(context.Background(), false); e != nil {
		t.Fatal(e)
	}
	if n := len(h.seen()); h.seen()[n-1] != "unroute netbird.example.com" {
		t.Fatal(h.seen())
	}
}

func TestExpiredTokenAfterALongOffSaysSo(t *testing.T) {
	h := newHarness(t)
	h.s.Setup()
	h.wait("running")
	if e := h.s.Disable(context.Background(), false); e != nil {
		t.Fatal(e)
	}
	var k secrets
	readJSON(filepath.Join(h.dir, "secrets.json"), &k)
	k.TokenExpires = time.Now().Add(-time.Hour)
	writeJSON(filepath.Join(h.dir, "secrets.json"), k)
	h.s.Setup()
	if st := h.wait("failed"); st.Code != "netbird_server_token_expired" {
		t.Fatalf("%+v", st)
	}
}

func TestDisableDisconnectsThisNASAndReportsAFailedDisconnect(t *testing.T) {
	h := newHarness(t)
	h.s.Setup()
	h.wait("running")
	h.leave = errors.New("busy")
	e := h.s.Disable(context.Background(), false)
	if connector.ErrorCode(e) != "netbird_server_leave_failed" {
		t.Fatal(e)
	}
	// The rest is done anyway: the server is off and its name removed.
	st := h.s.Status(context.Background())
	if st.State != "off" || st.CleanupHostname != "" || h.nb.proc().Alive() || !st.Joined {
		t.Fatalf("%+v", st)
	}
	h.leave = nil
	if e = h.s.Disable(context.Background(), false); e != nil {
		t.Fatal(e)
	}
	if st = h.s.Status(context.Background()); st.Joined {
		t.Fatalf("%+v", st)
	}
	if got := slices.DeleteFunc(h.seen(), func(c string) bool { return !strings.HasPrefix(c, "leave") }); len(got) != 2 {
		t.Fatal(got)
	}
}

func TestAPITokenIsRenewedAMonthBeforeItExpires(t *testing.T) {
	h := newHarness(t)
	h.s.Setup()
	h.wait("running")
	var k secrets
	readJSON(filepath.Join(h.dir, "secrets.json"), &k)
	// Far from expiry nothing changes.
	h.s.mu.Lock()
	h.s.renewAt = time.Time{}
	h.s.mu.Unlock()
	time.Sleep(50 * time.Millisecond)
	h.nb.mu.Lock()
	minted := h.nb.minted
	h.nb.mu.Unlock()
	if minted != 0 {
		t.Fatal("renewed early")
	}
	k.TokenExpires = time.Now().Add(20 * 24 * time.Hour)
	h.s.mu.Lock()
	writeJSON(filepath.Join(h.dir, "secrets.json"), k)
	h.s.renewAt = time.Time{}
	h.s.mu.Unlock()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var now secrets
		h.s.mu.Lock()
		readJSON(filepath.Join(h.dir, "secrets.json"), &now)
		next := h.s.renewAt
		h.s.mu.Unlock()
		// The next look is set once the renewal returns.
		if now.Token == "nbp_token_1" && !next.IsZero() {
			if now.TokenID != "t1" || time.Until(now.TokenExpires) < 300*24*time.Hour || time.Until(next) < 300*24*time.Hour {
				t.Fatalf("%+v %v", now, next)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("not renewed")
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.nb.mu.Lock()
	defer h.nb.mu.Unlock()
	if !slices.Equal(h.nb.deleted, []string{"t0"}) || len(h.nb.tokens) != 1 {
		t.Fatal("old token kept", h.nb.deleted, h.nb.tokens)
	}
}

func TestDisableCancelsARunningSetup(t *testing.T) {
	h := newHarness(t)
	block := make(chan struct{})
	h.s.d.Install = func(ctx context.Context) error {
		close(block)
		<-ctx.Done()
		return ctx.Err()
	}
	h.s.Setup()
	<-block
	if e := h.s.Setup(); e != nil {
		t.Fatal("repeat setup must follow the live job", e)
	}
	if e := h.s.Disable(context.Background(), false); e != nil {
		t.Fatal(e)
	}
	if st := h.s.Status(context.Background()); st.State != "off" {
		t.Fatalf("%+v", st)
	}
}

func TestUpdateRestartsTheServerAndRevertsWhenTheNewOneFails(t *testing.T) {
	h := newHarness(t)
	h.s.Setup()
	h.wait("running")
	applied, reverted := 0, 0
	prepare := func() (func() error, func() error, error) {
		return func() error { applied++; return nil }, func() error { reverted++; return nil }, nil
	}
	before := h.nb.startCount()
	if e := h.s.UpdateBinary(context.Background(), prepare); e != nil || applied != 1 || h.nb.startCount() != before+1 {
		t.Fatal("update", e)
	}
	h.wait("running")
	h.nb.mu.Lock()
	h.nb.exitAtOnce = true
	h.nb.mu.Unlock()
	// The old executable answers again after the revert.
	revertToGood := func() (func() error, func() error, error) {
		return func() error { return nil }, func() error {
			reverted++
			h.nb.mu.Lock()
			h.nb.exitAtOnce = false
			h.nb.mu.Unlock()
			return nil
		}, nil
	}
	if e := h.s.UpdateBinary(context.Background(), revertToGood); e == nil || reverted != 1 {
		t.Fatal("failed update kept", e, reverted)
	}
	h.wait("running")
	unchanged := errors.New("unchanged")
	if e := h.s.UpdateBinary(context.Background(), func() (func() error, func() error, error) { return nil, nil, unchanged }); !errors.Is(e, unchanged) {
		t.Fatal(e)
	}
}

func TestUpdateWhileOffOnlySelects(t *testing.T) {
	h := newHarness(t)
	applied := false
	e := h.s.UpdateBinary(context.Background(), func() (func() error, func() error, error) {
		return func() error { applied = true; return nil }, func() error { return nil }, nil
	})
	if e != nil || !applied || h.nb.startCount() != 0 {
		t.Fatal(e)
	}
}

func TestControlHandlerAndClient(t *testing.T) {
	h := newHarness(t)
	sock := ipctest.Path(t, filepath.Join(t.TempDir(), "c.sock"))
	l, e := ipctest.Listen(t, sock)
	if e != nil {
		t.Fatal(e)
	}
	mux := http.NewServeMux()
	mux.Handle("/nbserver/", h.s.Handler())
	srv := httptest.NewUnstartedServer(mux)
	srv.Listener = l
	srv.Start()
	defer srv.Close()
	c := Client{Socket: sock}
	if r, e := c.Call(context.Background(), "status", Request{}); e != nil || r.Status.State != "off" {
		t.Fatal(r, e)
	}
	if _, e := c.Call(context.Background(), "credentials", Request{}); connector.ErrorCode(e) != "netbird_server_off" {
		t.Fatal(e)
	}
	if r, e := c.Call(context.Background(), "setup", Request{}); e != nil || r.Status.State != "working" {
		t.Fatal(r, e)
	}
	h.wait("running")
	if r, e := c.Call(context.Background(), "credentials", Request{}); e != nil || r.Email != "admin@netbird.example.com" || r.Password == "" {
		t.Fatal(r, e)
	}
	if r, e := c.Call(context.Background(), "disable", Request{RemoveData: true}); e != nil || r.Status.State != "off" {
		t.Fatal(r, e)
	}
	if _, e := c.Call(context.Background(), "nope", Request{}); e == nil {
		t.Fatal("unknown action accepted")
	}
}

func countOf(list []string, v string) int {
	n := 0
	for _, s := range list {
		if s == v {
			n++
		}
	}
	return n
}

func TestTakenNameIsChosenAgainOnRetry(t *testing.T) {
	h := newHarness(t)
	route := h.s.d.Route
	names := 0
	h.s.d.Name = func(context.Context) (string, error) {
		names++
		if names == 1 {
			return "netbird.example.com", nil
		}
		return "netbird-2.example.com", nil
	}
	h.s.d.Route = func(context.Context, string) error { return connector.Coded("cloudflare_name_taken", "x") }
	h.s.Setup()
	if st := h.wait("failed"); st.Code != "cloudflare_name_taken" || st.Hostname != "" {
		t.Fatalf("%+v", st)
	}
	h.s.d.Route = route
	h.s.Setup()
	if st := h.wait("running"); st.Hostname != "netbird-2.example.com" {
		t.Fatalf("%+v", st)
	}
}

func TestBridgeFailureStopsSetupBeforeTheServerStarts(t *testing.T) {
	h := newHarness(t)
	h.bridge = connector.Coded("workers_routes_permission", "x")
	h.s.Setup()
	st := h.wait("failed")
	if st.Code != "workers_routes_permission" || !strings.Contains(stepStates(st), "bridge=failedworkers_routes_permission start=pending") || h.nb.startCount() != 0 {
		t.Fatal(stepStates(st))
	}
	// Turning off removes whatever part of the bridge was made.
	if e := h.s.Disable(context.Background(), false); e != nil {
		t.Fatal(e)
	}
	if !slices.Contains(h.seen(), "unbridge") {
		t.Fatal(h.seen())
	}
}

func TestFailedBridgeRemovalIsKeptForARetry(t *testing.T) {
	h := newHarness(t)
	h.s.Setup()
	h.wait("running")
	h.unbrg = connector.Coded("workers_unavailable", "x")
	if e := h.s.Disable(context.Background(), true); connector.ErrorCode(e) != "workers_unavailable" {
		t.Fatal(e)
	}
	if st := h.s.Status(context.Background()); st.State != "off" || st.CleanupHostname != "netbird.example.com" {
		t.Fatalf("%+v", st)
	}
	if _, e := os.Stat(filepath.Join(h.dir, "secrets.json")); e != nil {
		t.Fatal("data removed before the bridge")
	}
	h.unbrg = nil
	if e := h.s.Disable(context.Background(), false); e != nil {
		t.Fatal(e)
	}
	if st := h.s.Status(context.Background()); st.CleanupHostname != "" {
		t.Fatalf("%+v", st)
	}
	if countOf(h.seen(), "unroute netbird.example.com") != 1 || countOf(h.seen(), "unbridge") != 2 {
		t.Fatal(h.seen())
	}
}

func TestRestoreBringsTheBridgeUpToDate(t *testing.T) {
	h := newHarness(t)
	h.s.Setup()
	h.wait("running")
	h.s.Close()
	h.s = h.server()
	t.Cleanup(h.s.Close)
	if e := h.s.Restore(context.Background()); e != nil {
		t.Fatal(e)
	}
	h.wait("running")
	deadline := time.Now().Add(5 * time.Second)
	for countOf(h.seen(), "bridge example.com netbird.example.com") < 2 {
		if time.Now().After(deadline) {
			t.Fatal(h.seen())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestCheckWaitsForGRPCToBeTurnedOn(t *testing.T) {
	h := newHarness(t)
	h.check = connector.Coded("netbird_server_grpc_blocked", "x")
	h.s.Setup()
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(stepStates(h.s.Status(context.Background())), "check=runningnetbird_server_grpc_blocked") {
		if time.Now().After(deadline) {
			t.Fatal(stepStates(h.s.Status(context.Background())))
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Long past the usual check window, it still waits, then continues by
	// itself once gRPC is on.
	time.Sleep(100 * time.Millisecond)
	if st := h.s.Status(context.Background()); st.State != "working" {
		t.Fatalf("%+v", st)
	}
	h.mu.Lock()
	h.check = nil
	h.mu.Unlock()
	h.wait("running")
}

// With the Workers token gone the bridge cannot be deployed. When a token is
// saved again, the next status read brings the bridge back at once instead
// of after the usual retry.
func TestBridgeReturnsSoonAfterTheTokenIsBack(t *testing.T) {
	h := newHarness(t)
	h.s.Setup()
	h.wait("running")
	h.s.Close()
	h.mu.Lock()
	h.zone = connector.Coded("netbird_server_workers_required", "x")
	h.mu.Unlock()
	h.s = h.server()
	t.Cleanup(h.s.Close)
	if e := h.s.Restore(context.Background()); e != nil {
		t.Fatal(e)
	}
	h.wait("running")
	if st := h.s.Status(context.Background()); st.Prerequisite != "netbird_server_workers_required" {
		t.Fatalf("a running server must name the missing token: %+v", st)
	}
	time.Sleep(50 * time.Millisecond)
	before := countOf(h.seen(), "bridge example.com netbird.example.com")
	h.mu.Lock()
	h.zone = nil
	h.mu.Unlock()
	h.s.Status(context.Background())
	deadline := time.Now().Add(2 * time.Second)
	for countOf(h.seen(), "bridge example.com netbird.example.com") == before {
		if time.Now().After(deadline) {
			t.Fatal("bridge not redeployed after the token came back", h.seen())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestEnabledFollowsTheServer(t *testing.T) {
	h := newHarness(t)
	if h.s.Enabled() {
		t.Fatal("enabled before setup")
	}
	h.s.Setup()
	h.wait("running")
	if !h.s.Enabled() {
		t.Fatal("not enabled while running")
	}
	if e := h.s.Disable(context.Background(), false); e != nil {
		t.Fatal(e)
	}
	if h.s.Enabled() {
		t.Fatal("enabled after turning off")
	}
}

func TestSetupDependencyRefusalIsAnObservableFailedStepBeforePersistence(t *testing.T) {
	h := newHarness(t)
	h.s.d.Preflight = func(context.Context) error {
		return connector.Coded("cloudflare_not_connected", "connect Cloudflare first")
	}
	if e := h.s.Setup(); e != nil {
		t.Fatal(e)
	}
	st := h.wait("failed")
	if st.Code != "cloudflare_not_connected" || st.Steps[0].State != "failed" {
		t.Fatal(st)
	}
	if _, e := os.Stat(h.s.p.state()); !os.IsNotExist(e) {
		t.Fatal("refused setup wrote server state", e)
	}
}
