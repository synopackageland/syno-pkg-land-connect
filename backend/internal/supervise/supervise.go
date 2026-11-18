// Package supervise holds the one tolerant supervision path shared by every
// provider daemon (NetBird, Tailscale, cloudflared). On a swapping NAS a
// daemon can take minutes to answer: a slow read means waiting, never a
// restart, a stop or a new sign-in. Providers keep only their own parts
// (status parsing, sign-in states) behind Daemon.
package supervise

import (
	"context"
	"errors"
	"sync"
	"time"

	"dsmapp/internal/connector"
	"github.com/synopackageland/syno-pkg-land-sdk/supervision"
)

// ErrExited reports a daemon process that ended while it was waited on.
var ErrExited = errors.New("daemon exited")

// ErrNotReady reports a daemon that did not answer within its window.
var ErrNotReady = errors.New("daemon did not answer")

// Backoff yields waits from First, doubling up to Max.
type Backoff struct {
	First, Max time.Duration
	next       time.Duration
}

func (b *Backoff) Next() time.Duration {
	if b.next <= 0 {
		b.next = b.First
	}
	d := b.next
	b.next = min(2*d, b.Max)
	return d
}
func (b *Backoff) Reset() { b.next = 0 }

// Readiness waits for a new daemon to answer one read. Each read is bounded
// by Probe; the reads back off from Pause to 2 s rather than add load.
type Readiness struct {
	Window, Probe, Pause time.Duration
	Alive                func() bool
	Read                 func(context.Context) error
}

func (r Readiness) Wait(ctx context.Context) error {
	window, cancel := context.WithTimeout(ctx, r.Window)
	defer cancel()
	pause := Backoff{First: r.Pause, Max: 2 * time.Second}
	if pause.First <= 0 {
		pause.First = 100 * time.Millisecond
	}
	for {
		if !r.Alive() {
			return ErrExited
		}
		probe, stop := context.WithTimeout(window, r.Probe)
		e := r.Read(probe)
		stop()
		if e == nil {
			return nil
		}
		select {
		case <-window.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return ErrNotReady
		case <-time.After(pause.Next()):
		}
	}
}

// Daemon is one provider's restorable daemon. Every method except WaitReady
// runs with the provider lock held.
type Daemon interface {
	// Open starts the daemon from the saved identity.
	Open(context.Context) error
	// Waits reports whether the opened daemon may take minutes to answer;
	// one that does not signs in before Begin returns.
	Waits() bool
	// WaitReady waits, without the lock, for the daemon to answer.
	WaitReady(context.Context) error
	// Alive reports whether the opened daemon still runs.
	Alive() bool
	// SignIn brings the saved identity up; it never asks for a new sign-in.
	SignIn(context.Context) error
	// Close stops the daemon before it is started again.
	Close() error
}

// Restorer adapts provider readiness/sign-in to the SDK's one restart policy.
// All lifecycle methods run under Lock; a live, slow daemon waits without
// spending failures. Monitor retains lifetime ownership after restore completes.
type Restorer struct {
	Name       string
	Lock       sync.Locker
	Attempt    func() uint64
	First, Max time.Duration
	Retrying   func()
	Monitor    bool
	Dependency func(context.Context) error
	Preflight  func(context.Context) error
	Changed    func()
	Transition func(supervision.Snapshot)
	run        *restoreRun
	policy     *supervision.Policy
	lastState  supervision.State
}
type restoreRun struct {
	cancel context.CancelFunc
	done   chan struct{}
	policy *supervision.Policy
	ready  bool
}

