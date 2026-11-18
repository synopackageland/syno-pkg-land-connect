package connector

import (
	"context"
	"dsmapp/internal/ipctest"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeClock is a settable clock for the rate limit; retries and timeouts use
// real, short durations.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// scriptedProbe answers each attempt from a list of errors; past the end it succeeds.
type scriptedProbe struct {
	calls atomic.Int32
	errs  []error
}

func (p *scriptedProbe) probe(context.Context, string) (bool, error) {
	n := int(p.calls.Add(1)) - 1
	if n < len(p.errs) {
		return false, p.errs[n]
	}
	return false, nil
}

func testChecker(probe Probe) (*Checker, *fakeClock) {
	clock := &fakeClock{now: time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)}
	c := NewChecker(probe)
	c.Now = clock.Now
	c.Timeout = time.Second
	c.RetryDelay = 10 * time.Millisecond
	c.Accept = 2 * time.Second
	// The retry tests below predate the settling window; settle_test.go
	// covers it with its own clock.
	c.Settle = func(string) time.Duration { return 0 }
	c.Begin(clock.Now())
	return c, clock
}

var errProbe = Coded("not_connected", "the connection is not running")

func TestCheckServesTheCachedResultWithinTheRateLimit(t *testing.T) {
	p := &scriptedProbe{}
	c, clock := testChecker(p.probe)
	defer c.Close()
	first := c.Check(context.Background(), "tailscale")
	if first.State != "ok" || first.Provider != "tailscale" || first.CheckedAt != "2026-10-12T08:00:00Z" {
		t.Fatalf("first = %+v", first)
	}
	clock.Advance(29 * time.Second)
	if again := c.Check(context.Background(), "tailscale"); again != first {
		t.Fatalf("within 30 s a check reran: %+v", again)
	}
	if p.calls.Load() != 1 {
		t.Fatalf("probe ran %d times, want 1", p.calls.Load())
	}
	clock.Advance(2 * time.Second)
	if later := c.Check(context.Background(), "tailscale"); later.CheckedAt != "2026-10-12T08:00:31Z" || p.calls.Load() != 2 {
		t.Fatalf("after 30 s the check did not rerun: %+v, %d probes", later, p.calls.Load())
	}
}

func TestCheckLimitsEachProviderSeparately(t *testing.T) {
	p := &scriptedProbe{}
	c, _ := testChecker(p.probe)
	defer c.Close()
	c.Check(context.Background(), "tailscale")
	c.Check(context.Background(), "netbird")
	if p.calls.Load() != 2 {
		t.Fatalf("probe ran %d times, want one per provider", p.calls.Load())
	}
}

func TestCheckTimesOutASlowProbe(t *testing.T) {
	c, _ := testChecker(func(ctx context.Context, _ string) (bool, error) {
		<-ctx.Done()
		return false, ctx.Err()
	})
	defer c.Close()
	c.Timeout = 20 * time.Millisecond
	c.Retries = 1
	if got := c.Check(context.Background(), "ipv6"); got.State != "failed" || got.Code != "check_timeout" {
		t.Fatalf("got %+v, want failed check_timeout", got)
	}
}

func TestAFreshConnectionRetriesBeforeItFails(t *testing.T) {
	p := &scriptedProbe{errs: []error{errProbe, errProbe}}
	c, _ := testChecker(p.probe)
	defer c.Close()
	start := time.Now()
	got := c.Check(context.Background(), "cloudflare")
	if got.State != "ok" || got.Attempts != 3 || p.calls.Load() != 3 {
		t.Fatalf("got %+v after %d probes, want ok on the third attempt", got, p.calls.Load())
	}
	if elapsed := time.Since(start); elapsed < 20*time.Millisecond {
		t.Fatalf("attempts were not spaced by the retry delay: %v", elapsed)
	}
}

func TestAFreshConnectionFailsAfterThreeAttempts(t *testing.T) {
	p := &scriptedProbe{errs: []error{errProbe, errProbe, errProbe, errProbe}}
	c, _ := testChecker(p.probe)
	defer c.Close()
	got := c.Check(context.Background(), "cloudflare")
	if got.State != "failed" || got.Code != "not_connected" || got.Attempts != 3 || p.calls.Load() != 3 {
		t.Fatalf("got %+v after %d probes", got, p.calls.Load())
	}
}

func TestALaterCheckTriesOnce(t *testing.T) {
	p := &scriptedProbe{}
	c, clock := testChecker(p.probe)
	defer c.Close()
	c.Check(context.Background(), "tailscale")
	p.errs = []error{nil, errProbe, errProbe, errProbe}
	clock.Advance(time.Minute)
	if got := c.Check(context.Background(), "tailscale"); got.State != "failed" || got.Attempts != 1 || p.calls.Load() != 2 {
		t.Fatalf("got %+v after %d probes, want one failed attempt", got, p.calls.Load())
	}
}

func TestResetStartsAFreshConnection(t *testing.T) {
	p := &scriptedProbe{}
	c, _ := testChecker(p.probe)
	defer c.Close()
	c.Check(context.Background(), "tailscale")
	c.Reset("tailscale")
	p.errs = []error{nil, errProbe, errProbe}
	if got := c.Check(context.Background(), "tailscale"); got.State != "ok" || got.Attempts != 3 || p.calls.Load() != 4 {
		t.Fatalf("got %+v after %d probes: a reset should rerun at once and retry", got, p.calls.Load())
	}
}

