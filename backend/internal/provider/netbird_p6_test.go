package provider

import (
	"bufio"
	"context"
	"dsmapp/internal/connector"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNetBirdP6ZeroFieldsAndStructuredStates(t *testing.T) {
	for _, row := range []struct{ raw, state, phase, code string }{
		{"NeedsLogin", "needs-login", "awaiting-login", ""}, {"Idle", "connecting", "connecting", ""},
		{"Connecting", "connecting", "connecting", ""}, {"LoginFailed", "failed", "failed", "login_denied"},
		{"SessionExpired", "needs-login", "obtaining-login-url", ""}, {"Mystery", "failed", "failed", "provider_status_invalid"},
		{"", "failed", "failed", "provider_status_invalid"},
	} {
		t.Run(row.raw, func(t *testing.T) {
			session := &nbSession{snapshot: NetBirdSnapshot{DaemonStatus: row.raw, LoginURL: "https://login.netbird.io/activate?user_code=ABCD-EFGH", UserCode: "ABCD-EFGH"}}
			p := NewNetBird(t.TempDir(), &nbDriver{session: session})
			s, e := p.Start(context.Background(), Settings{})
			if e != nil || s.State != row.state || s.Phase != row.phase || s.Code != row.code {
				t.Fatalf("%#v %v", s, e)
			}
			if row.raw == "SessionExpired" && s.LoginURL != "" {
				t.Fatal("expired challenge retained")
			}
		})
	}
}
func TestNetBirdP6ConnectedRequiresEverySignal(t *testing.T) {
	for _, row := range []struct {
		state, ip, management string
		ready                 bool
	}{
		{"Connected", "100.88.0.2/16", "", true}, {"Connected", "10.2.3.4/24", "https://management.example.test", true},
		{"Connected", "10.2.3.4/24", "", false}, {"Idle", "100.88.0.2/16", "", false},
		{"Connected", "127.0.0.1/8", "https://management.example.test", false}, {"Connected", "224.0.0.1/8", "", false},
	} {
		session := &nbSession{snapshot: NetBirdSnapshot{DaemonStatus: row.state, ManagementConnected: true, SignalConnected: true, IP: row.ip}}
		p := NewNetBird(t.TempDir(), &nbDriver{session: session})
		s, e := p.Start(context.Background(), Settings{ManagementURL: row.management})
		if e != nil || s.NetworkReady != row.ready {
			t.Fatalf("%#v %v", s, e)
		}
	}
}
func TestNetBirdP6LoginURLCorpusAndStreaming(t *testing.T) {
	good := "https://login.netbird.io/activate?user_code=ABCD-EFGH"
	for _, raw := range []string{good + "\n", good + "&next=x", strings.Replace(good, "login.", "api.", 1), strings.Replace(good, ".io/", ".io:443/", 1), strings.Replace(good, "ABCD-", "ABCD%2D", 1), good + "#x", strings.Replace(good, ".io/", ".io.evil.test/", 1)} {
		if _, _, e := netbirdLoginURL(raw, "", nil); connector.ErrorCode(e) != "login_url_rejected" {
			t.Fatalf("accepted %q: %v", raw, e)
		}
	}
	for _, out := range []string{"Use this URL to log in:\n\n" + good + " \n", "Use this URL to log in:\n\nhttps://login.netbird.io/activate and enter the code ABCD-EFGH to authenticate.\n"} {
		p := newNetbirdLoginParser(nil)
		for i := 0; i < len(out); i++ {
			if _, e := p.Write([]byte(out[i : i+1])); e != nil {
				t.Fatal(e)
			}
		}
		u, code, e := p.Challenge()
		if u == "" || code != "ABCD-EFGH" || e != nil {
			t.Fatal(u, code, e)
		}
	}
	p := newNetbirdLoginParser(nil)
	_, _ = p.Write([]byte("Use this URL to log in:\n" + good + "\nUse this URL to log in:\n" + good + "\n"))
	if _, _, e := p.Challenge(); connector.ErrorCode(e) != "login_output_invalid" {
		t.Fatal("duplicate accepted", e)
	}
	if _, _, e := netbirdLoginURL("https://idp.example.test/device", "", nil); connector.ErrorCode(e) != "login_origin_required" {
		t.Fatal(e)
	}
	if u, _, e := netbirdLoginURL("https://idp.example.test/device", "", []string{"https://idp.example.test"}); e != nil || u == "" {
		t.Fatal(e)
	}
	if _, _, e := netbirdLoginURL("https://management.example.test/device", "", []string{"https://idp.example.test"}); e == nil {
		t.Fatal("management trusted as IdP")
	}
}

// Streaming commands are controllable without an account or external network.
type nbStreaming struct {
	stopped chan struct{}
	output  string
	cliErr  error
	daemon  connector.Process
}

func (c *nbStreaming) Start(string, ...string) (connector.Process, error) { return c.daemon, nil }
func (c *nbStreaming) Run(context.Context, string, ...string) ([]byte, error) {
	return []byte(`{"daemonStatus":"NeedsLogin","management":{"connected":false},"signal":{"connected":false},"netbirdIp":""}`), nil
}
func (c *nbStreaming) Stream(ctx context.Context, _ string, out io.Writer, _ ...string) error {
	if c.output != "" {
		if _, e := out.Write([]byte(c.output)); e != nil {
			close(c.stopped)
			return e
		}
	}
	if c.cliErr != nil {
		close(c.stopped)
		return c.cliErr
	}
	<-ctx.Done()
	close(c.stopped)
	return ctx.Err()
}
func TestNetBirdP6StreamingLifetimeAndDeadlines(t *testing.T) {
	for _, row := range []struct {
		output, code string
		window       time.Duration
		cliErr       error
	}{
		{"Use this URL to log in:\n\nhttps://login.netbird.io/activate?user_code=ABCD-EFGH \n", "", time.Minute, nil},
		// NetBird 0.80.0 ends WaitSSOLogin on one failed token poll and names the IdP URL.
		{"Use this URL to log in:\n\nhttps://login.netbird.io/activate?user_code=ABCD-EFGH \n\nError: daemon up failed: sso login failed: waiting sso login failed with: rpc error: code = Unknown desc = parsing token response failed with error: failed to request access token with error: Post \"https://login.netbird.io/oauth/token\": read tcp 192.0.2.3:56592->198.51.100.10:443: read: connection reset by peer\n", "control_unreachable", time.Minute, nbError("login_denied")},
		{"Use this URL to log in:\n\nhttps://login.netbird.io/activate?user_code=ABCD-EFGH \n", "login_wait_timeout", 20 * time.Millisecond, nil},
		{"", "login_url_timeout", time.Minute, nil},
		{"Use this URL to log in:\nhttps://evil.example.test/device\n", "login_origin_required", time.Minute, nil},
	} {
		t.Run(row.code, func(t *testing.T) {
			c := &nbStreaming{stopped: make(chan struct{}), output: row.output, cliErr: row.cliErr, daemon: &nbSafeProcess{}}
			session, e := (NetBirdExec{Dir: t.TempDir(), Binaries: fakeBinaries{}, Commands: c, URLWindow: 20 * time.Millisecond, LoginWindow: row.window}).Open(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithCancel(context.Background())
			if e = session.Login(ctx, "https://api.netbird.io:443", ""); e != nil {
				t.Fatal(e)
			}
			cancel()
			var snapshot NetBirdSnapshot
			until := time.Now().Add(time.Second)
			for time.Now().Before(until) {
				snapshot, e = session.Inspect(context.Background())
				if e != nil {
					t.Fatal(e)
				}
				if row.code == "" && snapshot.LoginURL != "" || snapshot.Code == row.code && row.code != "" {
					break
				}
				time.Sleep(time.Millisecond)
			}
			if row.code == "" {
				if snapshot.UserCode != "ABCD-EFGH" {
					t.Fatal(snapshot)
				}
				select {
				case <-c.stopped:
					t.Fatal("caller cancellation killed WaitSSOLogin")
				default:
				}
			} else if snapshot.Code != row.code {
				t.Fatal(snapshot)
			}
			if e = session.Stop(); e != nil {
				t.Fatal(e)
			}
			select {
			case <-c.stopped:
			default:
				t.Fatal("CLI survived stop")
			}
		})
	}
}
func TestNetBirdP6RepeatedSSOJoinsAttempt(t *testing.T) {
	driver := &nbDriver{session: &nbSession{snapshot: NetBirdSnapshot{DaemonStatus: "NeedsLogin"}}}
	p := NewNetBird(t.TempDir(), driver)
	m := NewMultiSelection(t.TempDir(), p)
	first, e := m.Connect(context.Background(), connector.Request{Provider: "netbird"})
	if e != nil {
		t.Fatal(e)
	}
	second, e := m.Connect(context.Background(), connector.Request{Provider: "netbird"})
	if e != nil {
		t.Fatal(e)
	}
	if driver.opens != 1 || first.AttemptID != second.AttemptID {
		t.Fatal("second SSO process", driver.opens, first, second)
	}
}

type nbSafeProcess struct{ stopped atomic.Bool }

func (p *nbSafeProcess) Stop() error { p.stopped.Store(true); return nil }
func (p *nbSafeProcess) Alive() bool { return !p.stopped.Load() }
func (p *nbSafeProcess) Ready() bool { return p.Alive() }

func TestNetBirdP6SOCKSChecksDSMEvidenceAndRefusesRedirects(t *testing.T) {
	for _, row := range []struct {
		body   string
		status int
		ok     bool
	}{
		{`{"success":true,"data":{"SYNO.API.Auth":{"path":"entry.cgi","minVersion":1,"maxVersion":7}}}`, 200, true},
		{`{"success":true,"data":{"SYNO.API.Auth":{"path":"auth.cgi","minVersion":1,"maxVersion":7}}}`, 200, true},
		{`{"success":true,"data":{"SYNO.API.Auth":{"path":"evil.cgi","minVersion":1,"maxVersion":7}}}`, 200, false},
		{`<html>Hello</html>`, 200, false}, {``, 302, false}, {strings.Repeat("x", 65537), 200, false},
	} {
		listener, e := net.Listen("tcp", "127.0.0.1:0")
		if e != nil {
			t.Fatal(e)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			c, e := listener.Accept()
			if e != nil {
				return
			}
			defer c.Close()
			c.SetDeadline(time.Now().Add(time.Second))
			hello := make([]byte, 3)
			if _, e = io.ReadFull(c, hello); e != nil {
				return
			}
			c.Write([]byte{5, 0})
			header := make([]byte, 5)
			if _, e = io.ReadFull(c, header); e != nil {
				return
			}
			target := make([]byte, int(header[4])+2)
			if _, e = io.ReadFull(c, target); e != nil {
				return
			}
			c.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0})
			req, e := http.ReadRequest(bufio.NewReader(c))
			if e != nil || req.URL.Path != "/webapi/query.cgi" {
				return
			}
			fmt.Fprintf(c, "HTTP/1.1 %d Test\r\nContent-Length: %d\r\nLocation: http://evil.example.test/\r\nConnection: close\r\n\r\n%s", row.status, len(row.body), row.body)
		}()
		s := &netbirdExecSession{proxyPort: listener.Addr().(*net.TCPAddr).Port}
		e = s.CheckHTTP(context.Background(), "100.88.0.2", 5432)
		listener.Close()
		<-done
		if (e == nil) != row.ok {
			t.Fatal(row.status, e)
		}
	}
}
func TestNetBirdP6IPAndFQDNEndpointsUseDetectedPort(t *testing.T) {
	p := NewNetBird(t.TempDir(), &nbDriver{session: &nbSession{snapshot: NetBirdSnapshot{DaemonStatus: "Connected", ManagementConnected: true, SignalConnected: true, IP: "100.88.0.2/16", FQDN: "nas.example.test"}}})
	p.HTTPPort = func(context.Context) (int, error) { return 5432, nil }
	s, e := p.Start(context.Background(), Settings{})
	if e != nil || len(s.Endpoints) != 1 || s.Endpoints[0].URL != "http://100.88.0.2:5432/" {
		t.Fatal(s, e)
	}
	p.verifiedFQDN = "nas.example.test"
	p.verifiedIP = "100.88.0.2"
	p.verifiedPort = 5432
	if s = p.Status(); len(s.Endpoints) != 2 || s.Endpoints[1].URL != "http://nas.example.test:5432/" || s.ExternalVerified {
		t.Fatal(s)
	}
	p.HTTPPort = func(context.Context) (int, error) { return 5500, nil }
	if s = p.Status(); len(s.Endpoints) != 1 || s.Endpoints[0].LocalPort != 5500 {
		t.Fatal("stale service proof", s)
	}
}

