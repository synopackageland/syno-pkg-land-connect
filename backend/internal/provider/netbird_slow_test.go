package provider

import (
	"context"
	"dsmapp/internal/connector"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// slowNetBirdSession answers like a daemon on a swapping NAS: reads stall or
// fail for a while, then answer again. It never forgets its sign-in.
type slowNetBirdSession struct {
	mu          sync.Mutex
	snapshot    NetBirdSnapshot
	readErr     error
	block       chan struct{}
	stopped     bool
	logins      int
	readyErrors int
	exits       bool
}

func (s *slowNetBirdSession) Login(context.Context, string, string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logins++
	return nil
}
func (s *slowNetBirdSession) Inspect(ctx context.Context) (NetBirdSnapshot, error) {
	s.mu.Lock()
	block, snapshot, err := s.block, s.snapshot, s.readErr
	s.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done():
			return NetBirdSnapshot{}, ErrUnavailable
		}
	}
	return snapshot, err
}
func (s *slowNetBirdSession) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopped = true
	return nil
}
func (s *slowNetBirdSession) CheckHTTP(context.Context, string, int) error { return nil }
func (s *slowNetBirdSession) set(f func(*slowNetBirdSession)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f(s)
}

// readySession is a daemon that needs several waits before it answers; with
// exits set, each failed wait ends the process.
type readySession struct{ *slowNetBirdSession }

func (s readySession) Alive() bool { s.mu.Lock(); defer s.mu.Unlock(); return !s.exits }

func (s readySession) WaitReady(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readyErrors > 0 {
		s.readyErrors--
		return nbError("daemon_start_timeout")
	}
	// The final successful start stays alive for lifetime supervision.
	s.exits = false
	return nil
}

type slowNetBirdDriver struct {
	mu      sync.Mutex
	session NetBirdSession
	opens   int
}

func (d *slowNetBirdDriver) Open(context.Context) (NetBirdSession, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.opens++
	return d.session, nil
}
func (d *slowNetBirdDriver) count() int { d.mu.Lock(); defer d.mu.Unlock(); return d.opens }

func connectedSnapshot() NetBirdSnapshot {
	return NetBirdSnapshot{DaemonStatus: "Connected", ManagementConnected: true, SignalConnected: true, IP: "100.88.0.2/16"}
}

func slowNetBird(t *testing.T) (*MultiSelection, *NetBird, *slowNetBirdSession, *slowNetBirdDriver) {
	t.Helper()
	session := &slowNetBirdSession{snapshot: connectedSnapshot()}
	driver := &slowNetBirdDriver{session: session}
	dir := t.TempDir()
	nb := NewNetBird(filepath.Join(dir, "netbird"), driver)
	nb.HTTPPort = func(context.Context) (int, error) { return 5000, nil }
	m := NewMultiSelection(dir, nb)
	if _, e := m.Connect(context.Background(), connector.Request{Provider: "netbird", Token: "SYNTHETIC", ManagementURL: "https://api.netbird.io:443", ConfirmExposure: true, ConfirmManagementPolicy: true}); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { m.Stop() })
	return m, nb, session, driver
}

// Regression: under memory pressure, status reads failed for minutes and
// Connect stopped NetBird, which then asked for a new sign-in.
func TestNetBirdSlowStatusReadsNeverStopItOrAskForSignIn(t *testing.T) {
	m, nb, session, driver := slowNetBird(t)
	child := m.children["netbird"]
	session.set(func(s *slowNetBirdSession) { s.readErr = ErrUnavailable })
	for i := 0; i < 20; i++ {
		if s := nb.Status(); s.State != "unknown" || s.Code != "" {
			t.Fatalf("read %d: a failed read must read as unknown, got %s %q", i, s.State, s.Code)
		}
		child.healthTick(context.Background())
	}
	if session.stopped || driver.count() != 1 {
		t.Fatal("failed status reads stopped or restarted NetBird", session.stopped, driver.count())
	}
	if a := child.Status().Apply; a != nil && (a.Phase == "reauth-required" || a.Phase == "repairing") {
		t.Fatal("failed status reads asked for sign-in or repaired", a)
	}
	raw, e := os.ReadFile(filepath.Join(nb.dir, "settings.json"))
	if e != nil || !strings.Contains(string(raw), `"enabled":true`) {
		t.Fatal("failed status reads disabled the saved identity", string(raw), e)
	}
	session.set(func(s *slowNetBirdSession) { s.readErr = nil })
	if s := m.StatusFor("netbird"); s.State != "connected" {
		t.Fatal("NetBird did not come back after reads recovered", s.State)
	}
}

