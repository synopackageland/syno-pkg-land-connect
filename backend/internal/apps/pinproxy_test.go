package apps

import (
	"bufio"
	"context"
	"dsmapp/internal/ipctest"
	"dsmapp/internal/localipc"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/ipc"
)

type memoryPinState struct {
	mu      sync.Mutex
	active  bool
	reason  string
	removed bool
	err     error
	writes  int
}

func (s *memoryPinState) Active(PinBinding) bool { s.mu.Lock(); defer s.mu.Unlock(); return s.active }
func (s *memoryPinState) Suspend(_ context.Context, _ PinBinding, reason string, removed bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = false
	s.reason = reason
	s.removed = removed
	s.writes++
	return s.err
}
func (s *memoryPinState) snapshot() (string, bool, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reason, s.removed, s.writes
}

type pinRun struct {
	p                   *PinProxy
	s                   *memoryPinState
	client              *http.Client
	sock, info, gateway string
	stop                func()
}
type pinResponse struct {
	code int
	body string
}

func startPin(t *testing.T, l *pinLab, b PinBinding, provider string, edit func(*PinConfig)) *pinRun {
	t.Helper()
	// Keep disposable metadata separate from the SDK's package-bound socket.
	dir, err := os.MkdirTemp("/tmp", "pin-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	packageDir := filepath.Join(dir, "package")
	if err = os.MkdirAll(packageDir, 0700); err != nil {
		t.Fatal(err)
	}
	info, gateway := filepath.Join(packageDir, "INFO"), filepath.Join(packageDir, "gateway.json")
	if err = os.WriteFile(info, []byte("package=DemoApp"), 0600); err != nil {
		t.Fatal(err)
	}
	writeGateway(t, gateway, b.Port)
	s := &memoryPinState{active: true}
	cfg := PinConfig{Binding: b, Provider: provider, InfoPath: info, GatewayJSON: gateway, ProtectionDigest: "digest", State: s, Verifier: NewVerifier(l.opts)}
	if edit != nil {
		edit(&cfg)
	}
	cfg.Binding.Socket = ipctest.ID(t)
	ctx, cancel := context.WithCancel(context.Background())
	p, err := StartPinProxy(ctx, cfg)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	socket := PinSocket(cfg.Binding.Socket)
	done := make(chan error, 1)
	go func() { done <- p.Wait(); p.Stop() }()
	tr := &http.Transport{MaxIdleConns: 128, MaxIdleConnsPerHost: 64, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return ipc.DialUnix(ctx, socket)
	}}
	run := &pinRun{p: p, s: s, client: &http.Client{Transport: tr, Timeout: 5 * time.Second}, sock: socket, info: info, gateway: gateway}
	var once sync.Once
	run.stop = func() {
		once.Do(func() {
			cancel()
			tr.CloseIdleConnections()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("proxy did not stop")
			}
		})
	}
	t.Cleanup(run.stop)
	return run
}
func writeGateway(t *testing.T, path string, port int) {
	t.Helper()
	raw := fmt.Sprintf(`{"listen":{"https":%d}}`, port)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}