func TestNetBirdP6ProductionStreamCancelsOwnedChild(t *testing.T) {
	c := &netbirdCommands{OSCommands: netbirdOSCommands(t.TempDir())}
	parser := newNetbirdLoginParser(nil)
	ready := make(chan struct{})
	parser.onChallenge = func(string, string) { close(ready) }
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- c.Stream(ctx, "/bin/sh", parser, "-c", "printf 'Use this URL to log in:\\nhttps://login.netbird.io/activate?user_code=ABCD-EFGH\\n'; exec /bin/sleep 30")
	}()
	select {
	case <-ready:
	case <-time.After(time.Second):
		t.Fatal("no streamed challenge")
	}
	c.mu.Lock()
	alive := c.login.Alive()
	c.mu.Unlock()
	if !alive {
		t.Fatal("CLI exited at challenge")
	}
	cancel()
	select {
	case e := <-done:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("child not joined")
	}
	if e := c.StopLogin(); e != nil {
		t.Fatal(e)
	}
	c.mu.Lock()
	alive = c.login.Alive()
	c.mu.Unlock()
	if alive {
		t.Fatal("owned CLI survived cancel")
	}
}

// A restored attempt that failed stays failed for status reads; signing in again
// opens a fresh attempt that returns to needs-login.
func TestNetBirdP6SignInAgainAfterRestoredFailure(t *testing.T) {
	dir := t.TempDir()
	if e := NewNetBird(dir, &nbDriver{session: &nbSession{snapshot: NetBirdSnapshot{DaemonStatus: "NeedsLogin"}}}).persist(netbirdSaved{Enabled: true}); e != nil {
		t.Fatal(e)
	}
	session := &nbSession{snapshot: NetBirdSnapshot{Code: "control_unreachable"}}
	driver := &nbDriver{session: session}
	p := NewNetBird(dir, driver)
	if e := p.Restore(context.Background()); e != nil {
		t.Fatal(e)
	}
	if s := p.Status(); s.State != "failed" || s.Code != "control_unreachable" {
		t.Fatal(s)
	}
	session.snapshot = NetBirdSnapshot{DaemonStatus: "NeedsLogin", LoginURL: "https://login.netbird.io/activate?user_code=ABCD-EFGH", UserCode: "ABCD-EFGH"}
	s, e := NewMultiSelection(t.TempDir(), p).Connect(context.Background(), connector.Request{Provider: "netbird"})
	if e != nil || driver.opens != 2 {
		t.Fatal(s, e, driver.opens)
	}
	if s = p.Status(); s.State != "needs-login" || s.UserCode != "ABCD-EFGH" || s.Code != "" {
		t.Fatal(s)
	}
}

