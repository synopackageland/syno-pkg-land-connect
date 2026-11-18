package provider

import (
	"context"
	"dsmapp/internal/connector"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type nbDriver struct {
	session *nbSession
	opens   int
}

func (d *nbDriver) Open(context.Context) (NetBirdSession, error) { d.opens++; return d.session, nil }

type nbSession struct {
	snapshot          NetBirdSnapshot
	key, management   string
	stopped, failStop bool
	inspectErr        error
}

func (s *nbSession) Login(_ context.Context, management, key string) error {
	s.management = management
	s.key = key
	return nil
}
func (s *nbSession) Inspect(context.Context) (NetBirdSnapshot, error) {
	return s.snapshot, s.inspectErr
}
func (s *nbSession) Stop() error {
	if s.failStop {
		return errors.New("stop failed")
	}
	s.stopped = true
	return nil
}
func nbSettings() Settings {
	return Settings{ManagementURL: "https://api.netbird.io:443", Token: "SYNTHETIC-SETUP-KEY", ConfirmExposure: true, ConfirmManagementPolicy: true}
}

func TestNetBirdRequiresIndependentManagementConsentAndRejectsMisleadingLocalMappings(t *testing.T) {
	d := &nbDriver{session: &nbSession{}}
	p := NewNetBird(t.TempDir(), d)
	for _, mutate := range []func(*Settings){
		func(s *Settings) {
			s.ManagementURL = "https://management.example.test"
			s.ConfirmManagementPolicy = false
		},
		func(s *Settings) { s.Mappings = []PortMapping{{LocalPort: 5001, PublicPort: 5001, Protocol: "https"}} },
		func(s *Settings) { s.ManagementURL = "https://user:secret@example.com" }, func(s *Settings) { s.ManagementURL = "http://example.com" },
		func(s *Settings) { s.ManagementURL = "https://example.com/path?secret=x" },
	} {
		s := nbSettings()
		mutate(&s)
		if _, e := p.Start(context.Background(), s); e == nil {
			t.Fatal("unsafe settings accepted")
		}
	}
	if d.opens != 0 {
		t.Fatal("invalid request started client")
	}
}
func TestNetBirdPrivateLoginDurableDisableAndRestore(t *testing.T) {
	dir := t.TempDir()
	session := &nbSession{snapshot: NetBirdSnapshot{DaemonStatus: "Connected", ManagementConnected: true, SignalConnected: true, IP: "100.88.0.2/16"}}
	d := &nbDriver{session: session}
	p := NewNetBird(dir, d)
	status, e := p.Start(context.Background(), nbSettings())
	if e != nil || status.State != "connected" || status.NetBirdIP != "100.88.0.2" {
		t.Fatalf("status %#v error %v", status, e)
	}
	if status.KnownLimitation != "netbird_management_policy" {
		t.Fatal("missing trust boundary")
	}
	raw, e := os.ReadFile(filepath.Join(dir, "settings.json"))
	if e != nil || strings.Contains(string(raw), "SYNTHETIC") {
		t.Fatal("secret persisted")
	}
	encoded, _ := json.Marshal(status)
	if strings.Contains(string(encoded), "SYNTHETIC") {
		t.Fatal("secret in status")
	}
	if e = p.Stop(); e != nil {
		t.Fatal(e)
	}
	restored := NewNetBird(dir, d)
	if e = restored.Restore(context.Background()); e != nil || session.key != "" {
		t.Fatal("restart replayed setup key", e)
	}
	session.failStop = true
	if e = restored.Disable(); e == nil {
		t.Fatal("cleanup error swallowed")
	}
	before := d.opens
	next := NewNetBird(dir, d)
	if e = next.Restore(context.Background()); e != nil || d.opens != before {
		t.Fatal("disabled gate revived client")
	}
	session.failStop = false
	if e = restored.Disable(); e != nil {
		t.Fatal(e)
	}
}
func TestNetBirdSelectionCoexists(t *testing.T) {
	dir := t.TempDir()
	events := []string{}
	ts := &fixtureRuntime{id: "tailscale", events: &events}
	nb := NewNetBird(filepath.Join(dir, "netbird"), &nbDriver{session: &nbSession{snapshot: NetBirdSnapshot{DaemonStatus: "Connected", ManagementConnected: true, SignalConnected: true, IP: "100.88.0.2/16"}}})
	m := NewMultiSelection(dir, ts, nb)
	if _, e := m.Connect(context.Background(), connector.Request{Provider: "tailscale", ConfirmExposure: true}); e != nil {
		t.Fatal(e)
	}
	if _, e := m.Connect(context.Background(), connector.Request{Provider: "netbird", Token: "SYNTHETIC", ManagementURL: "https://api.netbird.io:443", ConfirmExposure: true, ConfirmManagementPolicy: true}); e != nil {
		t.Fatal(e)
	}
	if !ts.live || m.StatusFor("netbird").State != "connected" || len(m.Status().Connections) != 4 {
		t.Fatal("independent connections not ready")
	}
	if _, e := m.DisconnectProvider("netbird"); e != nil || !ts.live {
		t.Fatal("NetBird disable affected Tailscale", e)
	}
}

func TestNetBirdStatusDoesNotInventReadiness(t *testing.T) {
	session := &nbSession{snapshot: NetBirdSnapshot{DaemonStatus: "Connected", ManagementConnected: true, IP: "100.88.0.2/16"}}
	p := NewNetBird(t.TempDir(), &nbDriver{session: session})
	status, e := p.Start(context.Background(), nbSettings())
	if e != nil || status.State != "connecting" || status.NetBirdIP != "" {
		t.Fatalf("false ready: %#v %v", status, e)
	}
	session.snapshot.SignalConnected = true
	session.snapshot.IP = "not-an-ip"
	if p.Status().State == "connected" {
		t.Fatal("malformed IP ready")
	}
}

func TestNetBirdOfflineRevokeKeepsIdentityAndPreventsRestore(t *testing.T) {
	dir := t.TempDir()
	client := filepath.Join(dir, "netbird", "client")
	if e := os.MkdirAll(client, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(client, "default.json"), []byte("SYNTHETIC-IDENTITY"), 0600); e != nil {
		t.Fatal(e)
	}
	if e := RevokeOffline(dir); e != nil {
		t.Fatal(e)
	}
	// Uninstall keeping data keeps the peer for a reinstall; deleting data wipes it.
	if _, e := os.Stat(filepath.Join(client, "default.json")); e != nil {
		t.Fatal("peer identity lost", e)
	}
	d := &nbDriver{session: &nbSession{}}
	if e := NewNetBird(filepath.Join(dir, "netbird"), d).Restore(context.Background()); e != nil || d.opens != 0 {
		t.Fatal("offline revoke revived peer", e)
	}
}

type resumingSession struct {
	nbSession
	resumes bool
	logins  int
}

func (s *resumingSession) Login(ctx context.Context, management, key string) error {
	s.logins++
	return s.nbSession.Login(ctx, management, key)
}
func (s *resumingSession) Resumes(context.Context) bool { return s.resumes }

type resumingDriver struct{ session *resumingSession }

func (d resumingDriver) Open(context.Context) (NetBirdSession, error) { return d.session, nil }

func writeIdentity(t *testing.T, dir, content string) {
	t.Helper()
	client := filepath.Join(dir, "client")
	if e := os.MkdirAll(client, 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(filepath.Join(client, "default.json"), []byte(content), 0600); e != nil {
		t.Fatal(e)
	}
}
func readIdentity(t *testing.T, dir string) string {
	t.Helper()
	raw, e := os.ReadFile(filepath.Join(dir, "client", "default.json"))
	if e != nil {
		return ""
	}
	return string(raw)
}

func TestNetBirdOffKeepsIdentityAndOnResumesWithoutSignIn(t *testing.T) {
	dir := t.TempDir()
	session := &resumingSession{nbSession: nbSession{snapshot: NetBirdSnapshot{DaemonStatus: "Connected", ManagementConnected: true, SignalConnected: true, IP: "100.88.0.2/16"}}}
	p := NewNetBird(dir, resumingDriver{session})
	s := nbSettings()
	s.Token = ""
	if _, e := p.Start(context.Background(), s); e != nil || session.logins != 1 {
		t.Fatal("first start must sign in", e, session.logins)
	}
	writeIdentity(t, dir, "PEER-A")
	if e := p.Disable(); e != nil {
		t.Fatal(e)
	}
	if readIdentity(t, dir) != "PEER-A" {
		t.Fatal("off deleted the peer identity")
	}
	session.resumes = true
	if _, e := p.Start(context.Background(), s); e != nil || session.logins != 1 {
		t.Fatal("kept identity asked for a new sign-in", e, session.logins)
	}
	// An identity that does not come up signs in again, still with the same key.
	session.resumes = false
	_ = p.Disable()
	if _, e := p.Start(context.Background(), s); e != nil || session.logins != 2 || readIdentity(t, dir) != "PEER-A" {
		t.Fatal("expired identity not signed in", e, session.logins)
	}
	// A setup key always signs in.
	session.resumes = true
	_ = p.Disable()
	if _, e := p.Start(context.Background(), nbSettings()); e != nil || session.logins != 3 {
		t.Fatal("setup key skipped sign-in", e, session.logins)
	}
}

func TestNetBirdKeepsOneIdentityPerManagementServer(t *testing.T) {
	dir := t.TempDir()
	session := &resumingSession{nbSession: nbSession{snapshot: NetBirdSnapshot{DaemonStatus: "Connected", ManagementConnected: true, SignalConnected: true, IP: "100.88.0.2/16"}}}
	p := NewNetBird(dir, resumingDriver{session})
	cloud := nbSettings()
	if _, e := p.Start(context.Background(), cloud); e != nil {
		t.Fatal(e)
	}
	writeIdentity(t, dir, "CLOUD-PEER")
	own := nbSettings()
	own.ManagementURL = "https://nb.example.test"
	if _, e := p.Start(context.Background(), own); e != nil {
		t.Fatal(e)
	}
	if readIdentity(t, dir) != "" {
		t.Fatal("another server's identity used")
	}
	writeIdentity(t, dir, "OWN-PEER")
	if _, e := p.Start(context.Background(), cloud); e != nil || readIdentity(t, dir) != "CLOUD-PEER" {
		t.Fatal("cloud identity not restored", e)
	}
	if _, e := p.Start(context.Background(), own); e != nil || readIdentity(t, dir) != "OWN-PEER" {
		t.Fatal("own server identity not restored", e)
	}
	if e := p.Forget(); !errors.Is(e, ErrConnectionEnabled) {
		t.Fatal("forgot a live identity", e)
	}
	if e := p.Disable(); e != nil {
		t.Fatal(e)
	}
	if e := p.Forget(); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"client", "identities", "identity.json"} {
		if _, e := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(e) {
			t.Fatal("identity kept after forget:", name)
		}
	}
}

func TestNetBirdLegacyIdentityBelongsToSavedServer(t *testing.T) {
	dir := t.TempDir()
	raw, _ := json.Marshal(netbirdSaved{Settings: Settings{ManagementURL: "https://api.netbird.io:443"}})
	if e := os.WriteFile(filepath.Join(dir, "settings.json"), raw, 0600); e != nil {
		t.Fatal(e)
	}
	writeIdentity(t, dir, "LEGACY-PEER")
	session := &resumingSession{resumes: true}
	p := NewNetBird(dir, resumingDriver{session})
	s := nbSettings()
	s.Token = ""
	if _, e := p.Start(context.Background(), s); e != nil || session.logins != 0 || readIdentity(t, dir) != "LEGACY-PEER" {
		t.Fatal("legacy identity not resumed", e, session.logins)
	}
}

func TestNetBirdBinaryActivationAndRollbackDoNotRequirePortSelection(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "activate", true: "rollback"}[fail], func(t *testing.T) {
			events := []string{}
			version := "old"
			p := &updateRuntime{fixtureRuntime: fixtureRuntime{id: "netbird", events: &events}, version: &version, failNew: fail}
			m := NewSelection(t.TempDir(), p)
			if _, e := m.Connect(context.Background(), connector.Request{Provider: "netbird", ConfirmExposure: true, ConfirmManagementPolicy: true}); e != nil {
				t.Fatal(e)
			}
			m.SetPortSource(failingNetBirdPorts{})
			e := m.UpdateBinaries(context.Background(), "netbird", changeVersion(&events, &version))
			if (e != nil) != fail || !p.live || !m.saved.Enabled || m.failed {
				t.Fatal(e, events, version)
			}
			want := "new"
			if fail {
				want = "old"
			}
			if version != want {
				t.Fatal(version)
			}
		})
	}
}