func (r *pinRun) get(path, host string) (pinResponse, error) {
	req, _ := http.NewRequest("GET", "http://localhost"+path, nil)
	if host != "" {
		req.Host = host
	}
	for _, h := range []string{"Forwarded", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Forwarded-For"} {
		req.Header.Set(h, "forged.example.com")
	}
	req.Header.Set(UserMarker, "user-data")
	res, err := r.client.Do(req)
	if err != nil {
		return pinResponse{}, err
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	return pinResponse{res.StatusCode, string(body)}, err
}
func goodObservation(b PinBinding) PinObservation {
	return PinObservation{DSMPort: 5001, MagicDNS: "nas.example.com.", ProtectionDigest: "digest", Ports: map[string]int{b.Package: b.Port}}
}

func TestHostAndReuse(t *testing.T) {
	for _, provider := range []string{"tailscale", "cloudflared"} {
		t.Run(provider, func(t *testing.T) {
			l := newPinLab(t)
			b := l.binding()
			b.Authority = "nas.example.com:443"
			if provider == "cloudflared" {
				b.Authority = "nas.example.com"
			}
			r := startPin(t, l, b, provider, nil)
			for i := 0; i < 2; i++ {
				host := "localhost"
				if provider == "cloudflared" {
					host = "NAS.example.com"
				}
				res, err := r.get("/", host)
				if err != nil || res.code != 200 {
					t.Fatalf("%+v %v", res, err)
				}
				var body map[string]any
				if err = json.Unmarshal([]byte(res.body), &body); err != nil {
					t.Fatal(err)
				}
				want := b.Authority
				if provider == "cloudflared" {
					want = host
				}
				if body["host"] != want {
					t.Fatal(body)
				}
				for _, h := range []string{"forwarded", "x_forwarded_host", "x_forwarded_proto", "x_forwarded_for"} {
					if v := body[h]; v != nil {
						t.Fatalf("leaked %s: %v", h, v)
					}
				}
			}
			if l.app.Hellos.Load() != 1 {
				t.Fatalf("connection not reused: %d", l.app.Hellos.Load())
			}
			if l.app.SNI.Load() || l.app.Resumed.Load() {
				t.Fatal("SNI/resumption enabled")
			}
			r.p.tr.CloseIdleConnections()
			if res, err := r.get("/", "nas.example.com"); err != nil || res.code != 200 {
				t.Fatalf("second handshake: %+v %v", res, err)
			}
			if l.app.Hellos.Load() != 2 || l.app.Resumed.Load() {
				t.Fatal("new connection did not verify")
			}
			if provider == "cloudflared" {
				before := l.app.AppBytes()
				for _, host := range []string{"localhost", "else.example.com", "nas.example.com:444"} {
					res, err := r.get("/", host)
					if err != nil || res.code != 421 {
						t.Fatalf("421: %+v %v", res, err)
					}
				}
				if l.app.AppBytes() != before {
					t.Fatal("wrong authority reached upstream")
				}
			}
		})
	}
	for _, pair := range [][2]string{{"a.example.com", "A.example.com:443"}, {"[::1]", "[::1]:443"}} {
		if !SameAuthority(pair[0], pair[1]) {
			t.Fatal(pair)
		}
	}
	for _, s := range []string{"", ":443", "a.example.com:", "a.example.com:99999", "a.example.com/path", "a.example.com@other", "a.example.com\r\nHost: forged", "::1", "a.example.com?x"} {
		if SameAuthority(s, s) {
			t.Fatalf("invalid authority %q", s)
		}
	}
}

func TestPingTransientAndMismatch(t *testing.T) {
	for _, code := range []int{502, 503, 504, 401, 404} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			l := newPinLab(t)
			b := l.binding()
			l.app.PingStatus.Store(int64(code))
			r := startPin(t, l, b, "tailscale", nil)
			res, err := r.get("/", "")
			if err == nil && res.code == 200 {
				t.Fatal("bad ping forwarded request")
			}
			reason, _, writes := r.s.snapshot()
			if code >= 502 && code <= 504 {
				if writes != 0 || reason != "" || err != nil || res.code != http.StatusServiceUnavailable || !strings.Contains(res.body, "Retry") {
					t.Fatalf("transient: %+v %v %s", res, err, reason)
				}
				l.app.PingStatus.Store(200)
				if res, err = r.get("/", ""); err != nil || res.code != 200 {
					t.Fatalf("did not recover: %+v %v", res, err)
				}
			} else if writes != 1 || reason != "route_mismatch" {
				t.Fatalf("permanent %s %d", reason, writes)
			}
			// Ping may arrive, but the user's marked request never does.
			if code < 502 {
				for _, c := range l.app.Records() {
					if c.UserRequest {
						t.Fatal("user request followed bad ping")
					}
				}
			}
		})
	}
	t.Run("wrong-package", func(t *testing.T) {
		l := newPinLab(t)
		l.app.Package = "OtherApp"
		r := startPin(t, l, l.binding(), "tailscale", nil)
		r.get("/", "")
		reason, _, _ := r.s.snapshot()
		if reason != "route_mismatch" {
			t.Fatal(reason)
		}
	})
}

