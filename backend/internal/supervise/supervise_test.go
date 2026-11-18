package supervise

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fakeDaemon is a daemon that needs several waits before it answers; with
// exits set, each failed wait ends its process.
type fakeDaemon struct {
	mu                  sync.Mutex
	waits, waitErrors   int
	opens, closes       int
	signIns, signInErrs int
	openErr             error
	exits, alive        bool
	blocking            bool
	noWait              bool
}

func (d *fakeDaemon) Open(context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.opens++
	if d.openErr != nil {
		return d.openErr
	}
	d.alive = true
	return nil
}
func (d *fakeDaemon) Waits() bool { d.mu.Lock(); defer d.mu.Unlock(); return !d.noWait }
func (d *fakeDaemon) WaitReady(ctx context.Context) error {
	d.mu.Lock()
	blocking := d.blocking
	d.mu.Unlock()
	if blocking {
		<-ctx.Done()
		return ctx.Err()
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.waits++
	if d.waitErrors > 0 {
		d.waitErrors--
		if d.exits {
			d.alive = false
		}
		return errors.New("not answering yet")
	}
	return nil
}
func (d *fakeDaemon) Alive() bool { d.mu.Lock(); defer d.mu.Unlock(); return d.alive }
func (d *fakeDaemon) SignIn(context.Context) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.signIns++
	if d.signInErrs > 0 {
		d.signInErrs--
		return errors.New("sign-in did not finish")
	}
	return nil
}
func (d *fakeDaemon) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closes++
	d.alive = false
	return nil
}
func (d *fakeDaemon) counts() (int, int, int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.opens, d.closes, d.signIns
}

func restorer(mu *sync.Mutex) *Restorer {
	return &Restorer{Name: "fixture", Lock: mu, Attempt: func() uint64 { return 1 }, First: time.Millisecond}
}

func waitDone(t *testing.T, mu *sync.Mutex, r *Restorer) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		active := r.Active()
		mu.Unlock()
		if !active {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("restore never finished")
		}
		time.Sleep(time.Millisecond)
	}
}

// A daemon that is still running is waited on, never restarted: on a
// swapping NAS a restart only starts the minutes-long wait over.
func TestRestoreKeepsWaitingOnALiveDaemon(t *testing.T) {
	var mu sync.Mutex
	d := &fakeDaemon{waitErrors: 3}
	r := restorer(&mu)
	mu.Lock()
	if e := r.Begin(context.Background(), d); e != nil {
		t.Fatal(e)
	}
	mu.Unlock()
	waitDone(t, &mu, r)
	opens, closes, signIns := d.counts()
	if opens != 1 || closes != 0 || signIns != 1 {
		t.Fatalf("opens %d closes %d sign-ins %d, want 1 0 1", opens, closes, signIns)
	}
}

// A daemon that exited is started again, with growing waits, until it answers.
func TestRestoreStartsAnExitedDaemonAgain(t *testing.T) {
	var mu sync.Mutex
	d := &fakeDaemon{waitErrors: 3, exits: true}
	r := restorer(&mu)
	mu.Lock()
	if e := r.Begin(context.Background(), d); e != nil {
		t.Fatal(e)
	}
	mu.Unlock()
	waitDone(t, &mu, r)
	opens, closes, signIns := d.counts()
	if opens != 4 || closes != 3 || signIns != 1 {
		t.Fatalf("opens %d closes %d sign-ins %d, want 4 3 1", opens, closes, signIns)
	}
}

// A start that fails outright, such as a binary not yet readable, retries too.
func TestRestoreRetriesAFailedStart(t *testing.T) {
	var mu sync.Mutex
	d := &fakeDaemon{openErr: errors.New("binary unavailable")}
	r := restorer(&mu)
	mu.Lock()
	if e := r.Begin(context.Background(), d); e != nil {
		t.Fatal("a failed start must not fail the restore", e)
	}
	mu.Unlock()
	time.Sleep(20 * time.Millisecond)
	d.mu.Lock()
	d.openErr = nil
	d.mu.Unlock()
	waitDone(t, &mu, r)
	if opens, _, signIns := d.counts(); opens < 2 || signIns != 1 {
		t.Fatal("a failed start was not retried", opens, signIns)
	}
}