func TestACheckStillRunningReadsAsCheckingAndIsNotStartedTwice(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	c, _ := testChecker(func(ctx context.Context, _ string) (bool, error) {
		calls.Add(1)
		<-release
		return true, nil
	})
	defer c.Close()
	c.Accept = 10 * time.Millisecond
	if got := c.Check(context.Background(), "cloudflare"); got.State != "checking" || got.CheckedAt != "" {
		t.Fatalf("got %+v, want checking", got)
	}
	if got := c.Check(context.Background(), "cloudflare"); got.State != "checking" || calls.Load() != 1 {
		t.Fatalf("got %+v with %d probes, want the running check", got, calls.Load())
	}
	close(release)
	c.Accept = time.Second
	if got := c.Check(context.Background(), "cloudflare"); got.State != "ok" || !got.ExternalVerified {
		t.Fatalf("got %+v, want the finished external result", got)
	}
}

func TestBeforeAnyCheckTheResultIsNotChecked(t *testing.T) {
	c, _ := testChecker((&scriptedProbe{}).probe)
	defer c.Close()
	if got := c.Last("netbird"); got.State != "not-checked" || got.Provider != "netbird" {
		t.Fatalf("got %+v", got)
	}
}

func TestStatusProbeAcceptsOnlyAConnectedProvider(t *testing.T) {
	probe := StatusProbe(func(id string) Status {
		if id == "tailscale" {
			return Status{Provider: id, State: "connected"}
		}
		return Status{Provider: id, State: "needs-login"}
	})
	if external, e := probe(context.Background(), "tailscale"); e != nil || external {
		t.Fatalf("connected: %v %v", external, e)
	}
	if _, e := probe(context.Background(), "netbird"); !errors.Is(e, errProbe) {
		t.Fatalf("not connected: %v", e)
	}
}

type checkingController struct {
	*lockedController
	checker *Checker
}

func (c *checkingController) Check(ctx context.Context, provider string) (CheckResult, error) {
	return c.checker.Check(ctx, provider), nil
}

func TestClientCheckCrossesTheControlSocket(t *testing.T) {
	c, _ := testChecker((&scriptedProbe{}).probe)
	defer c.Close()
	socket := ipctest.Path(t, filepath.Join(t.TempDir(), "control.sock"))
	ln, e := ipctest.Listen(t, socket)
	if e != nil {
		t.Fatal(e)
	}
	srv := &http.Server{Handler: Handler(&checkingController{newLockedController(), c}, func() {})}
	go srv.Serve(ln)
	defer srv.Close()
	got, e := Client{Socket: socket}.Check(context.Background(), "tailscale")
	if e != nil || got.State != "ok" || got.Provider != "tailscale" {
		t.Fatalf("got %+v %v", got, e)
	}
	if _, e := (Client{Socket: socket}).Check(context.Background(), "fake"); e == nil {
		t.Fatal("an unknown provider was checked")
	}
}

func TestSafetyChecksRunWithoutProviderRequestsAndCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	stopped := make(chan struct{})
	go RunSafetyChecks(ctx, func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		close(stopped)
		return ctx.Err()
	}, nil)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("safety check waits for a provider request")
	}
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("safety check outlived its context")
	}
}

func TestALastingSafetyFailureIsReportedOnce(t *testing.T) {
	defer func(d time.Duration) { safetyEvery = d }(safetyEvery)
	safetyEvery = time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	runs, reports := 0, []string{}
	results := []error{errors.New("corrupt"), errors.New("corrupt"), errors.New("corrupt"), nil, errors.New("corrupt"), errors.New("other")}
	done := make(chan struct{})
	go RunSafetyChecks(ctx, func(context.Context) error {
		mu.Lock()
		defer mu.Unlock()
		if runs == len(results) {
			close(done)
		}
		if runs >= len(results) {
			runs++
			return nil
		}
		runs++
		return results[runs-1]
	}, func(e error) { mu.Lock(); reports = append(reports, e.Error()); mu.Unlock() })
	<-done
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(reports, ",") != "corrupt,corrupt,other" {
		t.Fatal("each failure streak is reported once", reports)
	}
}

func TestStartingCheckKeepsNormalCadenceAndCanRecoverOrFail(t *testing.T) {
	for _, ending := range []error{nil, errProbe} {
		t.Run(checkCode(ending), func(t *testing.T) {
			calls := 0
			c, clock := testChecker(func(context.Context, string) (bool, error) {
				calls++
				if calls == 1 {
					return false, ErrCheckStarting
				}
				return ending == nil, ending
			})
			defer c.Close()
			c.Retries = 1
			if got := c.Check(context.Background(), "cloudflare"); got.State != "starting" || got.ExternalVerified || got.CheckedAt == "" {
				t.Fatalf("starting: %+v", got)
			}
			clock.Advance(c.Interval - time.Second)
			if got := c.Check(context.Background(), "cloudflare"); got.State != "starting" || calls != 1 {
				t.Fatalf("cached: %+v, calls %d", got, calls)
			}
			clock.Advance(time.Second)
			want := "ok"
			if ending != nil {
				want = "failed"
			}
			if got := c.Check(context.Background(), "cloudflare"); got.State != want || calls != 2 {
				t.Fatalf("next: %+v, calls %d", got, calls)
			}
		})
	}
}
