package cloudflare

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"dsmapp/internal/connector"
	"dsmapp/internal/supervise"
)

// slowBoot restores a saved own domain while cloudflared or DSM's port is
// not available yet, as on a NAS whose volume is still mounting.
func slowBoot(t *testing.T, missing int32) (*fixture, *Tunnel, *atomic.Int32) {
	t.Helper()
	f := newFixture(t)
	close(f.signIn(goodCert()))
	f.cloudflaredRuns("")
	if e := f.tunnel.Domain(context.Background(), "nas"); e != nil {
		t.Fatal(e)
	}
	f.waitState("connected")
	f.tunnel.Stop()
	var left atomic.Int32
	left.Store(missing)
	cfg := f.tunnel.cfg
	cfg.RestartDelay = time.Millisecond
	cfg.Cloudflared = func(context.Context) (string, error) {
		if left.Add(-1) >= 0 {
			return "", errors.New("volume not mounted yet")
		}
		return "/opt/cloudflared", nil
	}
	restarted := NewTunnel(cfg)
	t.Cleanup(func() { restarted.Stop() })
	if e := restarted.Restore(context.Background()); e != nil {
		t.Fatal("a cloudflared not available yet must not fail the restore", e)
	}
	return f, restarted, &left
}

func TestMissingBinaryFailsWithoutTimersAndExplicitRestoreRearms(t *testing.T) {
	_, tunnel, left := slowBoot(t, 1<<20)
	deadline := time.Now().Add(time.Second)
	for tunnel.Status().State != "failed" {
		if time.Now().After(deadline) {
			t.Fatal("missing binary did not fail", tunnel.Status())
		}
		time.Sleep(time.Millisecond)
	}
	before := left.Load()
	time.Sleep(20 * time.Millisecond)
	if left.Load() != before {
		t.Fatal("missing binary retried")
	}
	left.Store(0)
	if err := tunnel.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	for tunnel.Status().State != "connected" {
		if time.Now().After(deadline) {
			t.Fatal("explicit retry did not restore", tunnel.Status())
		}
		time.Sleep(time.Millisecond)
	}
}

func TestTurningOffEndsARestoreRetry(t *testing.T) {
	_, tunnel, left := slowBoot(t, 1<<30)
	time.Sleep(10 * time.Millisecond)
	if e := tunnel.Disable(); e != nil {
		t.Fatal(e)
	}
	before := left.Load()
	time.Sleep(30 * time.Millisecond)
	if left.Load() != before {
		t.Fatal("a turned-off tunnel kept looking for cloudflared")
	}
	if s := tunnel.Status(); s.State != "disconnected" {
		t.Fatal("a turned-off restore still reads as active", s.State)
	}
}

// Health names its step and attempt like every provider, and waits on what
// a busy NAS cannot answer in time instead of counting it toward a repair.
func TestCloudflareHealthWaitsOnASlowCheck(t *testing.T) {
	f := newFixture(t)
	close(f.signIn(goodCert()))
	f.cloudflaredRuns("")
	if e := f.tunnel.Domain(context.Background(), "nas"); e != nil {
		t.Fatal(e)
	}
	f.waitState("connected")
	var step *supervise.StepError
	f.tunnel.cfg.CheckWeb = func(context.Context, string) error { return context.DeadlineExceeded }
	if e := f.tunnel.CheckHealth(context.Background()); !errors.Is(e, supervise.ErrWaiting) || !errors.As(e, &step) || step.Attempt == 0 {
		t.Fatal("a check that timed out must wait at a named step", e)
	}
	refused := connector.Coded("apply_cloudflare_authorization_required", "sign in again")
	f.tunnel.cfg.CheckWeb = func(context.Context, string) error { return refused }
	if e := f.tunnel.CheckHealth(context.Background()); errors.Is(e, supervise.ErrWaiting) || connector.ErrorCode(e) != "apply_cloudflare_authorization_required" {
		t.Fatal("a failure Cloudflare reported must count", e)
	}
	f.tunnel.cfg.CheckWeb = nil
	f.tunnel.set(f.tunnel.gen, func(s *connector.Status) { s.State = "connecting" })
	if e := f.tunnel.CheckHealth(context.Background()); !errors.Is(e, supervise.ErrWaiting) {
		t.Fatal("a cloudflared still connecting must be waited on", e)
	}
}

func TestProbeFailuresAreClassifiedLikeEveryProvider(t *testing.T) {
	for _, c := range []struct {
		err     error
		waiting bool
	}{
		{context.DeadlineExceeded, true},
		{errAddressUnreached, true},
		{errOriginDown, true},
		{errTLS, false},
	} {
		if e := probeHealth("web", 1, c.err); errors.Is(e, supervise.ErrWaiting) != c.waiting {
			t.Fatal("misclassified", c.err, e)
		}
	}
}

func TestDNSFailureNeverClaimsSignInIsInvalid(t *testing.T) {
	f := newFixture(t)
	close(f.signIn(goodCert()))
	f.cloudflaredRuns("")
	if err := f.tunnel.Domain(context.Background(), "nas"); err != nil {
		t.Fatal(err)
	}
	f.waitState("connected")
	f.tunnel.cfg.CheckWeb = func(context.Context, string) error { return errDNSPending }
	err := f.tunnel.CheckHealth(context.Background())
	if !errors.Is(err, supervise.ErrWaiting) || connector.ErrorCode(err) != "cloudflare_dns_pending" {
		t.Fatalf("DNS failure became a sign-in/repair failure: %v", err)
	}
	f.certKept()
}