func TestNetBirdLostControlServerIsNotASignInProblem(t *testing.T) {
	m, _, session, _ := slowNetBird(t)
	child := m.children["netbird"]
	child.mu.Lock()
	child.lastGood = selectionState{}
	child.mu.Unlock()
	session.set(func(s *slowNetBirdSession) { s.snapshot.ManagementConnected = false })
	for i := 0; i < 3; i++ {
		child.healthTick(context.Background())
	}
	if a := child.Status().Apply; a == nil || a.Phase != "check-failed" || a.Code != "control_unreachable" {
		t.Fatal("a lost control server must not ask for sign-in", a)
	}
	session.set(func(s *slowNetBirdSession) { s.snapshot = NetBirdSnapshot{DaemonStatus: "NeedsLogin"} })
	for i := 0; i < 3; i++ {
		child.healthTick(context.Background())
	}
	if a := child.Status().Apply; a == nil || a.Phase != "reauth-required" {
		t.Fatal("NetBird's own NeedsLogin must ask for sign-in", a)
	}
}

func TestNetBirdExitedDaemonStillFails(t *testing.T) {
	_, nb, session, _ := slowNetBird(t)
	session.set(func(s *slowNetBirdSession) { s.readErr = ErrDaemonExited })
	if s := nb.Status(); s.State != "failed" || !session.stopped {
		t.Fatal("an exited daemon must fail", s.State)
	}
}

// A minute-long read on a loaded NAS must not hold the owner lock.
func TestHealthCheckDoesNotBlockStatus(t *testing.T) {
	m, _, session, _ := slowNetBird(t)
	child := m.children["netbird"]
	block := make(chan struct{})
	session.set(func(s *slowNetBirdSession) { s.block = block })
	done := make(chan struct{})
	go func() { defer close(done); child.healthTick(context.Background()) }()
	time.Sleep(20 * time.Millisecond)
	read := make(chan connector.Status, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		nb := child.registry["netbird"].(*NetBird)
		nb.mu.Lock()
		nb.mu.Unlock()
		child.mu.Lock()
		child.mu.Unlock()
		select {
		case <-ctx.Done():
		default:
			read <- connector.Status{State: "ok"}
		}
	}()
	select {
	case <-read:
	case <-time.After(time.Second):
		t.Fatal("health check held the provider or owner lock while reading")
	}
	close(block)
	<-done
}

func TestNetBirdRestoreRetriesUntilTheDaemonAnswers(t *testing.T) {
	session := &slowNetBirdSession{snapshot: connectedSnapshot(), readyErrors: 3, exits: true}
	driver := &slowNetBirdDriver{session: readySession{session}}
	dir := t.TempDir()
	first := NewNetBird(dir, &slowNetBirdDriver{session: session})
	if _, e := first.Start(context.Background(), nbSettings()); e != nil {
		t.Fatal(e)
	}
	first.Stop()
	nb := NewNetBird(dir, driver)
	nb.RetryDelay = time.Millisecond
	defer nb.Stop()
	if e := nb.Restore(context.Background()); e != nil {
		t.Fatal("a slow daemon must not fail the restore", e)
	}
	deadline := time.Now().Add(5 * time.Second)
	for nb.Status().State != "connected" {
		if s := nb.Status(); s.State != "connecting" && s.State != "waiting" && s.State != "backoff" && s.State != "connected" {
			t.Fatal("a restarting daemon must read as connecting", s.State, s.Code)
		}
		if time.Now().After(deadline) {
			t.Fatal("restore gave up", nb.Status())
		}
		time.Sleep(time.Millisecond)
	}
	if driver.count() != 4 || session.logins != 2 {
		t.Fatal("expected three failed starts then one sign-in-free login", driver.count(), session.logins)
	}
}

