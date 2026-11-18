package provider

import (
	"context"
	"dsmapp/internal/connector"
	"errors"
	"github.com/synopackageland/syno-pkg-land-sdk/supervision"
	"sync"
	"testing"
	"time"
)

// slowTailscaleSession answers like tailscaled on a swapping NAS: reads stall
// or fail for a while, and a new daemon needs several waits before it answers.
type slowTailscaleSession struct {
	mu          sync.Mutex
	snapshot    TailscaleSnapshot
	readErr     error
	block       chan struct{}
	stopped     bool
	exited      bool
	logins      int
	readyErrors int
	exits       bool
}

func (s *slowTailscaleSession) Login(context.Context, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logins++
	return nil
}
func (s *slowTailscaleSession) Inspect(ctx context.Context) (TailscaleSnapshot, error) {
	s.mu.Lock()
	block, snapshot, err := s.block, s.snapshot, s.readErr
	s.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return TailscaleSnapshot{}, ErrUnavailable
		}
	}
	return snapshot, err
}
func (s *slowTailscaleSession) Configure(context.Context, []PortMapping) error { return nil }
func (s *slowTailscaleSession) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	return nil
}
func (s *slowTailscaleSession) LastExit() supervision.Exit {
	return supervision.Exit{Code: 0, Reason: "child_exit"}
}
func (s *slowTailscaleSession) Alive() bool { s.mu.Lock(); defer s.mu.Unlock(); return !s.exited }
func (s *slowTailscaleSession) WaitReady(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readyErrors > 0 {
		s.readyErrors--
		if s.exits {
			s.exited = true
		}
		return connector.Coded("daemon_start_timeout", "private socket unavailable")
	}
	return nil
}
func (s *slowTailscaleSession) set(f func(*slowTailscaleSession)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s)
}
func (s *slowTailscaleSession) signIns() int { s.mu.Lock(); defer s.mu.Unlock(); return s.logins }

type slowTailscaleDriver struct {
	mu      sync.Mutex
	session *slowTailscaleSession
	opens   int
}

func (d *slowTailscaleDriver) Open(context.Context) (TailscaleSession, error) {
	d.mu.Lock()
	d.opens++
	d.mu.Unlock()
	d.session.set(func(s *slowTailscaleSession) { s.exited, s.stopped = false, false })
	return d.session, nil
}
func (d *slowTailscaleDriver) count() int { d.mu.Lock(); defer d.mu.Unlock(); return d.opens }

func runningTailscale() TailscaleSnapshot {
	return TailscaleSnapshot{BackendState: "Running", IPs: []string{"100.64.0.1"}, DNSName: "nas.fixture.ts.net.", Serving: true}
}

// restoredTailscale saves an enabled connection, then restores it with a
// fresh provider the way connectd does after a reboot.
func restoredTailscale(t *testing.T, session *slowTailscaleSession) (*Tailscale, *slowTailscaleDriver) {
	t.Helper()
	return restoredTailscaleAfter(t, session, time.Millisecond)
}
func restoredTailscaleAfter(t *testing.T, session *slowTailscaleSession, retry time.Duration) (*Tailscale, *slowTailscaleDriver) {
	t.Helper()
	dir := t.TempDir()
	first := NewTailscale(dir, &tsDriver{session: &tsSession{snapshot: runningTailscale()}})
	if _, e := first.Start(context.Background(), tsSettings()); e != nil {
		t.Fatal(e)
	}
	first.Stop()
	driver := &slowTailscaleDriver{session: session}
	p := NewTailscale(dir, driver)
	p.RetryDelay = retry
	t.Cleanup(func() { p.Stop() })
	if e := p.Restore(context.Background()); e != nil {
		t.Fatal("a slow daemon must not fail the restore", e)
	}
	return p, driver
}

func waitTailscale(t *testing.T, p *Tailscale, state string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		s := p.Status()
		if s.State == state {
			return
		}
		if s.State != "connecting" && s.State != "waiting" && s.State != "backoff" && s.State != "unknown" {
			t.Fatal("a restoring daemon must read as connecting", s.State, s.Code)
		}
		if time.Now().After(deadline) {
			t.Fatal("restore gave up", s)
		}
		time.Sleep(time.Millisecond)
	}
}