type failingNetBirdPorts struct{}

func (failingNetBirdPorts) ResolvePorts(context.Context) ([]ResolvedPort, error) {
	return nil, errors.New("NetBird must not resolve Connect service selections")
}

type failingNetBirdLogin struct{ nbSession }

func (*failingNetBirdLogin) Login(context.Context, string, string) error { return ErrDaemonExited }

type flakyNetBirdStop struct {
	nbSession
	failures, calls int
}

func (s *flakyNetBirdStop) Stop() error {
	s.calls++
	if s.calls <= s.failures {
		return errors.New("fixture: child did not exit")
	}
	return nil
}

type anyNetBirdDriver struct{ session NetBirdSession }

func (d anyNetBirdDriver) Open(context.Context) (NetBirdSession, error) { return d.session, nil }
func TestNetBirdStartAndStopFailuresNameThemselves(t *testing.T) {
	p := NewNetBird(t.TempDir(), anyNetBirdDriver{&failingNetBirdLogin{}})
	if _, e := p.Start(context.Background(), nbSettings()); !errors.Is(e, ErrNetBirdStart) {
		t.Fatal("start failure must carry netbird_start_failed", e)
	}
	if s := p.Status(); s.State != "failed" || s.Code != "netbird_start_failed" {
		t.Fatal(s.State, s.Code)
	}
	flaky := &flakyNetBirdStop{nbSession: nbSession{snapshot: NetBirdSnapshot{DaemonStatus: "Idle"}}, failures: 1}
	p = NewNetBird(t.TempDir(), anyNetBirdDriver{flaky})
	if _, e := p.Start(context.Background(), nbSettings()); e != nil {
		t.Fatal(e)
	}
	if e := p.Disable(); e != nil {
		t.Fatal("one late reap must not fail the disconnect", e)
	}
	flaky = &flakyNetBirdStop{nbSession: nbSession{snapshot: NetBirdSnapshot{DaemonStatus: "Idle"}}, failures: 100}
	p = NewNetBird(t.TempDir(), anyNetBirdDriver{flaky})
	p.Start(context.Background(), nbSettings())
	if e := p.Disable(); !errors.Is(e, ErrNetBirdStop) || flaky.calls > 3 {
		t.Fatal("a stop that keeps failing must stop retrying and name itself", e, flaky.calls)
	}
}