func TestNetBirdRestoreKeepsWaitingOnARunningDaemon(t *testing.T) {
	session := &slowNetBirdSession{snapshot: connectedSnapshot(), readyErrors: 3}
	dir := t.TempDir()
	first := NewNetBird(dir, &slowNetBirdDriver{session: session})
	if _, e := first.Start(context.Background(), nbSettings()); e != nil {
		t.Fatal(e)
	}
	first.Stop()
	session.set(func(s *slowNetBirdSession) { s.stopped = false })
	driver := &slowNetBirdDriver{session: readySession{session}}
	nb := NewNetBird(dir, driver)
	nb.RetryDelay = time.Millisecond
	defer nb.Stop()
	if e := nb.Restore(context.Background()); e != nil {
		t.Fatal(e)
	}
	deadline := time.Now().Add(5 * time.Second)
	for nb.Status().State != "connected" {
		if time.Now().After(deadline) {
			t.Fatal("restore gave up", nb.Status())
		}
		time.Sleep(time.Millisecond)
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if driver.count() != 1 || session.stopped {
		t.Fatal("a slow but running daemon must not be restarted", driver.count(), session.stopped)
	}
}

func TestStopEndsARestoreRetry(t *testing.T) {
	session := &slowNetBirdSession{snapshot: connectedSnapshot(), readyErrors: 1 << 30}
	dir := t.TempDir()
	first := NewNetBird(dir, &slowNetBirdDriver{session: session})
	if _, e := first.Start(context.Background(), nbSettings()); e != nil {
		t.Fatal(e)
	}
	first.Stop()
	driver := &slowNetBirdDriver{session: readySession{session}}
	nb := NewNetBird(dir, driver)
	nb.RetryDelay = time.Millisecond
	if e := nb.Restore(context.Background()); e != nil {
		t.Fatal(e)
	}
	time.Sleep(20 * time.Millisecond)
	if e := nb.Stop(); e != nil {
		t.Fatal(e)
	}
	opens := driver.count()
	time.Sleep(50 * time.Millisecond)
	if driver.count() != opens {
		t.Fatal("a stopped restore kept starting the daemon")
	}
}

type repairingFixture struct {
	safeFixture
	mu      sync.Mutex
	health  error
	repairs int
}

func (*repairingFixture) PrecheckRepair(context.Context) error { return nil }
func (p *repairingFixture) RepairUpdate(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.repairs++
	return nil
}
func (*repairingFixture) VerifyRepair(context.Context) (bool, error) { return true, nil }
func (p *repairingFixture) CheckHealth(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.health
}
func (p *repairingFixture) setHealth(e error) { p.mu.Lock(); p.health = e; p.mu.Unlock() }

// Regression: each repair that passed once reset the cap, so NetBird was
// restarted every few minutes forever.
func TestRepairCapHoldsAcrossSuccessfulRepairs(t *testing.T) {
	p := &repairingFixture{safeFixture: safeFixture{fixtureRuntime: fixtureRuntime{id: "netbird", events: &[]string{}}}}
	m := NewSelection(t.TempDir(), p)
	if _, e := m.Connect(context.Background(), connector.Request{Provider: "netbird"}); e != nil {
		t.Fatal(e)
	}
	m.lastGood = cloneState(m.saved)
	for round := 0; round < 6; round++ {
		// A repair passes its check, then health fails again soon after.
		p.setHealth(nbError("control_unreachable"))
		for i := 0; i < 3; i++ {
			m.healthTick(context.Background())
		}
		settle(t, m)
		m.mu.Lock()
		m.nextRepair = time.Time{}
		m.mu.Unlock()
		p.setHealth(nil)
		m.healthTick(context.Background())
	}
	if p.repairs != 3 {
		t.Fatal("repairs must stop at three until a sustained healthy window", p.repairs)
	}
	p.setHealth(nbError("control_unreachable"))
	for i := 0; i < 3; i++ {
		m.healthTick(context.Background())
	}
	if a := m.Status().Apply; a == nil || a.Phase != "recovery-required" {
		t.Fatal("exhausted repairs must say recovery is required", a)
	}
	// Only a sustained healthy window gives the budget back.
	p.setHealth(nil)
	m.healthTick(context.Background())
	m.mu.Lock()
	m.healthySince = time.Now().Add(-healthyWindow)
	m.mu.Unlock()
	m.healthTick(context.Background())
	if m.repairAttempts != 0 {
		t.Fatal("a sustained healthy window must reset the repair cap", m.repairAttempts)
	}
}

// settle waits for a pending repair to finish.
func settle(t *testing.T, m *Selection) {
	t.Helper()
	m.mu.Lock()
	tx := m.pending
	m.mu.Unlock()
	if tx == nil {
		return
	}
	select {
	case <-tx.done:
	case <-time.After(5 * time.Second):
		t.Fatal("repair did not settle")
	}
}

func TestIncompleteHealthNeverCountsOrRepairs(t *testing.T) {
	p := &repairingFixture{safeFixture: safeFixture{fixtureRuntime: fixtureRuntime{id: "netbird", events: &[]string{}}}}
	m := NewSelection(t.TempDir(), p)
	if _, e := m.Connect(context.Background(), connector.Request{Provider: "netbird"}); e != nil {
		t.Fatal(e)
	}
	m.lastGood = cloneState(m.saved)
	p.setHealth(healthWaiting("status", 7, errors.New("read timed out")))
	for i := 0; i < 10; i++ {
		m.healthTick(context.Background())
	}
	if p.repairs != 0 || m.healthFailures != 0 || m.Status().Apply != nil {
		t.Fatal("an incomplete observation counted as a failure", p.repairs, m.healthFailures)
	}
}

func TestMemoryPressurePostponesRepair(t *testing.T) {
	p := &repairingFixture{safeFixture: safeFixture{fixtureRuntime: fixtureRuntime{id: "netbird", events: &[]string{}}}}
	m := NewSelection(t.TempDir(), p)
	if _, e := m.Connect(context.Background(), connector.Request{Provider: "netbird"}); e != nil {
		t.Fatal(e)
	}
	m.lastGood = cloneState(m.saved)
	pressure := true
	m.pressure = func() bool { return pressure }
	p.setHealth(nbError("control_unreachable"))
	for i := 0; i < 6; i++ {
		m.healthTick(context.Background())
	}
	if p.repairs != 0 || m.repairAttempts != 0 {
		t.Fatal("a swapping NAS must not restart NetBird", p.repairs)
	}
	pressure = false
	m.healthTick(context.Background())
	settle(t, m)
	if p.repairs != 1 {
		t.Fatal("repair must resume once pressure ends", p.repairs)
	}
}