func (r *Restorer) Snapshot() supervision.Snapshot {
	if r.policy == nil {
		return supervision.Snapshot{State: supervision.Stopped}
	}
	s := r.policy.Snapshot()
	s.Log = "daemon.log"
	return s
}
func (r *Restorer) Policy() *supervision.Policy { return r.policy }
func (r *Restorer) notify() {
	s := r.Snapshot()
	if s.State == r.lastState {
		return
	}
	r.lastState = s.State
	if r.Changed != nil {
		r.Changed()
	}
	connector.Logf("%s: state=%s attempts=%d next=%s reason=%s", r.Name, s.State, s.Attempts, s.NextRetry.UTC().Format(time.RFC3339), s.Reason)
	if r.Transition != nil {
		r.Transition(s)
	}
}
func (r *Restorer) Begin(ctx context.Context, d Daemon) error {
	r.Cancel()
	cfg := supervision.Defaults()
	if r.First > 0 {
		cfg.Initial = r.First
		cfg.Min = min(time.Second, r.First)
	}
	if r.Max > 0 {
		cfg.Max = r.Max
	}
	r.policy = supervision.New(cfg, nil)
	r.lastState = ""
	runCtx, cancel := context.WithCancel(context.Background())
	run := &restoreRun{cancel: cancel, done: make(chan struct{}), policy: r.policy}
	r.run = run
	var e error
	if r.Preflight != nil {
		e = r.Preflight(ctx)
	}
	opened := false
	// Production dependency checks run outside the provider lock. Fixture and
	// local daemons keep their immediate-open contract.
	if r.Dependency == nil && e == nil {
		r.policy.Started(time.Now())
		e = d.Open(ctx)
		opened = e == nil
		if e == nil && !d.Waits() {
			e = d.SignIn(ctx)
			if e == nil {
				r.policy.Running()
				r.run = nil
				cancel()
				close(run.done)
				return nil
			}
			if e != nil {
				r.run = nil
				cancel()
				close(run.done)
				return e
			}
		}
	}
	go r.loop(runCtx, run, d, e, opened)
	return nil
}
func (r *Restorer) Active() bool { return r.run != nil && !r.run.ready }
func (r *Restorer) Cancel() <-chan struct{} {
	if r.policy != nil {
		r.policy.Stop()
	}
	return r.cancelRun()
}
func (r *Restorer) cancelRun() <-chan struct{} {
	if r.run == nil {
		done := make(chan struct{})
		close(done)
		return done
	}
	run := r.run
	run.cancel()
	r.run = nil
	return run.done
}
func (r *Restorer) loop(ctx context.Context, run *restoreRun, d Daemon, e error, opened bool) {
	defer close(run.done)
	defer run.cancel()
	policy := run.policy
	for {
		if ctx.Err() != nil {
			return
		}
		if !opened && e == nil {
			if r.Dependency != nil {
				policy.Waiting("network")
				r.Lock.Lock()
				if r.run != run {
					r.Lock.Unlock()
					return
				}
				r.notify()
				r.Lock.Unlock()
				if err := supervision.Dependency(ctx, r.Dependency, nil); err != nil {
					return
				}
			}
			r.Lock.Lock()
			if r.run != run {
				r.Lock.Unlock()
				return
			}
			policy.Started(time.Now())
			e = d.Open(ctx)
			opened = e == nil
			r.notify()
			r.Lock.Unlock()
			// Briefly yield after launch; the process owner holds its host lease.
			err := supervision.Wait(ctx, min(time.Second, max(time.Millisecond, r.First)))
			if err != nil {
				return
			}
		}
		if e == nil && opened && d.Waits() {
			e = d.WaitReady(ctx)
		}
		r.Lock.Lock()
		if r.run != run || ctx.Err() != nil {
			r.Lock.Unlock()
			return
		}
		if e == nil {
			e = d.SignIn(ctx)
		}
		if e == nil {
			policy.Running()
			run.ready = true
			r.notify()
			if !r.Monitor {
				r.run = nil
				r.Lock.Unlock()
				return
			}
			r.Lock.Unlock()
			// Lifetime observation is the sole restart owner. No health repair may
			// rearm its bounded series. Cancellation has no timers left behind.
			for {
				if supervision.Wait(ctx, observation(r.First)) != nil {
					return
				}
				r.Lock.Lock()
				if r.run != run {
					r.Lock.Unlock()
					return
				}
				alive := d.Alive()
				r.Lock.Unlock()
				if !alive {
					e = ErrExited
					break
				}
			}
			r.Lock.Lock()
			if r.run != run {
				r.Lock.Unlock()
				return
			}
			run.ready = false
		}
		permanent := Permanent(r.Name, e)
		if !permanent && d.Alive() {
			policy.Waiting("daemon")
			r.notify()
			r.Lock.Unlock()
			if supervision.Wait(ctx, observation(r.First)) != nil {
				return
			}
			e = nil
			continue
		}
		exit := supervision.Exit{Code: -1, Reason: connector.ErrorCode(e), Permanent: permanent}
		if exit.Reason == "" {
			exit.Reason = "daemon_exit"
		}
		if x, ok := d.(interface{ LastExit() supervision.Exit }); ok && !permanent {
			exit = x.LastExit()
		}
		if err := d.Close(); err != nil {
			exit.Permanent = true
			exit.Reason = "cleanup_required"
		}
		// Started is set at Open, never at a readiness/sign-in poll.
		s := policy.Exited(time.Now(), exit)
		r.notify()
		if s.State != supervision.Backoff {
			r.run = nil
			r.Lock.Unlock()
			return
		}
		if r.Retrying != nil {
			r.Retrying()
		}
		r.Lock.Unlock()
		if supervision.Wait(ctx, time.Until(s.NextRetry)) != nil {
			return
		}
		e = nil
		opened = false
	}
}
func observation(first time.Duration) time.Duration {
	if first > 0 {
		return min(first, 2*time.Second)
	}
	return 2 * time.Second
}