func TestNetBirdManagementOriginImplicitTrustAndExternalPrompt(t *testing.T) {
	for _, tc := range []struct {
		management, login, want string
		external                []string
	}{
		{"https://mesh.example.test", "https://mesh.example.test/oauth/device", "", nil},
		{"https://mesh.example.test:443/", "https://mesh.example.test/oauth/device", "", nil},
		{"https://mesh.example.test", "https://idp.example.test/device", "login_origin_required", nil},
		{"https://mesh.example.test", "https://idp.example.test/device", "", []string{"https://idp.example.test"}},
		{"https://mesh.example.test", "https://login.netbird.io.evil.test/device", "login_url_rejected", []string{"https://login.netbird.io.evil.test"}},
		{"https://mesh.example.test", "https://mesh.example.test/device%2fnext", "login_url_rejected", nil},
		{"https://mesh.example.test", "https://idp.example.test/device%2fnext", "login_url_rejected", nil},
		{"https://mesh.example.test", "https://user@idp.example.test/device", "login_url_rejected", nil},
		{"https://mesh.example.test", "http://mesh.example.test/device", "login_url_rejected", nil},
	} {
		t.Run(tc.login, func(t *testing.T) {
			got, code, err := netbirdLoginURL(tc.login, "", netbirdLoginOrigins(tc.management, tc.external))
			if connector.ErrorCode(err) != tc.want {
				t.Fatal(got, code, err)
			}
			if tc.want == "login_origin_required" && (got != "https://idp.example.test" || code != "") {
				t.Fatal("pending challenge leaked URL/code", got, code)
			}
		})
	}
}

