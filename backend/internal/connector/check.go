package connector

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrCheckTimeout names a probe that did not answer within Checker.Timeout.
var ErrCheckTimeout = Coded("check_timeout", "the connection check did not answer in time")

// ErrNotConnected names a provider whose connection is not running.
var ErrNotConnected = Coded("not_connected", "the connection is not running")

// ErrCheckStarting asks the UI to keep checking while a new address settles.
// It does not claim external reachability or change the provider's lifecycle.
var ErrCheckStarting = Coded("check_starting", "the address is getting ready")

var errCheckFailed = Coded("check_failed", "the connection check failed")

// Probe checks one provider from the NAS. external reports that the probe
// reached the NAS through the provider's public path, not only locally.
type Probe func(ctx context.Context, provider string) (external bool, err error)

// CheckResult is the last connection check of one provider. CheckedAt is
// when it finished, in UTC; it is empty while the first check still runs.
type CheckResult struct {
	Provider         string `json:"provider"`
	State            string `json:"state"` // not-checked, checking, starting, ok or failed
	Code             string `json:"code,omitempty"`
	ExternalVerified bool   `json:"externalVerified"`
	CheckedAt        string `json:"checkedAt,omitempty"`
	Attempts         int    `json:"attempts,omitempty"`
}

// DNSSettle is how long a new address may take to resolve and reach the
// tunnel. Cloudflare's probe and the checker's settling window share it.
const DNSSettle = 3 * time.Minute

// SettleWindow is how long a connection that was just made, or restored at
// boot, may fail its checks while it reads as setting up. DNS-based ways
// wait for a new name; the others only for their daemon to join.
func SettleWindow(provider string) time.Duration {
	switch provider {
	case "cloudflare", "workers":
		return DNSSettle
	}
	return time.Minute
}

// Checker runs connection checks in the daemon and keeps each provider's
// last result. A provider is checked at most once per Interval; a check
// still running is joined, never started twice. A connection that was just
// made, or restored when the daemon started, has not settled yet (DNS, the
// relay): until its Settle window ends a failed attempt reads "starting" and
// is retried with backoff, and the first passing attempt reads "ok". Only a
// failure after the window reports "failed". Outside a window a fresh
// connection tries up to Retries times, RetryDelay apart.
type Checker struct {
	Probe      Probe
	Timeout    time.Duration // per attempt
	Interval   time.Duration // rate limit per provider
	Retries    int           // attempts for a fresh connection
	RetryDelay time.Duration
	Accept     time.Duration // how long a caller waits before reading "checking"
	Settle     func(provider string) time.Duration
	Now        func() time.Time
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	entries    map[string]*checkEntry
	born       time.Time            // the boot window's start
	since      map[string]time.Time // a connection's window start
}

type checkEntry struct {
	result  CheckResult
	started time.Time
	settled bool // a check finished since the connection was made
	done    chan struct{}
	stop    chan struct{} // closed when a reset replaces this connection
}

func NewChecker(probe Probe) *Checker {
	ctx, cancel := context.WithCancel(context.Background())
	return &Checker{Probe: probe, Timeout: 10 * time.Second, Interval: 30 * time.Second, Retries: 3, RetryDelay: 5 * time.Second, Accept: 4 * time.Second, Settle: SettleWindow, Now: time.Now, ctx: ctx, cancel: cancel, entries: map[string]*checkEntry{}, born: time.Now(), since: map[string]time.Time{}}
}

// Begin anchors the boot window, when the daemon restores its connections.
func (c *Checker) Begin(at time.Time) {
	c.mu.Lock()
	c.born = at
	c.mu.Unlock()
}

// Close cancels running checks.
func (c *Checker) Close() { c.cancel() }

// Reset forgets a provider's result because its connection was just made;
// the next check runs at once, and the connection settles from now. A check
// still running for the old connection stops unseen.
func (c *Checker) Reset(provider string) {
	c.mu.Lock()
	if e := c.entries[provider]; e != nil {
		close(e.stop)
	}
	delete(c.entries, provider)
	c.since[provider] = c.Now()
	c.mu.Unlock()
}

// Last returns the provider's last result without starting a check.
func (c *Checker) Last(provider string) CheckResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.entry(provider).result
}