// Permanent maps structured provider refusals. Exit 1 is deliberately not
// guessed fatal: all upstream children use it for multiple unrelated failures.
func Permanent(name string, err error) bool {
	if supervision.PermanentStart(err) {
		return true
	}
	switch connector.ErrorCode(err) {
	case "cloudflare_name_taken", "binary_required", "invalid_config", "invalid_token", "cloudflare_invalid_token", "cloudflare_tunnel_missing", "cloudflare_authorization_required", "login_denied", "login_expired", "login_url_rejected", "invalid_management_url", "netbird_invalid_management_url", "not_configured", "server_not_configured", "netbird_server_off", "apply_reauth_required", "serve_prerequisite_missing":
		return true
	}
	return false
}

// Adopt watches an already signed-in owned daemon. A user start is a new
// trigger; subsequent process deaths spend this series instead of health repair.
func (r *Restorer) Adopt(d Daemon) {
	r.Cancel()
	cfg := supervision.Defaults()
	if r.First > 0 {
		cfg.Initial = r.First
		cfg.Min = min(time.Second, r.First)
	}
	if r.Max > 0 {
		cfg.Max = r.Max
	}
	r.policy = supervision.New(cfg, nil)
	r.policy.Started(time.Now())
	r.policy.Running()
	r.lastState = ""
	ctx, cancel := context.WithCancel(context.Background())
	run := &restoreRun{cancel: cancel, done: make(chan struct{}), ready: true, policy: r.policy}
	r.run = run
	go func() {
		for {
			if supervision.Wait(ctx, observation(r.First)) != nil {
				close(run.done)
				return
			}
			r.Lock.Lock()
			if r.run != run {
				r.Lock.Unlock()
				close(run.done)
				return
			}
			alive := d.Alive()
			r.Lock.Unlock()
			if !alive {
				r.Lock.Lock()
				if r.run != run {
					r.Lock.Unlock()
					close(run.done)
					return
				}
				run.ready = false
				r.Lock.Unlock()
				r.loop(ctx, run, d, ErrExited, true)
				return
			}
		}
	}()
}
func (r *Restorer) Refuse(code string) {
	if r.policy == nil {
		return
	}
	r.cancelRun()
	r.policy.Exited(time.Now(), supervision.Exit{Code: -1, Reason: code, Permanent: true})
	r.notify()
}
