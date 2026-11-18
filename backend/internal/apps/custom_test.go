package apps

import (
	"bufio"
	"context"
	"dsmapp/internal/ipctest"
	"github.com/synopackageland/syno-pkg-land-sdk/ipc"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestCustomTargetAcceptsLANAndLoopbackOrigins(t *testing.T) {
	for raw, want := range map[string]string{
		"http://127.0.0.1:32400":         "http://127.0.0.1:32400",
		"HTTP://Media.Example.com:8080/": "http://media.example.com:8080",
		"https://192.168.1.20":           "https://192.168.1.20",
		"https://192.168.1.20:443":       "https://192.168.1.20",
		"http://[::1]:8096":              "http://[::1]:8096",
		"http://10.0.0.5:80":             "http://10.0.0.5",
		" http://nas.local:5050 ":        "http://nas.local:5050",
	} {
		u, err := ParseTarget(raw)
		if err != nil || u.String() != want {
			t.Errorf("%q: %v %v, want %s", raw, u, err, want)
		}
	}
}

func TestCustomTargetRefusesAmbiguousPathsSchemesAndRefusedRanges(t *testing.T) {
	for raw, code := range map[string]string{
		"":                                       "custom_url_invalid",
		"127.0.0.1:8080":                         "custom_url_invalid",
		"ftp://127.0.0.1":                        "custom_url_scheme",
		"file:///etc/passwd":                     "custom_url_scheme",
		"http://user:pw@127.0.0.1":               "custom_url_invalid",
		"http://127.0.0.1:8080/%0D%0A":           "custom_url_path",
		"http://127.0.0.1:8080/%5Cweb":           "custom_url_path",
		"http://127.0.0.1:8080/photo/../webman/": "custom_url_path",
		"http://127.0.0.1:8080//web":             "custom_url_path",
		"http://127.0.0.1:8080/?a=1":             "custom_url_path",
		"http://127.0.0.1:8080/#top":             "custom_url_path",
		"http://127.0.0.1:0":                     "custom_url_invalid",
		"http://127.0.0.1:65536":                 "custom_url_invalid",
		"http://169.254.169.254":                 "custom_target_refused",
		"http://169.254.10.1:8080":               "custom_target_refused",
		"http://[fe80::1]:8080":                  "custom_target_refused",
		"http://[fd00:ec2::254]":                 "custom_target_refused",
		"http://[::ffff:169.254.169.254]/":       "custom_target_refused",
		"http://0.0.0.0:8080":                    "custom_target_refused",
		"http://[::]:8080":                       "custom_target_refused",
		"http://224.0.0.1":                       "custom_target_refused",
		"http://255.255.255.255":                 "custom_target_refused",
		"http://bad_host:80":                     "custom_url_invalid",
	} {
		if _, err := ParseTarget(raw); ErrorCode(err) != code {
			t.Errorf("%q: %v, want %s", raw, ErrorCode(err), code)
		}
	}
}

func TestCustomGuardRefusesResolvedRangesAndConnectsOwnPorts(t *testing.T) {
	g := TargetGuard{OwnPorts: func() map[int]bool { return map[int]bool{8443: true} }, LocalAddrs: func() []netip.Addr { return []netip.Addr{netip.MustParseAddr("192.168.1.2")} }}
	ctx := context.Background()
	for address, ok := range map[string]bool{
		"127.0.0.1:8080":       true,
		"192.168.1.9:8443":     true, // another host's port is not Connect's
		"127.0.0.1:8443":       false,
		"192.168.1.2:8443":     false,
		"[::1]:8443":           false,
		"169.254.169.254:80":   false,
		"[fe80::1%25eth0]:80":  false,
		"[fe80::1]:80":         false,
		"[::ffff:a9fe:a9fe]:1": false,
	} {
		_, err := g.Check(ctx, address)
		if (err == nil) != ok {
			t.Errorf("%s: %v", address, err)
		}
	}
	// localhost resolves through the resolver; loopback stays allowed.
	if _, err := g.Check(ctx, "localhost:8080"); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Check(ctx, "localhost:8443"); ErrorCode(err) != "custom_target_refused" {
		t.Fatal("own port through a name", err)
	}
}

func TestCustomNamesFollowTheReadableLadder(t *testing.T) {
	s := EmptyState()
	for _, c := range []struct{ name, want string }{
		{"Plex", "custom.plex"}, {"plex", "custom.plex-2"}, {"PLEX!", "custom.plex-3"},
		{"Home Assistant", "custom.home-assistant"}, {"日本語", "custom.app"}, {"", "custom.app-2"},
		{strings.Repeat("a", 60), "custom." + strings.Repeat("a", 40)},
		{strings.Repeat("a", 60) + "x", "custom." + strings.Repeat("a", 38) + "-2"},
	} {
		id, err := NewCustomID(s, c.name)
		if err != nil || id != c.want || !customIDPattern.MatchString(id) {
			t.Fatalf("%q: %s %v, want %s", c.name, id, err, c.want)
		}
		if s.Custom == nil {
			s.Custom = map[string]CustomApp{}
		}
		s.Custom[id] = CustomApp{Name: "x", URL: "http://127.0.0.1:1"}
	}
	for _, hex := range []string{"custom.a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4"} {
		if CustomLabel("A1B2-C3D4") == hex {
			t.Fatal("hex label")
		}
	}
}

func TestCustomStateAddEditRemoveKeepsTheStoreValid(t *testing.T) {
	s := EmptyState()
	if _, err := s.AddCustom(CustomRequest{Name: "Media", URL: "http://127.0.0.1:8096", AppSignIn: true}); ErrorCode(err) != "custom_risk_unconfirmed" {
		t.Fatal("app sign-in saved without the risk confirmation", err)
	}
	id, err := s.AddCustom(CustomRequest{Name: " Media ", URL: "http://127.0.0.1:8096/"})
	if err != nil || id != "custom.media" || s.Custom[id].Name != "Media" || s.Custom[id].URL != "http://127.0.0.1:8096" || s.Custom[id].AppSignIn {
		t.Fatal(id, err, s.Custom)
	}
	if err = s.Validate(); err != nil {
		t.Fatal(err)
	}
	// An approved public scope follows an edit under the same socket.
	def := s.Custom[id]
	rev, _ := s.Revision()
	s.Apps[id] = AppState{Rev: rev, Public: &Scope{Provider: Workers, State: Active, Origin: "https://media.example.workers.dev", Socket: "a" + strings.Repeat("0", 32), Authority: "media.example.workers.dev",
		Binding: Binding{Port: def.Port(), Summary: def.Digest(), Pin: Pin{Mode: string(ModeCustom)}}}}
	if err = s.Validate(); err != nil {
		t.Fatal(err)
	}
	if old, err := s.EditCustom(CustomRequest{ID: id, Name: "Jellyfin", URL: "http://127.0.0.1:8096"}); err != nil || old != nil {
		t.Fatal("a rename restarts nothing", old, err)
	}
	old, err := s.EditCustom(CustomRequest{ID: id, Name: "Jellyfin", URL: "https://192.168.1.20:8920", InsecureTLS: true})
	if err != nil || old == nil || s.Apps[id].Public.Binding.Port != 8920 || s.Apps[id].Public.Binding.Summary != s.Custom[id].Digest() || s.Apps[id].Public.Socket != old.Socket {
		t.Fatal(old, err)
	}
	if err = s.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.EditCustom(CustomRequest{ID: id, Name: "Jellyfin", URL: "https://192.168.1.20:8920", AppSignIn: true}); ErrorCode(err) != "custom_risk_unconfirmed" {
		t.Fatal(err)
	}
	if _, err = s.EditCustom(CustomRequest{ID: id, Name: "Jellyfin", URL: "https://192.168.1.20:8920", AppSignIn: true, AcceptRisk: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.EditCustom(CustomRequest{ID: id, Name: "Jellyfin", URL: "https://192.168.1.20:8920", AppSignIn: true}); err != nil {
		t.Fatal("keeping app sign-in needs no new confirmation", err)
	}
	publication := CustomPublication{Authority: s.Apps[id].Public.Authority, Socket: s.Apps[id].Public.Socket}
	def = s.Custom[id]
	def.Published = &publication
	s.Custom[id] = def
	// Tampered bindings and SDK-style pins on a custom ID are corrupt.
	bad := s
	bad.Apps = map[string]AppState{id: {Rev: s.Apps[id].Rev, Public: &Scope{}}}
	*bad.Apps[id].Public = *s.Apps[id].Public
	bad.Apps[id].Public.Binding.Pin.Mode = string(ModeS)
	if bad.Validate() == nil {
		t.Fatal("custom App with a certificate pin accepted")
	}
	// Closing public access removes the active scope but retains the exact
	// publication evidence needed by a later custom-App deletion.
	app := s.Apps[id]
	app.Public = nil
	delete(s.Apps, id)
	if _, err = s.RemoveCustom(id); err != nil {
		t.Fatal(err)
	}
	sc := s.Apps[id].Public
	if _, ok := s.Custom[id]; ok || sc == nil || sc.State != Paused || sc.Reason != "removed" || !sc.Cleanup || s.Validate() != nil {
		t.Fatal("removal must keep cleanup intent", sc, s.Validate())
	}
	s.DropRemovedCustom()
	if _, ok := s.Apps[id]; !ok {
		t.Fatal("dropped before cleanup")
	}
	sc.Cleanup = false
	s.DropRemovedCustom()
	if _, ok := s.Apps[id]; ok || s.Validate() != nil {
		t.Fatal("removed App kept after cleanup")
	}
	if _, err = s.RemoveCustom(id); ErrorCode(err) != "custom_not_found" {
		t.Fatal(err)
	}
}

func TestCustomLimit(t *testing.T) {
	s := EmptyState()
	for i := range MaxCustom {
		if _, err := s.AddCustom(CustomRequest{Name: "App " + strconv.Itoa(i), URL: "http://127.0.0.1:" + strconv.Itoa(1000+i)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.AddCustom(CustomRequest{Name: "More", URL: "http://127.0.0.1:9"}); ErrorCode(err) != "custom_limit" {
		t.Fatal(err)
	}
}

type customActive struct{ on atomic.Bool }

func (c *customActive) Active(PinBinding) bool { return c.on.Load() }

type customLab struct {
	t       *testing.T
	backend *httptest.Server
	hits    atomic.Int32
	seen    chan *http.Request
	app     CustomApp
	state   *customActive
	proxy   *CustomProxy
	socket  string
	client  *http.Client
}

const customAuthority = "media.example.com"

func newCustomLab(t *testing.T, appSignIn bool, own bool, backend http.HandlerFunc) *customLab {
	l := &customLab{t: t, seen: make(chan *http.Request, 16), state: &customActive{}}
	l.state.on.Store(true)
	l.backend = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l.hits.Add(1)
		select {
		case l.seen <- r.Clone(context.Background()):
		default:
		}
		backend(w, r)
	}))
	t.Cleanup(l.backend.Close)
	l.app = CustomApp{Name: "Media", URL: l.backend.URL, AppSignIn: appSignIn}
	port := l.app.Port()
	guard := TargetGuard{OwnPorts: func() map[int]bool { return map[int]bool{port: own} }}
	cfg := CustomConfig{Binding: PinBinding{Provider: Workers, Package: "custom.media", Target: TargetApp, Scope: Public, Socket: "a" + strings.Repeat("1", 32), Port: port, Summary: l.app.Digest(), Authority: customAuthority, Mode: ModeCustom},
		App: l.app, State: l.state, Guard: guard}
	if !appSignIn {
		cfg.Gate = &Gate{Login: func(_ context.Context, account, password, otp string) (string, string) {
			if account == "alice" && password == "secret" {
				return "alice", ""
			}
			return "", "invalid_credentials"
		}}
	}
	cfg.Binding.Socket = ipctest.ID(t)
	p, err := StartCustomProxy(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	l.proxy = p
	l.socket = PinSocket(cfg.Binding.Socket)
	l.client = unixClient(l.socket)
	l.client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	t.Cleanup(l.client.CloseIdleConnections)
	t.Cleanup(p.Stop)
	return l
}

func (l *customLab) do(method, path string, header http.Header, body string) *http.Response {
	l.t.Helper()
	req, _ := http.NewRequest(method, "http://connect"+path, strings.NewReader(body))
	req.Host = customAuthority
	for k, v := range header {
		req.Header[k] = v
	}
	res, err := l.client.Do(req)
	if err != nil {
		l.t.Fatal(err)
	}
	l.t.Cleanup(func() { res.Body.Close() })
	return res
}

func TestCustomProxyNeedsTheGateUnlessTheAppSignsIn(t *testing.T) {
	app := CustomApp{Name: "Media", URL: "http://127.0.0.1:8096"}
	b := PinBinding{Package: "custom.media", Scope: Public, Mode: ModeCustom, Summary: app.Digest(), Port: 8096, Authority: customAuthority}
	if _, err := NewCustomProxy(CustomConfig{Binding: b, App: app, State: &customActive{}}); err == nil {
		t.Fatal("gate-on App started without a gate")
	}
	for _, bad := range []PinBinding{
		{Package: "ExampleApp", Scope: Public, Mode: ModeCustom, Summary: app.Digest(), Port: 8096, Authority: customAuthority},
		{Package: "custom.media", Scope: Private, Mode: ModeCustom, Summary: app.Digest(), Port: 8096, Authority: customAuthority},
		{Package: "custom.media", Scope: Public, Mode: ModeS, Summary: app.Digest(), Port: 8096, Authority: customAuthority},
		{Package: "custom.media", Scope: Public, Mode: ModeCustom, Summary: "other", Port: 8096, Authority: customAuthority},
		{Package: "custom.media", Scope: Public, Mode: ModeCustom, Summary: app.Digest(), Port: 8097, Authority: customAuthority},
	} {
		if _, err := NewCustomProxy(CustomConfig{Binding: bad, App: app, State: &customActive{}, Gate: &Gate{}}); err == nil {
			t.Errorf("accepted %+v", bad)
		}
	}
}

func TestCustomProxyPinsTheOriginAndStripsClientAddresses(t *testing.T) {
	l := newCustomLab(t, true, false, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })
	raw := func(target string) *http.Response {
		conn, err := ipc.DialUnix(context.Background(), l.socket)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		io.WriteString(conn, "GET "+target+" HTTP/1.1\r\nHost: "+customAuthority+"\r\nX-Forwarded-For: 203.0.113.9\r\nCf-Connecting-Ip: 203.0.113.9\r\nForwarded: for=203.0.113.9\r\nConnection: close\r\n\r\n")
		res, err := http.ReadResponse(bufio.NewReader(conn), nil)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	// An absolute-form request line naming another host is refused.
	if res := raw("http://attacker.example.com/data"); res.StatusCode != http.StatusMisdirectedRequest || l.hits.Load() != 0 {
		t.Fatal("absolute-form target reached the App", res.StatusCode)
	}
	res := raw("http://" + customAuthority + "/data?x=1")
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	r := <-l.seen
	backend, _ := url.Parse(l.backend.URL)
	if r.Host != backend.Host || r.URL.Path != "/data" || r.URL.RawQuery != "x=1" || r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("Cf-Connecting-Ip") != "" || r.Header.Get("Forwarded") != "" ||
		r.Header.Get("X-Forwarded-Host") != customAuthority || r.Header.Get("X-Forwarded-Proto") != "https" {
		t.Fatalf("upstream saw %s %s %v", r.Host, r.URL, r.Header)
	}
	// The Host header never chooses a target.
	req, _ := http.NewRequest("GET", "http://connect/", nil)
	req.Host = "attacker.example.com"
	res, err := l.client.Do(req)
	if err != nil || res.StatusCode != http.StatusMisdirectedRequest {
		t.Fatal(res, err)
	}
	res.Body.Close()
	if l.hits.Load() != 1 {
		t.Fatal("misdirected request reached the App")
	}
	// A paused approval stops forwarding at once.
	l.state.on.Store(false)
	req, _ = http.NewRequest("GET", "http://connect/", nil)
	req.Host = customAuthority
	res, err = l.client.Do(req)
	if err == nil {
		res.Body.Close()
		if res.StatusCode != http.StatusServiceUnavailable {
			t.Fatal(res.StatusCode)
		}
	}
	if l.hits.Load() != 1 {
		t.Fatal("paused App still forwarded")
	}
}

func TestCustomProxyNeverFollowsRedirects(t *testing.T) {
	var self string
	l := newCustomLab(t, true, false, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/away":
			http.Redirect(w, r, "http://attacker.example.com/steal", http.StatusFound)
		case "/self":
			http.Redirect(w, r, self+"/next?a=1", http.StatusFound)
		default:
			io.WriteString(w, "ok")
		}
	})
	self = l.backend.URL
	res := l.do("GET", "/away", nil, "")
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "http://attacker.example.com/steal" || l.hits.Load() != 1 {
		t.Fatal("redirect followed or rewritten", res.StatusCode, res.Header.Get("Location"), l.hits.Load())
	}
	res = l.do("GET", "/self", nil, "")
	if res.StatusCode != http.StatusFound || res.Header.Get("Location") != "https://"+customAuthority+"/next?a=1" || l.hits.Load() != 2 {
		t.Fatal("own-origin redirect not mapped to the public address", res.Header.Get("Location"))
	}
}

func TestCustomProxyRefusesConnectsOwnPort(t *testing.T) {
	l := newCustomLab(t, true, true, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") })
	if res := l.do("GET", "/", nil, ""); res.StatusCode != http.StatusForbidden || l.hits.Load() != 0 {
		t.Fatal("dialed one of Connect's own ports", res.StatusCode)
	}
}

func TestCustomGateOnRequiresDSMSignIn(t *testing.T) {
	l := newCustomLab(t, false, false, func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: gateCookie, Value: "app-forged"})
		http.SetCookie(w, &http.Cookie{Name: "app", Value: "2"})
		io.WriteString(w, "ok")
	})
	page := http.Header{"Accept": {"text/html"}}
	res := l.do("GET", "/library?id=1", page, "")
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != gateSignIn+"?next=%2Flibrary%3Fid%3D1" {
		t.Fatal(res.StatusCode, res.Header.Get("Location"))
	}
	if res := l.do("GET", "/api/items", nil, ""); res.StatusCode != http.StatusUnauthorized {
		t.Fatal("API call without session", res.StatusCode)
	}
	if res := l.do("GET", gateSignIn, page, ""); res.StatusCode != 200 || res.Header.Get("Content-Security-Policy") == "" || res.Header.Get("X-Frame-Options") != "DENY" {
		t.Fatal("sign-in page", res.StatusCode, res.Header)
	}
	form := http.Header{"Content-Type": {"application/x-www-form-urlencoded"}, "Origin": {"https://" + customAuthority}}
	if res := l.do("POST", gateSignIn, http.Header{"Content-Type": form["Content-Type"], "Origin": {"https://attacker.example.com"}}, "account=alice&password=secret"); res.StatusCode != http.StatusForbidden {
		t.Fatal("cross-site sign-in accepted", res.StatusCode)
	}
	if res := l.do("POST", gateSignIn, form, "account=alice&password=wrong"); res.StatusCode != http.StatusUnauthorized {
		t.Fatal(res.StatusCode)
	}
	if l.hits.Load() != 0 {
		t.Fatal("App reached before sign-in")
	}
	res = l.do("POST", gateSignIn, form, "account=alice&password=secret&next=%2Flibrary")
	var token string
	for _, c := range res.Cookies() {
		if c.Name == gateCookie {
			token = c.Value
			if !c.Secure || !c.HttpOnly || c.Path != "/" || c.Domain != "" {
				t.Fatal("cookie attributes", c)
			}
		}
	}
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/library" || len(token) != 64 {
		t.Fatal(res.StatusCode, res.Header)
	}
	res = l.do("GET", "/library", http.Header{"Cookie": {gateCookie + "=" + token + "; app=1"}}, "")
	if res.StatusCode != 200 {
		t.Fatal(res.StatusCode)
	}
	r := <-l.seen
	if got := r.Header.Get("Cookie"); got != "app=1" {
		t.Fatal("Connect's cookie reached the App:", got)
	}
	for _, c := range res.Cookies() {
		if c.Name == gateCookie {
			t.Fatal("App set Connect's cookie")
		}
	}
	// Signing out ends the session for this App.
	l.do("GET", gateSignOut, http.Header{"Cookie": {gateCookie + "=" + token}}, "")
	if res := l.do("GET", "/library", http.Header{"Cookie": {gateCookie + "=" + token}}, ""); res.StatusCode != http.StatusUnauthorized {
		t.Fatal("session survived sign-out", res.StatusCode)
	}
}

