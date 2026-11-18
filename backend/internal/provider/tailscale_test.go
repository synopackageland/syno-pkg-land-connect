package provider

import (
	"context"
	"dsmapp/internal/connector"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type tsDriver struct {
	session *tsSession
	starts  int
}

func (d *tsDriver) Open(context.Context) (TailscaleSession, error) { d.starts++; return d.session, nil }

type tsSession struct {
	snapshot     TailscaleSnapshot
	configured   bool
	served       []PortMapping
	stopped      bool
	stopErr      bool
	configureErr bool
	loginKey     string
	inspectErr   error
}

func (s *tsSession) Login(_ context.Context, key string) error { s.loginKey = key; return nil }
func (s *tsSession) Inspect(context.Context) (TailscaleSnapshot, error) {
	return s.snapshot, s.inspectErr
}
func (s *tsSession) Configure(_ context.Context, m []PortMapping) error {
	if s.configureErr {
		return errors.New("fixture")
	}
	s.configured = true
	s.served = append([]PortMapping(nil), m...)
	return nil
}
func (s *tsSession) Stop() error {
	if s.stopErr {
		return errors.New("fixture")
	}
	s.stopped = true
	return nil
}
func tsSettings() Settings { return Settings{} }

// With no private App, a signed-in NAS is connected at once: peers reach DSM
// at its own Tailscale address, which userspace tailscaled forwards to
// loopback, and Serve carries nothing.
func TestTailscaleConnectsWithoutServeAndShowsTheOwnAddress(t *testing.T) {
	session := &tsSession{snapshot: TailscaleSnapshot{BackendState: "Running", IPs: []string{"100.64.0.1", "fd7a:115c:a1e0::1"}, DNSName: "nas.fixture.ts.net.", Tailnet: "fixture"}}
	p := NewTailscale(t.TempDir(), &tsDriver{session: session})
	p.HTTPPort = func(context.Context) (int, error) { return 5000, nil }
	s, e := p.Start(context.Background(), Settings{})
	if e != nil || s.State != "connected" || len(session.served) != 0 {
		t.Fatal(s, e, session.served)
	}
	if len(s.Endpoints) != 2 || s.Endpoints[0].URL != "http://100.64.0.1:5000/" || s.Endpoints[1].URL != "http://nas.fixture.ts.net:5000/" || s.ShareURL != "" {
		t.Fatal(s.Endpoints)
	}
}

// Serve carries the approved private Apps only, and the connection waits for
// their routes to read back.
func TestTailscaleServesOnlyApprovedPrivateApps(t *testing.T) {
	session := &tsSession{snapshot: TailscaleSnapshot{BackendState: "Running", IPs: []string{"100.64.0.1"}, DNSName: "nas.fixture.ts.net", Tailnet: "fixture"}}
	p := NewTailscale(t.TempDir(), &tsDriver{session: session})
	app := PortMapping{LocalPort: 8443, Protocol: "unix", PublicPort: 8443, Socket: "/run/synopkgconnect/app.sock"}
	p.AppMappings = func(context.Context) ([]PortMapping, error) { return []PortMapping{app}, nil }
	s, e := p.Start(context.Background(), Settings{})
	if e != nil || s.State == "connected" || len(session.served) != 1 || session.served[0] != app {
		t.Fatal(s, e, session.served)
	}
	session.snapshot.Serving = true
	if s = p.Status(); s.State != "connected" {
		t.Fatal(s)
	}
}

// Ports, exposure and services are not Tailscale settings any more.
func TestTailscaleRefusesPortsAndExposure(t *testing.T) {
	for _, settings := range []Settings{{Mappings: []PortMapping{{LocalPort: 5000, Protocol: "http", PublicPort: 443}}}, {ConfirmExposure: true}, {ServiceIDs: []string{"dsm-http"}}} {
		d := &tsDriver{session: &tsSession{}}
		if _, e := NewTailscale(t.TempDir(), d).Start(context.Background(), settings); e == nil || d.starts != 0 {
			t.Fatal(settings, e)
		}
	}
}
func TestTailscaleLoginThenServeIsPrivateAndPersistsNoKey(t *testing.T) {
	dir := t.TempDir()
	session := &tsSession{snapshot: TailscaleSnapshot{BackendState: "NeedsLogin", AuthURL: "https://login.tailscale.com/a/fixture"}}
	d := &tsDriver{session: session}
	p := NewTailscale(dir, d)
	settings := tsSettings()
	settings.Token = "tskey-auth-SYNTHETIC"
	s, e := p.Start(context.Background(), settings)
	if e != nil || s.State != "needs-login" || s.LoginURL == "" || session.configured {
		t.Fatal(s, e)
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	if string(raw) == "" || containsSecret(raw, settings.Token) {
		t.Fatal("key persisted")
	}
	session.snapshot = TailscaleSnapshot{BackendState: "Running", IPs: []string{"100.64.0.1"}, DNSName: "nas.fixture.ts.net.", Tailnet: "fixture"}
	s = p.Status()
	if s.State != "connected" || !s.LoggedIn || s.Tailnet != "fixture" || s.MagicDNS != "nas.fixture.ts.net" || len(session.served) != 0 {
		t.Fatal(s)
	}
	if e = p.Disable(); e != nil || !session.stopped {
		t.Fatal(e)
	}
	d.starts = 0
	if e = NewTailscale(dir, d).Restore(context.Background()); e != nil || d.starts != 0 {
		t.Fatal("disabled revived", e)
	}
}
func containsSecret(b []byte, s string) bool {
	for i := 0; i+len(s) <= len(b); i++ {
		if string(b[i:i+len(s)]) == s {
			return true
		}
	}
	return false
}
func TestTailscaleDisablePersistsBeforeFailedStop(t *testing.T) {
	dir := t.TempDir()
	session := &tsSession{stopErr: true}
	d := &tsDriver{session: session}
	p := NewTailscale(dir, d)
	p.Start(context.Background(), tsSettings())
	if e := p.Disable(); e == nil {
		t.Fatal("stop failure swallowed")
	}
	if p.Status().State != "stopping" {
		t.Fatal(p.Status())
	}
	d.starts = 0
	if e := NewTailscale(dir, d).Restore(context.Background()); e != nil || d.starts != 0 {
		t.Fatal(e)
	}
}

// A process that starts with Tailscale disabled never restores it; disabling
// it there must keep the saved settings it has not loaded yet.
func TestTailscaleDisableKeepsSettingsSavedByAnEarlierProcess(t *testing.T) {
	dir := t.TempDir()
	d := &tsDriver{session: &tsSession{}}
	first := NewTailscale(dir, d)
	first.Start(context.Background(), Settings{LoginServer: "https://headscale.example.com"})
	if e := first.Disable(); e != nil {
		t.Fatal(e)
	}
	if e := NewTailscale(dir, d).Disable(); e != nil {
		t.Fatal(e)
	}
	restored := NewTailscale(dir, d)
	if e := restored.Restore(context.Background()); e != nil {
		t.Fatal(e)
	}
	if got := restored.Status().LoginServer; got != "https://headscale.example.com" {
		t.Fatal("disable lost the saved control server", got)
	}
}
func TestTailscaleRejectsUnsafeLoginURLAndMalformedDNS(t *testing.T) {
	for _, u := range []string{"javascript:alert(1)", "https://evil.invalid/a/key", "https://user@login.tailscale.com/a/key", "https://login.tailscale.com.evil.invalid/a/key"} {
		session := &tsSession{snapshot: TailscaleSnapshot{BackendState: "NeedsLogin", AuthURL: u}}
		p := NewTailscale(t.TempDir(), &tsDriver{session: session})
		s, e := p.Start(context.Background(), tsSettings())
		if e != nil || s.LoginURL != "" {
			t.Fatal(s, e)
		}
	}
}

func TestTailscaleRestoreRechecksConsentAndNeverUsesSavedKey(t *testing.T) {
	for _, raw := range []string{
		`{"enabled":true,"settings":{"mappings":[{"localPort":5000,"protocol":"http","publicPort":443}]}}`,
		`{"enabled":true,"settings":{"funnel":true}}`,
		`{"enabled":true,"settings":{"confirmExposure":true,"token":"SYNTHETIC","mappings":[{"localPort":5000,"protocol":"http","publicPort":443}]}}`,
		`{"enabled":true,"settings":{"confirmExposure":true,"mappings":[{"localPort":5000,"protocol":"http","publicPort":443}]},"extra":true}`,
	} {
		dir := t.TempDir()
		if e := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(raw), 0600); e != nil {
			t.Fatal(e)
		}
		d := &tsDriver{session: &tsSession{}}
		p := NewTailscale(dir, d)
		if e := p.Restore(context.Background()); e == nil || d.starts != 0 {
			t.Fatal("unsafe restore", e, d.starts)
		}
	}
}
func TestTailscaleRestoreSignsInWithoutAKey(t *testing.T) {
	dir := t.TempDir()
	first := &tsDriver{session: &tsSession{snapshot: TailscaleSnapshot{BackendState: "NeedsLogin"}}}
	p := NewTailscale(dir, first)
	if _, e := p.Start(context.Background(), tsSettings()); e != nil {
		t.Fatal(e)
	}
	if e := p.Stop(); e != nil {
		t.Fatal(e)
	}
	second := &tsDriver{session: &tsSession{snapshot: TailscaleSnapshot{BackendState: "Running", IPs: []string{"100.64.0.1"}, DNSName: "nas.fixture.ts.net"}}}
	restored := NewTailscale(dir, second)
	if e := restored.Restore(context.Background()); e != nil {
		t.Fatal(e)
	}
	restored.Status()
	if second.starts != 1 || len(second.session.served) != 0 || second.session.loginKey != "" {
		t.Fatal("wrong restore")
	}
}

func TestTailscaleMissingAuthorizationURLRemainsConnecting(t *testing.T) {
	session := &tsSession{snapshot: TailscaleSnapshot{BackendState: "NeedsLogin"}}
	p := NewTailscale(t.TempDir(), &tsDriver{session: session})
	s, e := p.Start(context.Background(), tsSettings())
	if e != nil || s.State != "connecting" || s.LoginURL != "" || s.LoggedIn || session.configured {
		t.Fatal("pending login claimed readiness", s, e)
	}
	session.snapshot.AuthURL = "https://login.tailscale.com/a/fixture"
	if s = p.Status(); s.State != "needs-login" || s.LoginURL == "" || session.configured {
		t.Fatal("authorization readiness not observed", s)
	}
}

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time          { return c.now }
func (c *fakeClock) Advance(d time.Duration) { c.now = c.now.Add(d) }

func TestTailscaleTransientInspectTimeoutReportsUnknownWithoutStopping(t *testing.T) {
	session := &tsSession{snapshot: TailscaleSnapshot{BackendState: "Starting"}}
	d := &tsDriver{session: session}
	p := NewTailscale(t.TempDir(), d)
	if _, e := p.Start(context.Background(), tsSettings()); e != nil {
		t.Fatal(e)
	}
	session.inspectErr = context.DeadlineExceeded
	if s := p.Status(); s.State != "unknown" || session.stopped {
		t.Fatal("a single read timeout stopped tailscaled", s.State, session.stopped)
	}
	session.inspectErr = nil
	if s := p.Status(); s.State != "connecting" || session.stopped || d.starts != 1 {
		t.Fatal("did not recover after a transient read failure", s.State, d.starts)
	}
}
func TestTailscaleExitedDaemonFailsClosedAtOnce(t *testing.T) {
	session := &tsSession{snapshot: TailscaleSnapshot{BackendState: "Starting"}}
	p := NewTailscale(t.TempDir(), &tsDriver{session: session})
	if _, e := p.Start(context.Background(), tsSettings()); e != nil {
		t.Fatal(e)
	}
	session.inspectErr = ErrDaemonExited
	if s := p.Status(); s.State != "failed" || !session.stopped {
		t.Fatal("an exited daemon is not a transient read failure", s.State)
	}
}

type failingLoginSession struct{ tsSession }

func (*failingLoginSession) Login(context.Context, string) error { return ErrDaemonExited }
func TestTailscaleStartFailureCarriesItsCodeThroughSelection(t *testing.T) {
	session := &failingLoginSession{}
	p := NewTailscale(t.TempDir(), &failingDriver{session})
	if _, e := p.Start(context.Background(), tsSettings()); !errors.Is(e, ErrTailscaleStart) {
		t.Fatal("start failure must carry tailscale_start_failed", e)
	}
	if s := p.Status(); s.State != "failed" || s.Code != "tailscale_start_failed" {
		t.Fatal("failed status must name the reason", s.State, s.Code)
	}
	m := NewSelection(t.TempDir(), p)
	if _, e := m.Connect(context.Background(), connector.Request{Provider: "tailscale"}); !errors.Is(e, ErrTailscaleStart) {
		t.Fatal("selection must keep the provider's code", e)
	}
}

type failingDriver struct{ session TailscaleSession }

func (d *failingDriver) Open(context.Context) (TailscaleSession, error) { return d.session, nil }

// flakyStopSession fails its first stops, as a loaded NAS reaps late.
type flakyStopSession struct {
	tsSession
	failures int
	calls    int
}

func (s *flakyStopSession) Stop() error {
	s.calls++
	if s.calls <= s.failures {
		return errors.New("fixture: child did not exit")
	}
	s.stopped = true
	return nil
}
func TestTailscaleStopRetriesATransientFailureWithinBound(t *testing.T) {
	session := &flakyStopSession{tsSession: tsSession{snapshot: TailscaleSnapshot{BackendState: "NeedsLogin"}}, failures: 1}
	p := NewTailscale(t.TempDir(), &failingDriver{session})
	if _, e := p.Start(context.Background(), tsSettings()); e != nil {
		t.Fatal(e)
	}
	if e := p.Disable(); e != nil || !session.stopped {
		t.Fatal("one late reap must not fail the disconnect", e)
	}
	session = &flakyStopSession{tsSession: tsSession{snapshot: TailscaleSnapshot{BackendState: "NeedsLogin"}}, failures: 100}
	p = NewTailscale(t.TempDir(), &failingDriver{session})
	p.Start(context.Background(), tsSettings())
	if e := p.Disable(); !errors.Is(e, ErrTailscaleStop) || session.calls > 3 {
		t.Fatal("a stop that keeps failing must stop retrying and name itself", e, session.calls)
	}
}