// Check starts a check unless one ran within Interval or is still running,
// then waits up to Accept for it.
func (c *Checker) Check(ctx context.Context, provider string) CheckResult {
	c.mu.Lock()
	e := c.entry(provider)
	if e.done == nil && (e.started.IsZero() || c.Now().Sub(e.started) >= c.Interval) {
		attempts := 1
		if !e.settled {
			attempts = c.Retries
		}
		start, ok := c.since[provider]
		if !ok {
			start = c.born
		}
		e.started, e.done = c.Now(), make(chan struct{})
		e.result.State, e.result.Code = "checking", ""
		go c.run(e, provider, attempts, start.Add(c.Settle(provider)))
	}
	done := e.done
	c.mu.Unlock()
	if done != nil {
		timer := time.NewTimer(c.Accept)
		defer timer.Stop()
		select {
		case <-done:
		case <-timer.C:
		case <-ctx.Done():
		}
	}
	return c.Last(provider)
}

func (c *Checker) entry(provider string) *checkEntry {
	e := c.entries[provider]
	if e == nil {
		e = &checkEntry{result: CheckResult{Provider: provider, State: "not-checked"}, stop: make(chan struct{})}
		c.entries[provider] = e
	}
	return e
}

func (c *Checker) run(e *checkEntry, provider string, attempts int, settleUntil time.Time) {
	var external bool
	var err error
	n := 0
	for {
		n++
		if external, err = c.attempt(provider); err == nil {
			break
		}
		delay := c.RetryDelay
		if c.Now().Before(settleUntil) {
			// Still settling: say so at once and keep trying, never fail.
			c.mu.Lock()
			e.result = CheckResult{Provider: provider, State: "starting", Code: checkCode(err), CheckedAt: c.Now().UTC().Format(time.RFC3339), Attempts: n}
			c.mu.Unlock()
			delay = c.backoff(n)
		} else if n >= attempts {
			break
		}
		if !c.wait(delay, e.stop) {
			break
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e.result = CheckResult{Provider: provider, State: "ok", ExternalVerified: external, CheckedAt: c.Now().UTC().Format(time.RFC3339), Attempts: n}
	if err != nil {
		e.result.State, e.result.Code, e.result.ExternalVerified = "failed", checkCode(err), false
		if errors.Is(err, ErrCheckStarting) {
			e.result.State = "starting"
		}
	}
	e.settled = true
	close(e.done)
	e.done = nil
}

func (c *Checker) attempt(provider string) (bool, error) {
	ctx, cancel := context.WithTimeout(c.ctx, c.Timeout)
	defer cancel()
	external, err := c.Probe(ctx, provider)
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		err = ErrCheckTimeout
	}
	return external, err
}

// backoff spaces settling retries: RetryDelay, doubled each time, at most
// six times RetryDelay (5, 10, 20, 30 s).
func (c *Checker) backoff(n int) time.Duration {
	d := c.RetryDelay
	for i := 1; i < n && d < 6*c.RetryDelay; i++ {
		d *= 2
	}
	return min(d, 6*c.RetryDelay)
}

func (c *Checker) wait(d time.Duration, stop chan struct{}) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-c.ctx.Done():
		return false
	case <-stop:
		return false
	}
}

func checkCode(err error) string {
	if code := ErrorCode(err); code != "" {
		return code
	}
	return ErrorCode(errCheckFailed)
}

// Checkable names the providers a connection check accepts.
func Checkable(provider string) bool {
	switch provider {
	case "cloudflare", "workers", "tailscale", "netbird", "ipv6":
		return true
	}
	return false
}

// StatusProbe checks from the daemon's own status: a provider passes only
// while its connection runs. It never reaches the NAS from outside.
func StatusProbe(status func(provider string) Status) Probe {
	return func(_ context.Context, provider string) (bool, error) {
		if status(provider).State != "connected" {
			return false, ErrNotConnected
		}
		return false, nil
	}
}

// safetyEvery paces the background safety checks.
var safetyEvery = 30 * time.Second

// RunSafetyChecks runs a local refusal-only check now and every 30 seconds
// until ctx ends, independently of provider probes, page visits and provider
// retry/rate limits. Calls never overlap. The owner runs it only while the
// checked feature is in use, so an unused feature keeps no timer.
func RunSafetyChecks(ctx context.Context, check func(context.Context) error, onError func(error)) {
	ticker := time.NewTicker(safetyEvery)
	defer ticker.Stop()
	// The same failure is reported once until a check passes or fails
	// differently; a lasting fault must not fill the log every 30 s.
	last := ""
	for {
		if ctx.Err() != nil {
			return
		}
		bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := check(bounded)
		cancel()
		if err == nil {
			last = ""
		} else if err.Error() != last {
			last = err.Error()
			if onError != nil {
				onError(err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