func TestCustomGateOffForwardsWithoutConnectSignIn(t *testing.T) {
	l := newCustomLab(t, true, false, func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "app login page") })
	res := l.do("GET", "/", http.Header{"Accept": {"text/html"}}, "")
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || string(body) != "app login page" {
		t.Fatal(res.StatusCode, string(body))
	}
	// With the gate off, Connect's paths belong to the App.
	if res := l.do("GET", gateSignIn, nil, ""); res.StatusCode != 200 || l.hits.Load() != 2 {
		t.Fatal(res.StatusCode)
	}
}

func TestCustomGateRateLimitsSignIn(t *testing.T) {
	calls := 0
	g := &Gate{Login: func(context.Context, string, string, string) (string, string) {
		calls++
		return "", "invalid_credentials"
	}}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { g.handle(w, r, customAuthority, "Media") })
	for i := range 12 {
		req := httptest.NewRequest("POST", gateSignIn, strings.NewReader("account=alice&password=x"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Origin", "https://"+customAuthority)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if i >= 10 && rec.Code != http.StatusTooManyRequests {
			t.Fatal("attempt", i, rec.Code)
		}
	}
	if calls != 10 {
		t.Fatal("DSM asked", calls, "times")
	}
}

func TestCustomProxyStreamsAndCarriesWebSockets(t *testing.T) {
	release := make(chan struct{})
	up := websocket.Upgrader{}
	l := newCustomLab(t, true, false, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/events":
			w.Header().Set("Content-Type", "text/event-stream")
			io.WriteString(w, "data: first\n\n")
			w.(http.Flusher).Flush()
			<-release
			io.WriteString(w, "data: second\n\n")
		case "/ws":
			c, err := up.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer c.Close()
			kind, msg, err := c.ReadMessage()
			if err == nil {
				_ = c.WriteMessage(kind, append([]byte("echo "), msg...))
			}
		}
	})
	res := l.do("GET", "/events", nil, "")
	line, err := bufio.NewReader(res.Body).ReadString('\n')
	close(release)
	if err != nil || line != "data: first\n" {
		t.Fatal("stream was buffered", line, err)
	}
	d := websocket.Dialer{HandshakeTimeout: 3 * time.Second, NetDialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return ipc.DialUnix(ctx, l.socket) }}
	c, _, err := d.Dial("ws://connect/ws", http.Header{"Host": {customAuthority}})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err = c.WriteMessage(websocket.TextMessage, []byte("hi")); err != nil {
		t.Fatal(err)
	}
	_, msg, err := c.ReadMessage()
	if err != nil || string(msg) != "echo hi" {
		t.Fatal(string(msg), err)
	}
	// Stop ends live WebSockets too.
	l.proxy.Stop()
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err = c.ReadMessage(); err == nil {
		t.Fatal("WebSocket survived Stop")
	}
}