// Regression: on a swapping NAS tailscaled's status reads failed for minutes,
// and Connect stopped it and saved the connection as turned off.
func TestTailscaleSlowStatusReadsNeverStopItOrAskForSignIn(t *testing.T) {
	session := &slowTailscaleSession{snapshot: runningTailscale()}
	p := NewTailscale(t.TempDir(), &slowTailscaleDriver{session: session})
	if _, e := p.Start(context.Background(), tsSettings()); e != nil {
		t.Fatal(e)
	}
	defer p.Stop()
	session.set(func(s *slowTailscaleSession) { s.readErr = context.DeadlineExceeded })
	for i := 0; i < 50; i++ {
		if s := p.Status(); s.State != "unknown" || s.LoginURL != "" {
			t.Fatal("a slow read is not a failure", s.State, s.Code)
		}
	}
	if e := p.CheckHealth(context.Background()); !errors.Is(e, errHealthWaiting) {
		t.Fatal("a slow health read must wait, not count as a failure", e)
	}
	session.mu.Lock()
	stopped := session.stopped
	session.mu.Unlock()
	saved, e := p.read()
	if stopped || e != nil || !saved.Enabled {
		t.Fatal("slow reads stopped tailscaled or turned the connection off", stopped, e)
	}
}

// Only Tailscale itself reporting a missing sign-in asks for one; a daemon
// that is still starting is waited on.
func TestTailscaleHealthAsksForSignInOnlyWhenTailscaleDoes(t *testing.T) {
	session := &slowTailscaleSession{snapshot: runningTailscale()}
	p := NewTailscale(t.TempDir(), &slowTailscaleDriver{session: session})
	if _, e := p.Start(context.Background(), tsSettings()); e != nil {
		t.Fatal(e)
	}
	defer p.Stop()
	for _, state := range []string{"Starting", "NoState"} {
		session.set(func(s *slowTailscaleSession) { s.snapshot.BackendState = state })
		if e := p.CheckHealth(context.Background()); !errors.Is(e, errHealthWaiting) {
			t.Fatal("a starting daemon must be waited on", state, e)
		}
	}
	session.set(func(s *slowTailscaleSession) { s.snapshot.BackendState = "NeedsLogin" })
	if e := p.CheckHealth(context.Background()); connector.ErrorCode(e) != "apply_reauth_required" {
		t.Fatal("a sign-in Tailscale asked for was not reported", e)
	}
}

// An exited daemon fails, but the connection stays turned on so that a
// repair or the next boot starts it again with its saved sign-in.
func TestTailscaleExitedDaemonFailsWithoutTurningTheConnectionOff(t *testing.T) {
	session := &slowTailscaleSession{snapshot: runningTailscale()}
	p := NewTailscale(t.TempDir(), &slowTailscaleDriver{session: session})
	if _, e := p.Start(context.Background(), tsSettings()); e != nil {
		t.Fatal(e)
	}
	defer p.Stop()
	session.set(func(s *slowTailscaleSession) { s.readErr = ErrDaemonExited })
	if s := p.Status(); s.State != "failed" {
		t.Fatal("an exited daemon must fail", s.State)
	}
	if saved, e := p.read(); e != nil || !saved.Enabled {
		t.Fatal("an exited daemon turned the connection off", e)
	}
}

func TestHealthCheckDoesNotHoldTheTailscaleLock(t *testing.T) {
	session := &slowTailscaleSession{snapshot: runningTailscale()}
	p := NewTailscale(t.TempDir(), &slowTailscaleDriver{session: session})
	if _, e := p.Start(context.Background(), tsSettings()); e != nil {
		t.Fatal(e)
	}
	defer p.Stop()
	block := make(chan struct{})
	session.set(func(s *slowTailscaleSession) { s.block = block })
	done := make(chan struct{})
	go func() { defer close(done); _ = p.CheckHealth(context.Background()) }()
	time.Sleep(20 * time.Millisecond)
	locked := make(chan struct{})
	go func() { p.mu.Lock(); p.mu.Unlock(); close(locked) }()
	select {
	case <-locked:
	case <-time.After(time.Second):
		t.Fatal("health check held the Tailscale lock while reading")
	}
	close(block)
	<-done
}

