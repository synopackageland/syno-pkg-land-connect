package connector

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// settlingChecker anchors its boot window to the fake clock; each probe
// advances that clock by step, as real retries would.
func settlingChecker(t *testing.T, step time.Duration, fails int) (*Checker, *fakeClock, *atomic.Int32) {
	clock := &fakeClock{now: time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)}
	var calls atomic.Int32
	c := NewChecker(func(context.Context, string) (bool, error) {
		n := int(calls.Add(1))
		clock.Advance(step)
		if n <= fails {
			return false, errProbe
		}
		return false, nil
	})
	c.Now = clock.Now
	c.Begin(clock.Now())
	c.Timeout, c.RetryDelay, c.Accept = time.Second, time.Millisecond, 2*time.Second
	t.Cleanup(c.Close)
	return c, clock, &calls
}

func TestSettleWindowIsLongerForDNSBasedWays(t *testing.T) {
	for provider, want := range map[string]time.Duration{"cloudflare": 3 * time.Minute, "workers": 3 * time.Minute, "tailscale": time.Minute, "netbird": time.Minute, "ipv6": time.Minute} {
		if got := SettleWindow(provider); got != want {
			t.Errorf("%s: %v, want %v", provider, got, want)
		}
	}
	if DNSSettle != 3*time.Minute {
		t.Fatalf("DNSSettle = %v", DNSSettle)
	}
}

func TestSettlingRetriesUntilACheckPasses(t *testing.T) {
	// Five failures 10 s apart stay inside Tailscale's minute.
	c, _, calls := settlingChecker(t, 10*time.Second, 5)
	if got := c.Check(context.Background(), "tailscale"); got.State != "ok" || got.Attempts != 6 || calls.Load() != 6 {
		t.Fatalf("got %+v after %d probes, want ok on the sixth attempt", got, calls.Load())
	}
}

func TestAFailureWhileSettlingReadsStarting(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)}
	release := make(chan struct{})
	var calls atomic.Int32
	c := NewChecker(func(context.Context, string) (bool, error) {
		if calls.Add(1) > 1 {
			<-release
			return false, nil
		}
		return false, errProbe
	})
	defer c.Close()
	c.Now = clock.Now
	c.Begin(clock.Now())
	c.Timeout, c.RetryDelay, c.Accept = time.Second, time.Millisecond, 50*time.Millisecond
	if got := c.Check(context.Background(), "cloudflare"); got.State != "starting" || got.Code != "not_connected" || got.ExternalVerified {
		t.Fatalf("got %+v, want starting while the address settles", got)
	}
	close(release)
	c.Accept = time.Second
	if got := c.Check(context.Background(), "cloudflare"); got.State != "ok" {
		t.Fatalf("got %+v, want ok once a check passes", got)
	}
}

func TestAFailureAfterTheWindowReportsTheRealCode(t *testing.T) {
	// Failures 20 s apart: at 0, 20, 40 s the minute still runs; at 60 s it ended.
	c, _, calls := settlingChecker(t, 20*time.Second, 100)
	if got := c.Check(context.Background(), "tailscale"); got.State != "failed" || got.Code != "not_connected" || got.Attempts != 3 {
		t.Fatalf("got %+v after %d probes, want failed when the minute ends", got, calls.Load())
	}
}

func TestDNSBasedWaysSettleForThreeMinutes(t *testing.T) {
	// Eight failures 20 s apart need 160 s: Cloudflare waits, Tailscale does not.
	c, _, _ := settlingChecker(t, 20*time.Second, 8)
	if got := c.Check(context.Background(), "cloudflare"); got.State != "ok" {
		t.Fatalf("cloudflare: %+v, want ok within three minutes", got)
	}
	ts, _, _ := settlingChecker(t, 20*time.Second, 8)
	if got := ts.Check(context.Background(), "tailscale"); got.State != "failed" {
		t.Fatalf("tailscale: %+v, want failed after its minute", got)
	}
}

func TestABootWindowEndsAndALaterFirstCheckFailsAsBefore(t *testing.T) {
	c, clock, calls := settlingChecker(t, 0, 100)
	clock.Advance(2 * time.Minute)
	if got := c.Check(context.Background(), "tailscale"); got.State != "failed" || got.Attempts != c.Retries || int(calls.Load()) != c.Retries {
		t.Fatalf("got %+v after %d probes, want the plain retries once the boot window ended", got, calls.Load())
	}
}

func TestReconnectRestartsTheWindow(t *testing.T) {
	c, clock, calls := settlingChecker(t, 10*time.Second, 100)
	clock.Advance(10 * time.Minute)
	c.Check(context.Background(), "tailscale")
	before := calls.Load()
	c.Reset("tailscale")
	// Fail five more times 10 s apart, then pass: only a new window allows it.
	limit := before + 5
	c.Probe = func(context.Context, string) (bool, error) {
		clock.Advance(10 * time.Second)
		if calls.Add(1) <= limit {
			return false, errProbe
		}
		return false, nil
	}
	if got := c.Check(context.Background(), "tailscale"); got.State != "ok" || got.Attempts != 6 {
		t.Fatalf("got %+v, want ok within the new window", got)
	}
}

func TestResetStopsTheOldSettlingRun(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC)}
	var calls atomic.Int32
	c := NewChecker(func(context.Context, string) (bool, error) { calls.Add(1); return false, errProbe })
	defer c.Close()
	c.Now = clock.Now
	c.Begin(clock.Now())
	c.Timeout, c.RetryDelay, c.Accept = time.Second, time.Millisecond, 10*time.Millisecond
	c.Check(context.Background(), "netbird")
	c.Reset("netbird")
	time.Sleep(30 * time.Millisecond)
	n := calls.Load()
	time.Sleep(30 * time.Millisecond)
	if calls.Load() != n {
		t.Fatalf("the old run kept probing after a reset: %d then %d", n, calls.Load())
	}
}

func TestSettlingRetriesBackOff(t *testing.T) {
	c := NewChecker(nil)
	defer c.Close()
	c.RetryDelay = 5 * time.Second
	want := []time.Duration{5, 10, 20, 30, 30}
	for i, w := range want {
		if got := c.backoff(i + 1); got != w*time.Second {
			t.Errorf("retry %d waits %v, want %v", i+1, got, w*time.Second)
		}
	}
}

// A Free link address that is new (turned on, renamed) settles like a new own
// domain: after Reset its failing check reads starting and keeps retrying,
// then works; only past the window does a failure read failed.
func TestANewFreeLinkAddressSettlesBeforeItFails(t *testing.T) {
	c, clock, calls := settlingChecker(t, 20*time.Second, 4)
	clock.Advance(time.Hour) // long after boot
	c.Reset("workers")
	if got := c.Check(context.Background(), "workers"); got.State != "ok" || calls.Load() != 5 {
		t.Fatalf("got %+v after %d probes, want ok on the fifth attempt", got, calls.Load())
	}
	// Without the reset, the same four failures long after boot read failed.
	plain, clock3, _ := settlingChecker(t, 20*time.Second, 4)
	clock3.Advance(time.Hour)
	if got := plain.Check(context.Background(), "workers"); got.State != "failed" {
		t.Fatalf("got %+v without a reset, want failed", got)
	}
	failing, clock2, _ := settlingChecker(t, time.Minute, 1000)
	clock2.Advance(time.Hour)
	failing.Reset("workers")
	if got := failing.Check(context.Background(), "workers"); got.State != "failed" {
		t.Fatalf("got %+v past the window, want failed", got)
	}
}