func TestProcNetListeners(t *testing.T) {
	raw := `  sl  local_address rem_address   st tx_queue rx_queue tr tm->when retrnsmt   uid  timeout inode
   0: 0100007F:7D70 00000000:0000 0A 00000000:00000000 00:00000000 00000000  1026        0 1 1
   1: 00000000:1F90 00000000:0000 0A 00000000:00000000 00:00000000 00000000     0        0 2 1
   2: 0100007F:8000 0100007F:1F90 01 00000000:00000000 00:00000000 00000000     0        0 3 1
`
	got := parseProcNet(raw)
	if len(got) != 2 || got[0].Port != 32112 || got[0].UID != 1026 || got[0].Addr != netip.MustParseAddr("127.0.0.1") || got[1].Port != 8080 || !got[1].Addr.IsUnspecified() {
		t.Fatalf("%+v", got)
	}
}

func TestCustomStartPathNavigationKeepsDSMAssetsAndAPIs(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, r.URL.RequestURI()) }))
	defer upstream.Close()
	app := CustomApp{Name: "Photos", URL: upstream.URL + "/photo/", AppSignIn: true}
	state := &customActive{}
	state.on.Store(true)
	binding := PinBinding{Package: "custom.photos", Scope: Public, Mode: ModeCustom, Summary: app.Digest(), Port: app.Port(), Authority: customAuthority}
	proxy, err := NewCustomProxy(CustomConfig{Binding: binding, App: app, State: state})
	if err != nil {
		t.Fatal(err)
	}
	defer proxy.tr.CloseIdleConnections()
	for _, target := range []string{"/", "/photo/", "/photo/assets/app.js", "/webapi/auth.cgi?api=test"} {
		r := httptest.NewRequest(http.MethodGet, "https://"+customAuthority+target, nil)
		w := httptest.NewRecorder()
		proxy.ServeHTTP(w, r)
		if target == "/" {
			if w.Code != http.StatusFound || w.Header().Get("Location") != "/photo/" {
				t.Fatal(w.Code, w.Header())
			}
			continue
		}
		if w.Code != 200 || w.Body.String() != target {
			t.Fatalf("%s: %d %s", target, w.Code, w.Body.String())
		}
	}
	// Approval changes when the start path changes, even on the same DSM port.
	other := app
	other.URL = upstream.URL + "/drive/"
	if app.Digest() == other.Digest() {
		t.Fatal("start path was not bound into approval")
	}
}