func TestTailscaleRestoreRetriesUntilTheDaemonAnswers(t *testing.T) {
	session := &slowTailscaleSession{snapshot: runningTailscale(), readyErrors: 3, exits: true}
	p, driver := restoredTailscale(t, session)
	if e := p.CheckHealth(context.Background()); !errors.Is(e, errHealthWaiting) && driver.count() < 4 {
		t.Fatal("a restoring daemon must be waited on", e)
	}
	waitTailscale(t, p, "connected")
	if driver.count() != 4 || session.signIns() != 1 {
		t.Fatal("expected three failed starts then one sign-in with the saved identity", driver.count(), session.signIns())
	}
}

func TestTailscaleRestoreKeepsWaitingOnARunningDaemon(t *testing.T) {
	session := &slowTailscaleSession{snapshot: runningTailscale(), readyErrors: 3}
	p, driver := restoredTailscale(t, session)
	waitTailscale(t, p, "connected")
	session.mu.Lock()
	defer session.mu.Unlock()
	if driver.count() != 1 || session.stopped {
		t.Fatal("a slow but running daemon must not be restarted", driver.count(), session.stopped)
	}
}

func TestStopEndsATailscaleRestoreRetry(t *testing.T) {
	session := &slowTailscaleSession{snapshot: runningTailscale(), readyErrors: 1 << 30, exits: true}
	p, driver := restoredTailscale(t, session)
	time.Sleep(10 * time.Millisecond)
	if e := p.Disable(); e != nil {
		t.Fatal(e)
	}
	opens := driver.count()
	time.Sleep(30 * time.Millisecond)
	if driver.count() != opens {
		t.Fatal("a turned-off connection kept starting tailscaled")
	}
	if s := p.Status(); s.State != "disconnected" {
		t.Fatal("a turned-off restore still reads as active", s.State)
	}
}

// A restored identity on a slow NAS can take minutes to reach the control
// server; no new sign-in makes that faster, so it never times out there.
func TestRestoredTailscaleNeverTimesOutReachingControl(t *testing.T) {
	for _, restored := range []bool{false, true} {
		f := &delayedLoginCommands{neverReady: true}
		opened, e := (TailscaleExec{Dir: t.TempDir(), Binaries: fakeBinaries{}, Commands: f, LoginWindow: time.Millisecond}).Open(context.Background())
		if e != nil {
			t.Fatal(e)
		}
		s := opened.(*tailscaleExecSession)
		if restored {
			s.KeepWaiting()
		}
		if e = s.Login(context.Background(), ""); e != nil {
			t.Fatal(e)
		}
		time.Sleep(5 * time.Millisecond)
		_, e = s.Inspect(context.Background())
		if restored && e != nil {
			t.Fatal("a restored daemon timed out reaching control", e)
		}
		if !restored && connector.ErrorCode(e) != "control_unreachable" {
			t.Fatal("a new sign-in must still time out", e)
		}
	}
}

// A repair verifies through the same tolerant reads: a slow one keeps the
// repair waiting, an exited daemon fails it.
func TestTailscaleRepairWaitsOnASlowRead(t *testing.T) {
	session := &slowTailscaleSession{snapshot: runningTailscale()}
	p := NewTailscale(t.TempDir(), &slowTailscaleDriver{session: session})
	if _, e := p.Start(context.Background(), tsSettings()); e != nil {
		t.Fatal(e)
	}
	defer p.Stop()
	if _, ok := any(p).(backgroundRestorer); !ok {
		t.Fatal("a Tailscale repair must wait for its background restore")
	}
	session.set(func(s *slowTailscaleSession) { s.readErr = context.DeadlineExceeded })
	if ready, e := p.VerifyRepair(context.Background()); ready || e != nil {
		t.Fatal("a slow read failed the repair", ready, e)
	}
	session.set(func(s *slowTailscaleSession) { s.readErr = ErrDaemonExited })
	if _, e := p.VerifyRepair(context.Background()); !errors.Is(e, ErrDaemonExited) {
		t.Fatal("an exited daemon must fail the repair", e)
	}
}