// A daemon that answers at once signs in before Begin returns.
func TestRestoreOfADaemonThatNeedsNoWaitIsImmediate(t *testing.T) {
	var mu sync.Mutex
	d := &fakeDaemon{noWait: true}
	r := restorer(&mu)
	mu.Lock()
	defer mu.Unlock()
	if e := r.Begin(context.Background(), d); e != nil || r.Active() {
		t.Fatal("an immediate daemon restored in the background", e)
	}
	if _, _, signIns := d.counts(); signIns != 1 {
		t.Fatal("no sign-in", signIns)
	}
	d.signInErrs = 1
	if e := r.Begin(context.Background(), d); e == nil {
		t.Fatal("an immediate failed sign-in was not reported")
	}
}

// Cancel ends the loop and its timers: nothing starts afterwards.
func TestCancelEndsTheRestore(t *testing.T) {
	var mu sync.Mutex
	d := &fakeDaemon{waitErrors: 1 << 30, exits: true}
	r := restorer(&mu)
	mu.Lock()
	_ = r.Begin(context.Background(), d)
	mu.Unlock()
	time.Sleep(10 * time.Millisecond)
	mu.Lock()
	done := r.Cancel()
	mu.Unlock()
	<-done
	opens, _, _ := d.counts()
	time.Sleep(20 * time.Millisecond)
	if again, _, _ := d.counts(); again != opens {
		t.Fatal("a cancelled restore kept starting the daemon")
	}
	mu.Lock()
	defer mu.Unlock()
	if r.Active() {
		t.Fatal("a cancelled restore reads as active")
	}
}

// The readiness wait never holds the provider lock.
func TestRestoreWaitsWithoutTheLock(t *testing.T) {
	var mu sync.Mutex
	d := &fakeDaemon{blocking: true}
	r := restorer(&mu)
	mu.Lock()
	_ = r.Begin(context.Background(), d)
	mu.Unlock()
	locked := make(chan struct{})
	go func() { mu.Lock(); mu.Unlock(); close(locked) }()
	select {
	case <-locked:
	case <-time.After(time.Second):
		t.Fatal("the readiness wait held the provider lock")
	}
	mu.Lock()
	done := r.Cancel()
	mu.Unlock()
	<-done
}

func TestBackoffDoublesToItsCapAndResets(t *testing.T) {
	b := Backoff{First: time.Second, Max: 5 * time.Second}
	var got []time.Duration
	for i := 0; i < 5; i++ {
		got = append(got, b.Next())
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 5 * time.Second, 5 * time.Second}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("waits %v, want %v", got, want)
		}
	}
	b.Reset()
	if b.Next() != time.Second {
		t.Fatal("reset did not start over")
	}
}

func TestReadyWaitsForAnAnswerAndStopsOnExit(t *testing.T) {
	reads := 0
	alive := true
	r := Readiness{Window: time.Second, Probe: 10 * time.Millisecond, Pause: time.Millisecond, Alive: func() bool { return alive }, Read: func(context.Context) error {
		reads++
		if reads < 3 {
			return errors.New("busy")
		}
		return nil
	}}
	if e := r.Wait(context.Background()); e != nil || reads != 3 {
		t.Fatal("did not wait for the answer", e, reads)
	}
	alive = false
	if e := r.Wait(context.Background()); !errors.Is(e, ErrExited) {
		t.Fatal("an exited daemon was waited on", e)
	}
	alive = true
	r.Window = 20 * time.Millisecond
	r.Read = func(context.Context) error { return errors.New("busy") }
	if e := r.Wait(context.Background()); !errors.Is(e, ErrNotReady) {
		t.Fatal("the wait outlived its window", e)
	}
}

// A read that took the whole probe time is bounded by the probe, not the window.
func TestReadyBoundsEachRead(t *testing.T) {
	var bounded bool
	r := Readiness{Window: time.Second, Probe: 5 * time.Millisecond, Pause: time.Millisecond, Alive: func() bool { return true }, Read: func(ctx context.Context) error {
		deadline, ok := ctx.Deadline()
		bounded = ok && time.Until(deadline) <= 5*time.Millisecond
		return nil
	}}
	if e := r.Wait(context.Background()); e != nil || !bounded {
		t.Fatal("a read was not bounded by the probe time", e)
	}
}