func TestUpgradeTimingReplay(t *testing.T) {
	l := newPinLab(t)
	b := l.binding()
	r := startPin(t, l, b, "tailscale", nil)
	o := goodObservation(b)
	mustWait := func() {
		t.Helper()
		res, err := r.get("/", "")
		if err != nil || res.code != http.StatusServiceUnavailable {
			t.Fatalf("waiting: %+v %v", res, err)
		}
		_, _, n := r.s.snapshot()
		if n != 0 {
			t.Fatal("transient persisted pause")
		}
	}
	if err := os.Remove(r.info); err != nil {
		t.Fatal(err)
	}
	mustWait() // observed INFO absence lasted 0.2–0.6 seconds, not 60 seconds
	if err := r.p.Check(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(r.info, []byte("restored"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(r.gateway); err != nil {
		t.Fatal(err)
	}
	mustWait() // gateway absence lasted 0.4–0.9 seconds
	writeGateway(t, r.gateway, b.Port)
	l.app.PingStatus.Store(502)
	r.p.tr.CloseIdleConnections()
	mustWait() // nginx stop/upgrade transient
	l.app.PingStatus.Store(200)
	if err := r.p.Check(context.Background(), o); err != nil {
		t.Fatal(err)
	}
	if res, err := r.get("/", ""); err != nil || res.code != 200 {
		t.Fatalf("same-port upgrade failed: %+v %v", res, err)
	}
	// Atomic replacement must invalidate the inode cache even when size is equal.
	writeGateway(t, r.gateway, b.Port+1)
	r.get("/", "")
	reason, _, n := r.s.snapshot()
	if reason != "port_changed" || n != 1 {
		t.Fatalf("changed port: %s %d", reason, n)
	}
}

func TestRemovalRequiresTwoPolls(t *testing.T) {
	l := newPinLab(t)
	r := startPin(t, l, l.binding(), "tailscale", nil)
	o := goodObservation(l.binding())
	os.Remove(r.info)
	r.p.Check(context.Background(), o)
	if _, _, n := r.s.snapshot(); n != 0 {
		t.Fatal("first missing INFO removed target")
	}
	os.WriteFile(r.info, []byte("back"), 0600)
	r.p.Check(context.Background(), o)
	os.Remove(r.info)
	r.p.Check(context.Background(), o)
	if _, _, n := r.s.snapshot(); n != 0 {
		t.Fatal("nonconsecutive misses counted")
	}
	r.p.Check(context.Background(), o)
	if _, _, n := r.s.snapshot(); n != 0 {
		t.Fatal("upgrade metadata gap removed package")
	}
	os.RemoveAll(filepath.Dir(r.info))
	r.p.Check(context.Background(), o)
	if _, _, n := r.s.snapshot(); n != 0 {
		t.Fatal("first absent package poll removed target")
	}
	r.p.Check(context.Background(), o)
	reason, removed, n := r.s.snapshot()
	if reason != "removed" || !removed || n != 1 {
		t.Fatalf("%s %t %d", reason, removed, n)
	}
}

func TestPinMismatchBoundedWait(t *testing.T) {
	l := newPinLab(t)
	b := l.binding()
	r := startPin(t, l, b, "tailscale", nil)
	ctx := context.Background()
	next := l.leaf(NewKey(), approvedNames, true)
	l.app.SetCert(next)
	for _, d := range []time.Duration{0, 119 * time.Second} {
		l.clk.Add(d)
		r.get("/", "")
		if _, _, n := r.s.snapshot(); n != 0 {
			t.Fatal("paused early")
		}
	}
	// Consistency restored before 120 seconds clears the timer without a click.
	l.ref.SetCert(next)
	if res, err := r.get("/", ""); err != nil || res.code != 200 {
		t.Fatalf("renewal failed %+v %v", res, err)
	}
	l.app.SetCert(l.leaf(NewKey(), approvedNames, true))
	r.p.tr.CloseIdleConnections()
	r.p.Check(ctx, goodObservation(b))
	l.clk.Add(120 * time.Second)
	r.p.Check(ctx, goodObservation(b))
	reason, _, n := r.s.snapshot()
	if reason != "cert_mismatch" || n != 1 {
		t.Fatalf("120 second boundary: %s %d", reason, n)
	}
	l.app.SetCert(next)
	if r.p.active() {
		t.Fatal("paused proxy automatically re-enabled")
	}
}

func TestUpstreamBlackholeHandshakeTimeout(t *testing.T) {
	l := newPinLab(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, e := ln.Accept()
		if e == nil {
			accepted <- c
		}
	}()
	b := l.binding()
	b.Port = ln.Addr().(*net.TCPAddr).Port
	r := startPin(t, l, b, "tailscale", func(c *PinConfig) {
		o := l.opts
		o.HandshakeTimeout = 100 * time.Millisecond
		c.Verifier = NewVerifier(o)
	})
	before := time.Now()
	res, err := r.get("/", "")
	c := <-accepted
	defer c.Close()
	if err != nil || res.code != http.StatusServiceUnavailable || !strings.Contains(res.body, "Retry") || time.Since(before) > time.Second {
		t.Fatalf("blackhole: %+v %v %s", res, err, time.Since(before))
	}
	if _, _, n := r.s.snapshot(); n != 0 {
		t.Fatal("timeout persisted pause")
	}
}

func TestSocketSafety(t *testing.T) {
	ipctest.Require(t)
	for _, id := range []string{"../app", "app.sock", "", "app/child"} {
		if PinSocket(id) != "" {
			t.Fatal(id)
		}
	}
	path := PinSocket(ipctest.ID(t))
	if err := os.WriteFile(path, []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(path)
	if err := localipc.Prepare(path); err == nil {
		t.Fatal("regular file unlinked")
	}
	if raw, _ := os.ReadFile(path); string(raw) != "preserve" {
		t.Fatal("file modified")
	}
	os.Remove(path)
	if err := os.Symlink(t.TempDir(), path); err != nil {
		t.Fatal(err)
	}
	if err := localipc.Prepare(path); err == nil {
		t.Fatal("socket symlink accepted")
	}
	if err := os.Chmod(localipc.RunDir, 0755); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(localipc.RunDir, 0700)
	if err := localipc.Prepare(path); err == nil {
		t.Fatal("unsafe directory accepted")
	}
}

// Streams flush immediately; WebSockets use a real HTTP upgrade/hijack. Closing
// the underlying sockets must finish both readers, for every pause trigger.
func TestEveryPauseClosesStreamsAndWebSockets(t *testing.T) {
	for _, reason := range []string{"cert_mismatch", "route_mismatch", "port_changed", "authority_changed", "protection_changed", "dsm_port_changed", "duplicate_port", "removed", "revoke", "storage_error"} {
		for _, kind := range []string{"stream", "websocket"} {
			t.Run(reason+"/"+kind, func(t *testing.T) {
				l := newPinLab(t)
				// Serve the diagnostic and long response from the same TLS connection.
				l.app.Close()
				l.app = &LabServer{Name: "long", Package: "DemoApp"}
				l.app.SetCert(l.leaf(l.key, approvedNames, true))
				l.app.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/_spl/ping" {
						if code := l.app.PingStatus.Load(); code != 0 && code != 200 {
							w.WriteHeader(int(code))
							return
						}
						io.WriteString(w, `{"package":"DemoApp"}`)
						return
					}
					if kind == "stream" {
						w.Header().Set("Content-Type", "text/event-stream")
						io.WriteString(w, "data: ready\n\n")
						w.(http.Flusher).Flush()
						<-r.Context().Done()
						return
					}
					c, br, err := w.(http.Hijacker).Hijack()
					if err != nil {
						return
					}
					defer c.Close()
					io.WriteString(c, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
					io.Copy(c, br)
				})
				var err error
				l.ap, err = l.app.Start("127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(l.app.Close)
				b := l.binding()
				if reason == "dsm_port_changed" {
					b.Target = TargetDSM
				}
				run := startPin(t, l, b, "tailscale", nil)
				c, err := net.Dial("unix", run.sock)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				c.SetDeadline(time.Now().Add(2 * time.Second))
				upgrade := ""
				if kind == "websocket" {
					upgrade = "Upgrade: websocket\r\nConnection: Upgrade\r\n"
				}
				fmt.Fprintf(c, "GET /long HTTP/1.1\r\nHost: localhost\r\n%s\r\n", upgrade)
				br := bufio.NewReader(c)
				req, _ := http.NewRequest("GET", "http://localhost/long", nil)
				res, err := http.ReadResponse(br, req)
				if err != nil {
					t.Fatal(err)
				}
				if kind == "stream" {
					line, err := bufio.NewReader(res.Body).ReadString('\n')
					if err != nil || line != "data: ready\n" {
						t.Fatalf("not flushed: %q %v", line, err)
					}
				} else if res.StatusCode != 101 {
					t.Fatal(res.StatusCode)
				}
				o := goodObservation(b)
				ctx := context.Background()
				switch reason {
				case "port_changed":
					writeGateway(t, run.gateway, b.Port+1)
					run.p.Check(ctx, o)
				case "authority_changed":
					o.MagicDNS = "renamed.example.com"
					run.p.Check(ctx, o)
				case "protection_changed":
					o.ProtectionDigest = "changed"
					run.p.Check(ctx, o)
				case "dsm_port_changed":
					o.DSMPort = b.Port + 1
					run.p.Check(ctx, o)
				case "duplicate_port":
					o.Ports["OtherApp"] = b.Port
					run.p.Check(ctx, o)
				case "removed":
					os.RemoveAll(filepath.Dir(run.info))
					run.p.Check(ctx, o)
					run.p.Check(ctx, o)
				case "revoke":
					run.s.Suspend(ctx, b, "revoked", false)
					run.p.Stop()
				case "storage_error":
					run.s.err = errors.New("storage unavailable")
					if run.p.Pause(ctx, reason, false) == nil {
						t.Fatal("missing storage error")
					}
				case "cert_mismatch":
					l.app.SetCert(l.leaf(NewKey(), approvedNames, true))
					run.p.Check(ctx, o)
					l.clk.Add(120 * time.Second)
					run.p.Check(ctx, o)
				case "route_mismatch":
					l.app.PingStatus.Store(404)
					run.p.tr.CloseIdleConnections()
					run.get("/trigger", "")
				}
				// Read any final chunk bytes and require closure before the deadline.
				_, err = io.Copy(io.Discard, br)
				var ne net.Error
				if errors.As(err, &ne) && ne.Timeout() {
					t.Fatalf("%s left %s open", reason, kind)
				}
				if reason != "revoke" {
					got, _, _ := run.s.snapshot()
					if got != reason {
						t.Fatalf("wrong pause: %s", got)
					}
				}
			})
		}
	}
}

func TestNativeIngressProxyKeepsPackagePingAndDeniesDSM(t *testing.T) {
	l := newPinLab(t)
	l.app.Close()
	var callsMu sync.Mutex
	calls := []string{}
	l.app.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callsMu.Lock()
		calls = append(calls, r.URL.Path)
		callsMu.Unlock()
		if r.URL.Path == "/music/_spl/ping" {
			io.WriteString(w, `{"package":"DemoApp"}`)
			return
		}
		io.WriteString(w, "selected app")
	})
	var err error
	l.ap, err = l.app.Start("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	b := l.binding()
	b.Authority = "music.example.com"
	b.EntryPath = "/music/"
	run := startPin(t, l, b, "cloudflared", func(c *PinConfig) {
		raw, _ := json.Marshal(map[string]any{"package": "DemoApp", "ingress_path": "/music/", "listen": map[string]int{}})
		if err := os.WriteFile(c.GatewayJSON, raw, 0644); err != nil {
			t.Fatal(err)
		}
	})
	for _, path := range []string{"/music/app/", "/music/rest/api", "/webman/3rdparty/DemoApp/sso/authorize.cgi"} {
		res, err := run.get(path, b.Authority)
		if err != nil || res.code != 200 {
			t.Fatalf("%s %+v %v", path, res, err)
		}
	}
	callsMu.Lock()
	before := len(calls)
	callsMu.Unlock()
	for _, path := range []string{"/webapi/entry.cgi?api=SYNO.Core.Package&method=uninstall", "/webman/3rdparty/Other/sso/open.cgi", "/synopkgland/Other/", "/music/%2e%2e/webman/"} {
		res, err := run.get(path, b.Authority)
		if err != nil || res.code != 404 {
			t.Fatalf("not isolated %s %+v %v", path, res, err)
		}
	}
	callsMu.Lock()
	defer callsMu.Unlock()
	if len(calls) != before {
		t.Fatal("refused request reached DSM")
	}
	found := false
	for _, path := range calls {
		if path == "/music/_spl/ping" {
			found = true
		}
		if path == "/_spl/ping" {
			t.Fatal("unscoped ping")
		}
	}
	if !found {
		t.Fatal("package identity not verified on connection")
	}
}
