package provider

import (
	"context"
	"dsmapp/internal/apps"
	"dsmapp/internal/connector"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeBinaries struct{ missing bool }

func (f fakeBinaries) Resolve(_ context.Context, name string) (Binary, error) {
	if f.missing {
		return Binary{}, ErrUnavailable
	}
	return Binary{Path: "/fixture/" + name, Version: "1.fixture"}, nil
}

type delayedLoginCommands struct {
	commandFixture
	reads      int
	neverReady bool
}

func (f *delayedLoginCommands) Run(ctx context.Context, binary string, args ...string) ([]byte, error) {
	if strings.Contains(strings.Join(args, " "), "status --json") {
		f.reads++
		if f.neverReady || f.reads < 3 {
			return []byte(`{"BackendState":"NeedsLogin","AuthURL":""}`), nil
		}
		return []byte(`{"BackendState":"NeedsLogin","AuthURL":"https://login.tailscale.com/a/fixture"}`), nil
	}
	return f.commandFixture.Run(ctx, binary, args...)
}
func (f fakeBinaries) Version(context.Context, string) (string, error) { return "1.fixture", nil }

type commandFixture struct {
	args    [][]string
	procs   []*execFixtureProcess
	status  []byte
	serve   []byte
	keyRead string
	fail    bool
}
type execFixtureProcess struct{ stopped bool }

func (p *execFixtureProcess) Stop() error { p.stopped = true; return nil }
func (p *execFixtureProcess) Alive() bool { return !p.stopped }
func (p *execFixtureProcess) Ready() bool { return p.Alive() }
func (f *commandFixture) Start(binary string, args ...string) (connector.Process, error) {
	f.args = append(f.args, append([]string{binary}, args...))
	for _, a := range args {
		if strings.HasPrefix(a, "--auth-key=file:") {
			raw, e := connector.ReadPrivate(strings.TrimPrefix(a, "--auth-key=file:"))
			if e != nil {
				return nil, e
			}
			f.keyRead = string(raw)
		}
	}
	if f.fail && strings.Contains(strings.Join(args, " "), " up ") {
		return nil, ErrUnavailable
	}
	p := &execFixtureProcess{}
	f.procs = append(f.procs, p)
	return p, nil
}
func (f *commandFixture) Run(_ context.Context, binary string, args ...string) ([]byte, error) {
	f.args = append(f.args, append([]string{binary}, args...))
	joined := strings.Join(args, " ")
	for _, a := range args {
		if strings.HasPrefix(a, "--auth-key=file:") {
			raw, e := connector.ReadPrivate(strings.TrimPrefix(a, "--auth-key=file:"))
			if e != nil {
				return nil, e
			}
			f.keyRead = string(raw)
		}
	}
	if strings.Contains(joined, "serve status") {
		return f.serve, nil
	}
	if strings.Contains(joined, "status --json") {
		return f.status, nil
	}
	if f.fail {
		return nil, errors.New("untrusted output")
	}
	return nil, nil
}

// appRoute is an approved private App's route: HTTPS on port to its proxy socket.
func appRoute(id string, port int) PortMapping {
	return PortMapping{LocalPort: port, PublicPort: port, Protocol: "unix", Socket: apps.PinSocket(id)}
}
func TestTailscaleExecOwnsUserspaceDaemonAndForegroundServe(t *testing.T) {
	dir := t.TempDir()
	f := &commandFixture{status: []byte(`{"BackendState":"Running","TailscaleIPs":["100.64.0.1"],"CertDomains":["nas.fixture.ts.net","fixture.ts.net"],"Self":{"DNSName":"nas.fixture.ts.net."},"CurrentTailnet":{"Name":"fixture"}}`), serve: []byte(`{}`)}
	d := TailscaleExec{Dir: dir, Binaries: unixBinaries{version: "1.104.1"}, Commands: f}
	s, e := d.Open(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Login(context.Background(), "tskey-auth-SYNTHETIC"); e != nil || f.keyRead != "tskey-auth-SYNTHETIC" {
		t.Fatal(e)
	}
	s.Inspect(context.Background())
	if _, e = os.Stat(filepath.Join(dir, "auth-key")); !os.IsNotExist(e) {
		t.Fatal("key retained")
	}
	if e = s.Configure(context.Background(), []PortMapping{appRoute("app", 443)}); e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(f.args)
	args := string(raw)
	for _, expected := range []string{"--tun=userspace-networking", "--socket=", "--state=", "--accept-dns=false", "--accept-routes=false", "--https=443", "unix:" + apps.PinSocket("app")} {
		if !strings.Contains(args, expected) {
			t.Fatal("missing", expected, args)
		}
	}
	if strings.Contains(args, "tskey-auth-SYNTHETIC") || strings.Contains(args, "--bg") || strings.Contains(args, "funnel") {
		t.Fatal("unsafe args", args)
	}
	f.serve = []byte(`{"TCP":{"443":{"HTTPS":true}},"Web":{"nas.fixture.ts.net:443":{"Handlers":{"/":{"Proxy":"unix:` + apps.PinSocket("app") + `"}}}}}`)
	state, e := s.Inspect(context.Background())
	if e != nil || !state.Serving {
		t.Fatal(state, e)
	}
	f.serve = []byte(`{"TCP":{"443":{"HTTPS":true}},"Web":{"nas.fixture.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:5001"}}}}}`)
	state, e = s.Inspect(context.Background())
	if e == nil || state.Serving {
		t.Fatal("mismatch must fail closed", state, e)
	}
	if e = s.Stop(); e != nil {
		t.Fatal(e)
	}
	for _, p := range f.procs {
		if !p.stopped {
			t.Fatal("owned child survived")
		}
	}
}
func TestTailscaleExecMissingBinaryDoesNotStart(t *testing.T) {
	f := &commandFixture{}
	d := TailscaleExec{Dir: t.TempDir(), Binaries: fakeBinaries{missing: true}, Commands: f}
	if _, e := d.Open(context.Background()); e == nil || len(f.args) != 0 {
		t.Fatal(e)
	}
}
func TestTailscaleExecErrorsNeverContainKeyOrRawOutput(t *testing.T) {
	f := &commandFixture{fail: true}
	d := TailscaleExec{Dir: t.TempDir(), Binaries: fakeBinaries{}, Commands: f}
	s, e := d.Open(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	e = s.Login(context.Background(), "SYNTHETIC")
	if e == nil || strings.Contains(e.Error(), "untrusted") {
		t.Fatal(e)
	}
}

func TestForegroundReadbackRejectsUnexpectedTargetsAndPublicExposure(t *testing.T) {
	routes := []PortMapping{appRoute("app", 443)}
	valid := `{"Foreground":{"session":{"TCP":{"443":{"HTTPS":true}},"Web":{"nas.fixture.ts.net:443":{"Handlers":{"/":{"Proxy":"unix:` + apps.PinSocket("app") + `"}}}}}}}`
	if ok, _ := checkServeMode([]byte(valid), "nas.fixture.ts.net", routes); !ok {
		t.Fatal("valid foreground refused")
	}
	cases := []string{
		strings.Replace(valid, `"Handlers":{"/":`, `"Handlers":{"/extra":{"Proxy":"http://127.0.0.1:22"},"/":`, 1),
		strings.Replace(valid, `"HTTPS":true`, `"HTTPS":true,"TCPForward":"127.0.0.1:22"`, 1),
		strings.Replace(valid, `"TCP":{"443":`, `"TCP":{"8443":{"HTTPS":true},"443":`, 1),
		strings.Replace(valid, `"TCP":`, `"AllowFunnel":{"nas.fixture.ts.net:443":true},"TCP":`, 1),
	}
	for _, raw := range cases {
		if ok, _ := checkServeMode([]byte(raw), "nas.fixture.ts.net", routes); ok {
			t.Fatal("unexpected exposure accepted", raw)
		}
	}
}

func TestTailscaleLoginRemovesStaleKeyAndDoesNotAcceptBadStatus(t *testing.T) {
	dir := t.TempDir()
	if e := connector.WritePrivate(filepath.Join(dir, "auth-key"), []byte("SYNTHETIC-STALE")); e != nil {
		t.Fatal(e)
	}
	f := &commandFixture{status: []byte(`{"BackendState":"NeedsLogin","AuthURL":"https://login.tailscale.com/a/fixture"}`)}
	s, e := (TailscaleExec{Dir: dir, Binaries: fakeBinaries{}, Commands: f}).Open(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(dir, "auth-key")); !os.IsNotExist(e) {
		t.Fatal("stale key survives open")
	}
	if e = s.Login(context.Background(), ""); e != nil {
		t.Fatal("valid pending login should not be failure", e)
	}
	f.status = []byte(`{"BackendState":"NeedsLogin","AuthURL":"https://evil.invalid/a/key"}`)
	if _, e = s.Inspect(context.Background()); e == nil {
		t.Fatal("unsafe url treated as pending login")
	}
}

func TestTailscaleExecRefusesConflictingReplacementAndWithdraws(t *testing.T) {
	f := &commandFixture{status: []byte(`{"BackendState":"Running","TailscaleIPs":["100.64.0.1"],"CertDomains":["nas.fixture.ts.net","fixture.ts.net"],"Self":{"DNSName":"nas.fixture.ts.net."}}`)}
	s, e := (TailscaleExec{Dir: t.TempDir(), Binaries: unixBinaries{version: "1.104.1"}, Commands: f}).Open(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Configure(context.Background(), []PortMapping{appRoute("app", 443)}); e != nil {
		t.Fatal(e)
	}
	old := f.procs[len(f.procs)-1]
	if e = s.Configure(context.Background(), []PortMapping{appRoute("other", 443)}); e == nil || old.stopped || f.procs[0].stopped {
		t.Fatal("conflicting replacement interrupted the old route", e)
	}
	if e = s.Configure(context.Background(), nil); e != nil || !old.stopped || f.procs[0].stopped {
		t.Fatal("withdraw", e)
	}
	s.Stop()
}

func TestTailscaleUnkeyedLoginDeadlineKeepsOwnedPendingDaemon(t *testing.T) {
	f := &delayedLoginCommands{commandFixture: commandFixture{fail: false}, neverReady: true}
	s, e := (TailscaleExec{Dir: t.TempDir(), Binaries: fakeBinaries{}, Commands: f, LoginWindow: 200 * time.Millisecond}).Open(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	start := time.Now()
	if e = s.Login(context.Background(), ""); e != nil {
		t.Fatal("live unkeyed pending authorization was killed", e)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("login window exceeded")
	}
}
func TestTailscaleExecInspectReportsExitedDaemon(t *testing.T) {
	f := &commandFixture{status: []byte(`{"BackendState":"Running","TailscaleIPs":["100.64.0.1"],"CertDomains":["nas.fixture.ts.net","fixture.ts.net"],"Self":{"DNSName":"nas.fixture.ts.net."}}`), serve: []byte(`{}`)}
	s, e := TailscaleExec{Dir: t.TempDir(), Binaries: fakeBinaries{}, Commands: f}.Open(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	f.procs[0].stopped = true
	if _, e = s.Inspect(context.Background()); !errors.Is(e, ErrDaemonExited) {
		t.Fatal("an exited daemon must be distinguishable from a slow read", e)
	}
}

// slowSocketCommands fails readiness probes, as a loaded NAS does while
// tailscaled is still creating its socket, then reports a running daemon.
type slowSocketCommands struct {
	commandFixture
	failures int
	probes   int
}

func (f *slowSocketCommands) Run(ctx context.Context, binary string, args ...string) ([]byte, error) {
	if strings.Contains(strings.Join(args, " "), "status --json") {
		f.probes++
		if f.probes <= f.failures {
			return nil, ErrUnavailable
		}
	}
	return f.commandFixture.Run(ctx, binary, args...)
}
func TestTailscaleLoginWaitsForSlowSocketWithinCallerDeadline(t *testing.T) {
	f := &slowSocketCommands{commandFixture: commandFixture{status: []byte(`{"BackendState":"Running","TailscaleIPs":["100.64.0.1"],"CertDomains":["nas.fixture.ts.net","fixture.ts.net"],"Self":{"DNSName":"nas.fixture.ts.net."}}`)}, failures: 5}
	s, e := (TailscaleExec{Dir: t.TempDir(), Binaries: fakeBinaries{}, Commands: f}).Open(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if e = s.Login(ctx, "tskey-auth-SYNTHETIC"); e != nil {
		t.Fatal("a socket that is slow to appear must be awaited until the caller's deadline", e, f.probes)
	}
}
func TestTailscaleUnkeyedLoginStaysPendingWhenReadsFailAfterUp(t *testing.T) {
	// The socket answered once; every later read fails, as on a loaded NAS.
	f := &readsFailAfterFirst{commandFixture: commandFixture{status: []byte(`{"BackendState":"NoState"}`)}}
	s, e := (TailscaleExec{Dir: t.TempDir(), Binaries: fakeBinaries{}, Commands: f, LoginWindow: 200 * time.Millisecond}).Open(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Login(context.Background(), ""); e != nil {
		t.Fatal("a live unkeyed daemon must stay pending; status polling reports the outcome", e)
	}
}

// readsFailAfterFirst answers the readiness probe, then fails every read.
type readsFailAfterFirst struct {
	commandFixture
	probes int
}

func (f *readsFailAfterFirst) Run(ctx context.Context, binary string, args ...string) ([]byte, error) {
	if strings.Contains(strings.Join(args, " "), "status --json") {
		f.probes++
		if f.probes > 1 {
			return nil, ErrUnavailable
		}
	}
	return f.commandFixture.Run(ctx, binary, args...)
}

type slowStopProcess struct{ stopped chan struct{} }

func (p *slowStopProcess) Stop() error {
	time.Sleep(300 * time.Millisecond)
	close(p.stopped)
	return nil
}
func (p *slowStopProcess) Alive() bool { return true }
func (p *slowStopProcess) Ready() bool { return true }

type slowStopCommands struct{ commandFixture }

func (f *slowStopCommands) Start(string, ...string) (connector.Process, error) {
	return &slowStopProcess{stopped: make(chan struct{})}, nil
}
func TestTailscaleExecStopsDaemonAndForwardsConcurrently(t *testing.T) {
	f := &slowStopCommands{commandFixture{status: []byte(`{"BackendState":"Running","TailscaleIPs":["100.64.0.1"],"CertDomains":["nas.fixture.ts.net","fixture.ts.net"],"Self":{"DNSName":"nas.fixture.ts.net."}}`)}}
	s, e := (TailscaleExec{Dir: t.TempDir(), Binaries: unixBinaries{version: "1.104.1"}, Commands: f}).Open(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	mappings := []PortMapping{appRoute("a", 443), appRoute("b", 8443), appRoute("c", 10000)}
	if e = s.Configure(context.Background(), mappings); e != nil {
		t.Fatal(e)
	}
	started := time.Now()
	if e = s.Stop(); e != nil {
		t.Fatal(e)
	}
	if time.Since(started) > 700*time.Millisecond {
		t.Fatal("each slow child must not add to the disconnect time", time.Since(started))
	}
}

func TestTailscaleRuntimeAndLogsAvoidPersistentStateDirectory(t *testing.T) {
	dir := t.TempDir()
	c := &commandFixture{}
	s, e := (TailscaleExec{Dir: dir, Binaries: fakeBinaries{}, Commands: c}).Open(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Stop()
	args := strings.Join(c.args[0], " ")
	if strings.Contains(args, "--statedir="+dir) || !strings.Contains(args, "--statedir=/tmp/SynoPkgConnect-") {
		t.Fatal(args)
	}
	if !strings.Contains(args, "--state="+filepath.Join(dir, "tailscaled.state")) {
		t.Fatal("identity must survive reboot", args)
	}
}
