package provider

import (
	"context"
	"dsmapp/internal/apps"
	"dsmapp/internal/connector"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestP5LoginURLExactShape(t *testing.T) {
	for _, raw := range []string{"https://login.tailscale.com/a/token?x=1", "https://login.tailscale.com/a/token#x", "https://login.tailscale.com/a/token/extra", "https://login.tailscale.com/a/%74oken", "https://login.tailscale.com:443/a/token", "https://login.tailscale.com/a/", "https://login.tailscale.com/a/token\n"} {
		if safeLoginURL(raw) != "" {
			t.Errorf("accepted %q", raw)
		}
	}
	if safeLoginURL("https://login.tailscale.com/a/Abc123") == "" {
		t.Fatal("official URL refused")
	}
}
func TestP5PrivateStartNeedsNoPublicConsent(t *testing.T) {
	p := NewTailscale(t.TempDir(), &tsDriver{session: &tsSession{snapshot: TailscaleSnapshot{BackendState: "NeedsLogin", AuthURL: "https://login.tailscale.com/a/token"}}})
	s := tsSettings()
	s.ConfirmExposure = false
	if _, e := p.Start(context.Background(), s); e != nil {
		t.Fatal(e)
	}
	s.ConfirmExposure = true
	if e := p.Validate(s); e == nil {
		t.Fatal("a private connection took public consent")
	}
}
func TestP5MachineApprovalAndNoURLAreDistinct(t *testing.T) {
	for _, tc := range []struct{ backend, state, code string }{{"NeedsMachineAuth", "needs-machine-auth", "machine_approval_required"}, {"NeedsLogin", "connecting", "control_unreachable"}, {"SomethingNew", "failed", "provider_status_invalid"}} {
		session := &tsSession{snapshot: TailscaleSnapshot{BackendState: tc.backend}}
		p := NewTailscale(t.TempDir(), &tsDriver{session: session})
		s, e := p.Start(context.Background(), tsSettings())
		if e != nil || s.State != tc.state || s.Code != tc.code || s.LoginURL != "" {
			t.Errorf("%s: %+v %v", tc.backend, s, e)
		}
	}
}
func TestP5LoginCLIStaysAliveUntilCompletion(t *testing.T) {
	f := &commandFixture{status: []byte(`{"BackendState":"NeedsLogin","AuthURL":"https://login.tailscale.com/a/token"}`)}
	s, e := (TailscaleExec{Dir: t.TempDir(), Binaries: fakeBinaries{}, Commands: f}).Open(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Stop()
	if e = s.Login(context.Background(), ""); e != nil {
		t.Fatal(e)
	}
	up := 0
	for _, args := range f.args {
		joined := strings.Join(args, " ")
		if strings.Contains(joined, " up ") {
			up++
			if strings.Contains(joined, "--timeout=1s") {
				t.Fatal("CLI abandons human login after one second")
			}
		}
	}
	if up != 1 || len(f.procs) < 2 || !f.procs[1].Alive() {
		t.Fatal("owned login child did not survive", up, len(f.procs))
	}
	if _, e = s.Inspect(context.Background()); e != nil {
		t.Fatal(e)
	}
	if !f.procs[1].Alive() {
		t.Fatal("obtaining URL stopped child")
	}
	f.status = []byte(`{"BackendState":"Running","TailscaleIPs":["100.64.0.1"],"CertDomains":["nas.fixture.ts.net","fixture.ts.net"]}`)
	s.Inspect(context.Background())
	if f.procs[1].Alive() {
		t.Fatal("login child not reaped after completion")
	}
}
func TestP5FailureNeverExposesUntrustedChallenge(t *testing.T) {
	session := &tsSession{snapshot: TailscaleSnapshot{BackendState: "NeedsLogin", AuthURL: "https://login.tailscale.com/a/token?secret=fixture"}}
	p := NewTailscale(t.TempDir(), &tsDriver{session: session})
	s, _ := p.Start(context.Background(), tsSettings())
	if s.LoginURL != "" || s.Code != "login_url_rejected" || s.State != "failed" {
		t.Fatal(s)
	}
}

func TestP5HeadscaleOriginIsExplicitAndNotLearned(t *testing.T) {
	server := "https://headscale.example.com:8443"
	for _, raw := range []string{"https://headscale.example.com:8443/register/node", "https://id.example.com/device?code=abc"} {
		origin := ""
		if strings.Contains(raw, "id.example") {
			origin = "https://id.example.com"
		}
		if trustedTailscaleURL(raw, server, origin) != raw {
			t.Fatal("explicit origin refused", raw)
		}
	}
	for _, raw := range []string{"https://headscale.example.com/register/node", "https://headscale.example.com:8443.evil/register/node", "https://headscale.example.com:8443/register/%2fadmin", "https://id.example.com/device?code=abc", "https://headscale.example.com:8443/register/../admin"} {
		if trustedTailscaleURL(raw, server, "") != "" {
			t.Fatal("untrusted URL", raw)
		}
	}
}
func TestP5HumanDeadlineStartsWithChallengeAndStopsChildren(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	f := &commandFixture{status: []byte(`{"BackendState":"NeedsLogin","AuthURL":""}`)}
	dir := t.TempDir()
	session, e := (TailscaleExec{Dir: dir, Binaries: fakeBinaries{}, Commands: f, Now: func() time.Time { return clock }}).Open(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if e = session.Login(context.Background(), "SYNTHETIC"); e != nil {
		t.Fatal(e)
	}
	clock = clock.Add(59 * time.Second)
	f.status = []byte(`{"BackendState":"NeedsLogin","AuthURL":"https://login.tailscale.com/a/token"}`)
	state, e := session.Inspect(context.Background())
	if e != nil || state.LoginDeadline != clock.Add(10*time.Minute).UTC().Format(time.RFC3339) {
		t.Fatal(state, e)
	}
	clock = clock.Add(10 * time.Minute)
	_, e = session.Inspect(context.Background())
	if connector.ErrorCode(e) != "login_wait_timeout" {
		t.Fatal(e)
	}
	for _, p := range f.procs {
		if p.Alive() {
			t.Fatal("child survived timeout")
		}
	}
	if _, e = os.Stat(filepath.Join(dir, "auth-key")); !os.IsNotExist(e) {
		t.Fatal("key survives timeout")
	}
}
func TestP5NoURLDeadlineIsNetworkFailureAndCancelReaps(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	f := &commandFixture{status: []byte(`{"BackendState":"NeedsLogin"}`)}
	session, e := (TailscaleExec{Dir: t.TempDir(), Binaries: fakeBinaries{}, Commands: f, Now: func() time.Time { return clock }}).Open(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	session.Login(context.Background(), "")
	clock = clock.Add(time.Minute)
	if _, e = session.Inspect(context.Background()); connector.ErrorCode(e) != "control_unreachable" {
		t.Fatal(e)
	}
	for _, p := range f.procs {
		if p.Alive() {
			t.Fatal("child survived acquisition timeout")
		}
	}
}

// A private App needs HTTPS certificates; without them its route is refused,
// never served over HTTP.
func TestP5MissingHTTPSRefusesInsteadOfFallingBack(t *testing.T) {
	f := &commandFixture{status: []byte(`{"BackendState":"Running","TailscaleIPs":["100.64.0.1","fd7a:115c:a1e0::1"],"Self":{"DNSName":"nas.fixture.ts.net."}}`), serve: []byte(`{"TCP":{"443":{"HTTP":true}},"Web":{"nas.fixture.ts.net:443":{"Handlers":{"/":{"Proxy":"unix:` + apps.PinSocket("app") + `"}}}}}`)}
	dir := t.TempDir()
	p := NewTailscale(dir, TailscaleExec{Dir: dir, Binaries: unixBinaries{version: "1.104.1"}, Commands: f})
	p.AppMappings = func(context.Context) ([]PortMapping, error) { return []PortMapping{appRoute("app", 443)}, nil }
	defer p.Stop()
	p.Start(context.Background(), tsSettings())
	state := p.Status()
	if state.State == "connected" || state.Code != "serve_prerequisite_missing" {
		t.Fatal(state)
	}
	if ok, _ := checkServeMode(f.serve, "nas.fixture.ts.net", []PortMapping{appRoute("app", 443)}); ok {
		t.Fatal("HTTP readback passed as HTTPS")
	}
}
func TestP5RunningRequiresValidSelfIP(t *testing.T) {
	for _, ip := range []string{"", "0.0.0.0", "127.0.0.1", "::", "ff00::1", "bogus"} {
		session := &tsSession{snapshot: TailscaleSnapshot{BackendState: "Running", DNSName: "nas.fixture.ts.net", IPs: []string{ip}, Serving: true}}
		p := NewTailscale(t.TempDir(), &tsDriver{session: session})
		state, _ := p.Start(context.Background(), tsSettings())
		if state.State == "connected" || session.configured {
			t.Fatal("invalid self identity passed", ip)
		}
	}
}
func TestP5RepeatedStartJoinsPendingAttempt(t *testing.T) {
	driver := &tsDriver{session: &tsSession{snapshot: TailscaleSnapshot{BackendState: "NeedsLogin", AuthURL: "https://login.tailscale.com/a/token"}}}
	p := NewTailscale(t.TempDir(), driver)
	m := NewSelection(t.TempDir(), p)
	defer m.Stop()
	request := connector.Request{Provider: "tailscale", Mappings: tsSettings().Mappings}
	first, e := m.Connect(context.Background(), request)
	if e != nil {
		t.Fatal(e)
	}
	second, e := m.Connect(context.Background(), request)
	if e != nil || driver.starts != 1 || second.LoginURL != first.LoginURL {
		t.Fatal("duplicate replaced pending login", driver.starts, e)
	}
}
func TestP5FailedAttemptDoesNotReviveOnRestore(t *testing.T) {
	dir := t.TempDir()
	session := &tsSession{snapshot: TailscaleSnapshot{BackendState: "NeedsLogin", AuthURL: "https://evil.invalid/login"}}
	driver := &tsDriver{session: session}
	p := NewTailscale(dir, driver)
	state, _ := p.Start(context.Background(), tsSettings())
	if state.State != "failed" {
		t.Fatal(state)
	}
	driver.starts = 0
	restored := NewTailscale(dir, driver)
	if e := restored.Restore(context.Background()); e != nil || driver.starts != 0 {
		t.Fatal("failed login revived", e, driver.starts)
	}
}
func TestP5SchemaDoesNotRequirePrivateExposureConsent(t *testing.T) {
	for _, field := range NewTailscale("", nil).Schema() {
		if field.Name == "confirmExposure" && field.Required {
			t.Fatal("private login schema requires public consent")
		}
	}
}

func TestP5NewAuthKeyReplacesPendingAttempt(t *testing.T) {
	driver := &tsDriver{session: &tsSession{snapshot: TailscaleSnapshot{BackendState: "NeedsLogin", AuthURL: "https://login.tailscale.com/a/token"}}}
	p := NewTailscale(t.TempDir(), driver)
	m := NewSelection(t.TempDir(), p)
	defer m.Stop()
	request := connector.Request{Provider: "tailscale", Mappings: tsSettings().Mappings}
	if _, e := m.Connect(context.Background(), request); e != nil {
		t.Fatal(e)
	}
	request.Token = "synthetic-replacement-key"
	if _, e := m.Connect(context.Background(), request); e != nil || driver.starts != 2 || driver.session.loginKey != request.Token {
		t.Fatal("new key ignored", e, driver.starts)
	}
}

type rejectedTailscaleDriver struct{ starts int }

func (d *rejectedTailscaleDriver) Open(context.Context) (TailscaleSession, error) {
	d.starts++
	return nil, ErrUnavailable
}
func TestP5OpenFailureDoesNotReviveOnRestore(t *testing.T) {
	dir := t.TempDir()
	driver := &rejectedTailscaleDriver{}
	p := NewTailscale(dir, driver)
	if state, e := p.Start(context.Background(), tsSettings()); e == nil || state.State != "failed" {
		t.Fatal(state, e)
	}
	if e := NewTailscale(dir, driver).Restore(context.Background()); e != nil || driver.starts != 1 {
		t.Fatal("failed open revived", e, driver.starts)
	}
}

func TestP5ApprovalTimeoutRetainsApprovalReason(t *testing.T) {
	clock := time.Unix(1700000000, 0)
	f := &commandFixture{status: []byte(`{"BackendState":"NeedsMachineAuth"}`)}
	session, e := (TailscaleExec{Dir: t.TempDir(), Binaries: fakeBinaries{}, Commands: f, Now: func() time.Time { return clock }}).Open(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer session.Stop()
	if e = session.Login(context.Background(), ""); e != nil {
		t.Fatal(e)
	}
	if _, e = session.Inspect(context.Background()); e != nil {
		t.Fatal(e)
	}
	clock = clock.Add(10 * time.Minute)
	if _, e = session.Inspect(context.Background()); connector.ErrorCode(e) != "machine_approval_wait_timeout" {
		t.Fatal("approval fact lost", e)
	}
}