func TestNetBirdExecSelfHostedLoginAndOriginNeedsAction(t *testing.T) {
	for _, tc := range []struct {
		host, state, phase, code string
		origins                  []string
	}{
		{"mesh.example.test", "needs-login", "awaiting-login", "", nil},
		{"idp.example.test", "needs-login", "trust-login-origin", "login_origin_required", nil},
		{"idp.example.test", "needs-login", "awaiting-login", "", []string{"https://idp.example.test"}},
		{"login.netbird.io.evil.test", "failed", "failed", "login_url_rejected", []string{"https://login.netbird.io.evil.test"}},
	} {
		t.Run(tc.host+tc.phase, func(t *testing.T) {
			c := &nbStreaming{stopped: make(chan struct{}), output: "Use this URL to log in:\nhttps://" + tc.host + "/device\n", daemon: &nbSafeProcess{}}
			p := NewNetBird(t.TempDir(), NetBirdExec{Dir: t.TempDir(), Binaries: fakeBinaries{}, Commands: c, URLWindow: time.Second, LoginWindow: time.Minute})
			defer p.Stop()
			result, err := p.Start(context.Background(), Settings{ManagementURL: "https://mesh.example.test", LoginOrigins: tc.origins})
			if err != nil {
				t.Fatal(err)
			}
			until := time.Now().Add(time.Second)
			for time.Now().Before(until) && result.Phase != tc.phase {
				time.Sleep(time.Millisecond)
				result = p.Status()
			}
			if result.State != tc.state || result.Phase != tc.phase || result.Code != tc.code {
				t.Fatalf("%+v", result)
			}
			if tc.phase == "trust-login-origin" && (result.LoginURL != "" || result.LoginOrigin != "https://idp.example.test") {
				t.Fatal("unsafe prompt", result)
			}
			if len(p.saved.Settings.LoginOrigins) != len(tc.origins) {
				t.Fatal("implicit management trust polluted external settings")
			}
		})
	}
}
