package provider

import (
	"context"
	"dsmapp/internal/connector"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type nbCommands struct {
	commandFixture
	key string
}

func (c *nbCommands) Run(ctx context.Context, binary string, args ...string) ([]byte, error) {
	for i, a := range args {
		if a == "--setup-key-file" {
			b, e := connector.ReadPrivate(args[i+1])
			if e != nil {
				return nil, e
			}
			c.key = string(b)
		}
	}
	return c.commandFixture.Run(ctx, binary, args...)
}
func TestNetBirdExecKeepsSetupKeyOutOfArgvAndOwnsDaemon(t *testing.T) {
	dir := t.TempDir()
	c := &nbCommands{commandFixture: commandFixture{status: []byte(`{"daemonStatus":"Connected","management":{"connected":true},"signal":{"connected":true},"netbirdIp":"100.88.0.2/16"}`)}}
	s, e := (NetBirdExec{Dir: dir, Binaries: fakeBinaries{}, Commands: c}).Open(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Login(context.Background(), "https://api.netbird.io:443", "SYNTHETIC-NB-KEY"); e != nil || c.key != "SYNTHETIC-NB-KEY" {
		t.Fatal("key file login failed", e)
	}
	for _, a := range c.args {
		if strings.Contains(strings.Join(a, " "), "SYNTHETIC-NB-KEY") {
			t.Fatal("setup key in argv")
		}
	}
	if _, e = os.Lstat(filepath.Join(dir, "client", "setup-key")); !os.IsNotExist(e) {
		t.Fatal("transient setup key retained")
	}
	joined := ""
	for _, a := range c.args {
		joined += strings.Join(a, " ") + "\n"
	}
	for _, flag := range []string{"--disable-client-routes", "--disable-server-routes", "--disable-dns", "--disable-ipv6", "--allow-server-ssh=false", "--allow-remote-jobs=false"} {
		if !strings.Contains(joined, flag) {
			t.Fatal("missing scope flag", flag)
		}
	}
	o, e := s.Inspect(context.Background())
	if e != nil || !o.ManagementConnected || !o.SignalConnected || o.IP != "100.88.0.2/16" {
		t.Fatal("status decode failed", e)
	}
	if e = s.Stop(); e != nil || !c.procs[0].stopped {
		t.Fatal("daemon not stopped", e)
	}
}
func TestNetBirdProcessEnvironmentDoesNotInheritCredentials(t *testing.T) {
	t.Setenv("NB_SETUP_KEY", "SYNTHETIC-UNTRUSTED")
	c := netbirdOSCommands(t.TempDir()).command("/fixture/netbird")
	env := strings.Join(c.Env, "\n")
	if strings.Contains(env, "SYNTHETIC-UNTRUSTED") || !strings.Contains(env, "NB_USE_NETSTACK_MODE=true") || !strings.Contains(env, "NB_NETSTACK_SKIP_PROXY=false") || !strings.Contains(env, "NB_ENABLE_NETSTACK_LOCAL_FORWARDING=true") {
		t.Fatal("unscoped NetBird environment")
	}
}
func TestNetBirdExecInspectReportsExitedDaemon(t *testing.T) {
	c := &nbCommands{commandFixture: commandFixture{status: []byte(`{"daemonStatus":"Connected","management":{"connected":true},"signal":{"connected":true},"netbirdIp":"100.88.0.2/16"}`)}}
	s, e := (NetBirdExec{Dir: t.TempDir(), Binaries: fakeBinaries{}, Commands: c}).Open(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	c.procs[0].stopped = true
	if _, e = s.Inspect(context.Background()); !errors.Is(e, ErrDaemonExited) {
		t.Fatal("an exited daemon must be distinguishable from a slow read", e)
	}
}

// slowNetBirdSocket fails readiness probes beyond the former 12 s budget's
// probe count, then answers, as a loaded NAS does.
type slowNetBirdSocket struct {
	nbCommands
	probes int
}

func (c *slowNetBirdSocket) Run(ctx context.Context, binary string, args ...string) ([]byte, error) {
	if strings.Contains(strings.Join(args, " "), "status --check live") {
		c.probes++
		if c.probes <= 3 {
			<-ctx.Done()
			return nil, ErrUnavailable
		}
	}
	return c.nbCommands.Run(ctx, binary, args...)
}
func TestNetBirdLoginWaitsForSlowSocketWithinCallerDeadline(t *testing.T) {
	c := &slowNetBirdSocket{nbCommands: nbCommands{commandFixture: commandFixture{status: []byte(`{}`)}}}
	s, e := (NetBirdExec{Dir: t.TempDir(), Binaries: fakeBinaries{}, Commands: c, ProbeTimeout: 100 * time.Millisecond}).Open(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Login(context.Background(), "https://api.netbird.io:443", ""); e != nil {
		t.Fatal("hung readiness probes must be retried, not wait out one shared budget", e)
	}
}

func TestNetBirdMutableRuntimeIsSeparateFromPersistentIdentity(t *testing.T) {
	dir := t.TempDir()
	commands, err := netbirdRuntimeCommands(dir)
	if err != nil {
		t.Fatal(err)
	}
	env := strings.Join(commands.Env, "\n")
	if commands.Dir != dir || strings.Contains(env, "NB_STATE_DIR="+dir) || !strings.Contains(env, "NB_STATE_DIR=/tmp/SynoPkgConnect-") {
		t.Fatal(commands.Dir, env)
	}
}

// busyEngineNetBird answers like NetBird 0.82 while its connection engine is
// stuck connecting: the full status (status --json) waits for the engine,
// while the daemon's own state answers at once.
type busyEngineNetBird struct {
	nbCommands
	fullReads, plainReads int
}

func (c *busyEngineNetBird) Run(ctx context.Context, binary string, args ...string) ([]byte, error) {
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, "status --json"):
		c.fullReads++
		<-ctx.Done()
		return nil, ErrUnavailable
	case strings.Contains(joined, "status --check"):
		c.plainReads++
	}
	return c.nbCommands.Run(ctx, binary, args...)
}

// Regression (a NAS after the 0096 to 0097 upgrade): readiness waited on
// status --json, which never answers while NetBird is connecting, so a
// running daemon read as daemon_start_timeout forever and was never signed in.
func TestNetBirdReadinessDoesNotWaitForTheConnectionEngine(t *testing.T) {
	c := &busyEngineNetBird{}
	s, e := (NetBirdExec{Dir: t.TempDir(), Binaries: fakeBinaries{}, Commands: c, ProbeTimeout: 50 * time.Millisecond, ReadyWindow: 300 * time.Millisecond}).Open(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	if e = s.(readyWaiter).WaitReady(context.Background()); e != nil || c.plainReads == 0 {
		t.Fatal("a daemon that answers its own status is ready", e, c.plainReads)
	}
}

// A daemon whose engine never answers, but which itself answers, is NetBird
// failing to reach its control server: a real failure, not an endless wait.
func TestNetBirdHungEngineIsAControlFailureNotAnIncompleteCheck(t *testing.T) {
	c := &busyEngineNetBird{}
	s, e := (NetBirdExec{Dir: t.TempDir(), Binaries: fakeBinaries{}, Commands: c}).Open(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	p := NewNetBird(t.TempDir(), nil)
	p.session, p.verifiedPort = s, 5000
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	e = p.observeHealth(ctx)
	if errors.Is(e, errHealthWaiting) || connector.ErrorCode(e) != "control_unreachable" {
		t.Fatal("a responsive daemon with a hung engine must fail its health check", e)
	}
	if ctx.Err() != nil {
		t.Fatal("the plain read must fit in the same budget")
	}
}

// A daemon that answers nothing at all may be a swapping NAS: keep waiting.
func TestNetBirdUnansweredDaemonStaysIncomplete(t *testing.T) {
	s, e := (NetBirdExec{Dir: t.TempDir(), Binaries: fakeBinaries{}, Commands: &silentNetBird{}}).Open(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	p := NewNetBird(t.TempDir(), nil)
	p.session, p.verifiedPort = s, 5000
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if e = p.observeHealth(ctx); !errors.Is(e, errHealthWaiting) {
		t.Fatal("an unanswered daemon must stay incomplete", e)
	}
}

type silentNetBird struct{ nbCommands }

func (c *silentNetBird) Run(ctx context.Context, _ string, _ ...string) ([]byte, error) {
	<-ctx.Done()
	return nil, ErrUnavailable
}

// A sign-in still in flight keeps the engine busy; that is not a failure yet.
func TestNetBirdBusyEngineDuringSignInStaysIncomplete(t *testing.T) {
	c := &busyEngineNetBird{}
	s, e := (NetBirdExec{Dir: t.TempDir(), Binaries: fakeBinaries{}, Commands: c}).Open(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	session := s.(*netbirdExecSession)
	session.done = make(chan struct{})
	p := NewNetBird(t.TempDir(), nil)
	p.session, p.verifiedPort = s, 5000
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if e = p.observeHealth(ctx); !errors.Is(e, errHealthWaiting) {
		t.Fatal("a sign-in in flight must stay incomplete", e)
	}
}

// statusNetBird answers status --json with a fixed daemon state, or waits
// like a busy engine when hung is set.
type statusNetBird struct {
	nbCommands
	daemonStatus string
	hung         bool
}

func (c *statusNetBird) Run(ctx context.Context, binary string, args ...string) ([]byte, error) {
	if strings.Contains(strings.Join(args, " "), "status --json") {
		c.commandFixture.args = append(c.commandFixture.args, append([]string{binary}, args...))
		if c.hung {
			<-ctx.Done()
			return nil, ErrUnavailable
		}
		return []byte(`{"daemonStatus":"` + c.daemonStatus + `","management":{"connected":false},"signal":{"connected":false}}`), nil
	}
	return c.nbCommands.Run(ctx, binary, args...)
}
func (c *statusNetBird) ranLogin() bool {
	for _, a := range c.commandFixture.args {
		for _, arg := range a {
			if arg == "login" || arg == "up" {
				return true
			}
		}
	}
	return false
}

// Regression (a NAS after a repair): the restarted daemon reconnected its
// saved identity by itself and passed its health check; the restore then ran
// netbird login, which cancelled that connection and asked for a new SSO
// sign-in. A restore never signs in again over a daemon that is connecting.
func TestNetBirdRestoreLeavesASelfConnectingDaemonAlone(t *testing.T) {
	for _, c := range []*statusNetBird{{daemonStatus: "Connected"}, {daemonStatus: "Connecting"}, {hung: true}} {
		p := NewNetBird(t.TempDir(), NetBirdExec{Dir: t.TempDir(), Binaries: fakeBinaries{}, Commands: c, ProbeTimeout: 50 * time.Millisecond})
		p.saved = netbirdSaved{Enabled: true, Settings: Settings{ManagementURL: "https://api.netbird.io:443"}}
		d := &netbirdDaemon{p: p}
		p.mu.Lock()
		if e := d.Open(context.Background()); e != nil {
			p.mu.Unlock()
			t.Fatal(e)
		}
		e := d.SignIn(context.Background())
		p.mu.Unlock()
		if session := p.session.(*netbirdExecSession); session.done != nil {
			<-session.done
		}
		if e != nil || c.ranLogin() {
			t.Fatal("a daemon bringing its saved identity up must not be signed in again", c.daemonStatus, c.hung, e)
		}
	}
}

// A daemon that is idle or lost its sign-in still goes through the sign-in.
func TestNetBirdRestoreSignsInAnIdleDaemon(t *testing.T) {
	c := &statusNetBird{daemonStatus: "Idle"}
	p := NewNetBird(t.TempDir(), NetBirdExec{Dir: t.TempDir(), Binaries: fakeBinaries{}, Commands: c, ProbeTimeout: 50 * time.Millisecond})
	p.saved = netbirdSaved{Enabled: true, Settings: Settings{ManagementURL: "https://api.netbird.io:443"}}
	d := &netbirdDaemon{p: p}
	p.mu.Lock()
	defer p.mu.Unlock()
	if e := d.Open(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e := d.SignIn(context.Background()); e != nil {
		t.Fatal(e)
	}
	session := p.session.(*netbirdExecSession)
	<-session.done
	if !c.ranLogin() {
		t.Fatal("an idle daemon must be signed in")
	}
}
