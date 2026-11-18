package connector

import (
	"context"
	"dsmapp/internal/ipctest"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// lockedController holds one lock across Connect, like the provider selector,
// so a status read during a slow start would block without the async gate.
type lockedController struct {
	mu       sync.Mutex
	release  chan struct{}
	err      error
	state    string
	jobCtx   chan error
	stopped  bool
	disabled []string
}

func newLockedController() *lockedController {
	return &lockedController{release: make(chan struct{}), state: "disconnected", jobCtx: make(chan error, 1)}
}
func (c *lockedController) view() Status {
	return Status{Provider: "multiple", State: c.state, Connections: []Status{{Provider: "cloudflare", State: "disconnected"}, {Provider: "tailscale", State: c.state}}}
}
func (c *lockedController) Status() Status { c.mu.Lock(); defer c.mu.Unlock(); return c.view() }
func (c *lockedController) Connect(ctx context.Context, _ Request) (Status, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	select {
	case <-c.release:
		c.jobCtx <- ctx.Err()
	case <-ctx.Done():
		c.jobCtx <- ctx.Err()
		c.state = "failed"
		return c.view(), ctx.Err()
	}
	if c.err != nil {
		return c.view(), c.err
	}
	c.state = "connecting"
	return c.view(), nil
}
func (c *lockedController) Disconnect() (Status, error) { return c.DisconnectProvider("") }
func (c *lockedController) DisconnectProvider(id string) (Status, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.disabled = append(c.disabled, id)
	c.state = "disconnected"
	return c.view(), nil
}
func (c *lockedController) Stop() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped = true
	return nil
}

func quickAsync(c Controller) *Async {
	a := NewAsync(c)
	a.Accept = 30 * time.Millisecond
	return a
}
func stateOf(s Status, provider string) (string, string) {
	for _, c := range s.Connections {
		if c.Provider == provider {
			return c.State, c.Code
		}
	}
	return "", ""
}

func TestAsyncConnectAcceptsSlowStartDetachedFromCaller(t *testing.T) {
	c := newLockedController()
	a := quickAsync(c)
	ctx, cancel := context.WithCancel(context.Background())
	start := time.Now()
	go func() { time.Sleep(10 * time.Millisecond); cancel() }()
	s, e := a.Connect(ctx, Request{Provider: "tailscale"})
	if e != nil || time.Since(start) > time.Second {
		t.Fatal("slow start must be accepted promptly", e)
	}
	if state, _ := stateOf(s, "tailscale"); state != "connecting" {
		t.Fatal("accepted connect must report connecting", s)
	}
	close(c.release)
	if e = <-c.jobCtx; e != nil {
		t.Fatal("a canceled request must not cancel the detached start", e)
	}
	a.Stop()
}
func TestAsyncStatusDoesNotWaitForPendingConnect(t *testing.T) {
	c := newLockedController()
	a := quickAsync(c)
	if _, e := a.Connect(context.Background(), Request{Provider: "tailscale"}); e != nil {
		t.Fatal(e)
	}
	done := make(chan Status, 1)
	go func() { done <- a.Status() }()
	select {
	case s := <-done:
		if state, _ := stateOf(s, "tailscale"); state != "connecting" {
			t.Fatal("pending connect must read as connecting", s)
		}
		if state, _ := stateOf(s, "cloudflare"); state != "disconnected" {
			t.Fatal("other connections keep their observed state", s)
		}
	case <-time.After(time.Second):
		t.Fatal("status blocked behind a pending connect")
	}
	close(c.release)
	a.wait()
	if state, _ := stateOf(a.Status(), "tailscale"); state != "connecting" {
		t.Fatal("finished connect must read the provider again")
	}
}
func TestAsyncFastResultStaysSynchronous(t *testing.T) {
	c := newLockedController()
	c.err = ErrBinaryRequired
	close(c.release)
	a := NewAsync(c)
	if _, e := a.Connect(context.Background(), Request{Provider: "tailscale"}); !errors.Is(e, ErrBinaryRequired) {
		t.Fatal("a quick refusal must reach the caller", e)
	}
	if _, code := stateOf(a.Status(), "tailscale"); code != "" {
		t.Fatal("a refusal already returned must not linger", code)
	}
}
func TestAsyncLateFailureIsReportedByStatus(t *testing.T) {
	c := newLockedController()
	c.err = ErrBinaryRequired
	a := quickAsync(c)
	if _, e := a.Connect(context.Background(), Request{Provider: "tailscale"}); e != nil {
		t.Fatal(e)
	}
	close(c.release)
	<-c.jobCtx
	a.wait()
	if state, code := stateOf(a.Status(), "tailscale"); state != "failed" || code != "binary_required" {
		t.Fatal("late refusal must be visible to polling", state, code)
	}
	c.err = nil
	if _, e := a.Connect(context.Background(), Request{Provider: "tailscale"}); e != nil {
		t.Fatal(e)
	}
	if _, code := stateOf(a.Status(), "tailscale"); code != "" {
		t.Fatal("a new attempt clears the previous failure", code)
	}
	a.Stop()
}
func TestAsyncRefusesOverlappingMutations(t *testing.T) {
	c := newLockedController()
	a := quickAsync(c)
	if _, e := a.Connect(context.Background(), Request{Provider: "tailscale"}); e != nil {
		t.Fatal(e)
	}
	if _, e := a.Connect(context.Background(), Request{Provider: "netbird"}); !errors.Is(e, ErrConnectBusy) {
		t.Fatal("second connect must be refused while one is pending", e)
	}
	if _, e := a.DisconnectProvider("cloudflare"); !errors.Is(e, ErrConnectBusy) {
		t.Fatal("another provider must not wait behind a pending connect", e)
	}
	close(c.release)
	a.Stop()
}
func TestAsyncDisconnectCancelsPendingConnect(t *testing.T) {
	c := newLockedController()
	a := quickAsync(c)
	if _, e := a.Connect(context.Background(), Request{Provider: "tailscale"}); e != nil {
		t.Fatal(e)
	}
	s, e := a.DisconnectProvider("tailscale")
	if e != nil || <-c.jobCtx == nil {
		t.Fatal("disconnect must cancel the pending start", e)
	}
	if state, code := stateOf(s, "tailscale"); state != "disconnected" || code != "" || len(c.disabled) != 1 {
		t.Fatal("disconnect must win over the canceled start", s, c.disabled)
	}
}
func TestAsyncStopCancelsPendingConnectBeforeStopping(t *testing.T) {
	c := newLockedController()
	a := quickAsync(c)
	if _, e := a.Connect(context.Background(), Request{Provider: "tailscale"}); e != nil {
		t.Fatal(e)
	}
	if e := a.Stop(); e != nil || !c.stopped || <-c.jobCtx == nil {
		t.Fatal("stop must cancel the pending start", e)
	}
}
func TestConnectThroughControlSocketReturnsBeforeSlowStartFinishes(t *testing.T) {
	c := newLockedController()
	a := quickAsync(c)
	socket := ipctest.Path(t, filepath.Join(t.TempDir(), "control.sock"))
	listener, e := ipctest.Listen(t, socket)
	if e != nil {
		t.Fatal(e)
	}
	server := &http.Server{Handler: Handler(a, func() {})}
	go server.Serve(listener)
	defer server.Close()
	client := Client{Socket: socket}
	s, e := client.Call(context.Background(), "connect", Request{Provider: "tailscale"})
	if state, _ := stateOf(s, "tailscale"); e != nil || state != "connecting" {
		t.Fatal(s, e)
	}
	if _, e = client.Call(context.Background(), "connect", Request{Provider: "tailscale"}); !errors.Is(e, ErrConnectBusy) {
		t.Fatal("busy code must survive the control socket", e)
	}
	close(c.release)
	a.Stop()
}
func TestAsyncLateUncodedFailureStillReportsACode(t *testing.T) {
	c := newLockedController()
	c.err = errors.New("provider unavailable")
	a := quickAsync(c)
	if _, e := a.Connect(context.Background(), Request{Provider: "tailscale"}); e != nil {
		t.Fatal(e)
	}
	close(c.release)
	<-c.jobCtx
	a.wait()
	if state, code := stateOf(a.Status(), "tailscale"); state != "failed" || code != "start_failed" {
		t.Fatal("a late failure must never be reported without a code", state, code)
	}
	a.Stop()
}
func TestAsyncLogsConnectAndDisconnectOutcomesWithoutSecrets(t *testing.T) {
	var out syncBuffer
	SetLog(&out)
	defer SetLog(io.Discard)
	c := newLockedController()
	c.err = Coded("tailscale_start_failed", "tailscale did not start")
	a := quickAsync(c)
	if _, e := a.Connect(context.Background(), Request{Provider: "tailscale", Token: "tskey-auth-SYNTHETIC-SECRET"}); e != nil {
		t.Fatal(e)
	}
	close(c.release)
	<-c.jobCtx
	a.wait()
	if _, e := a.DisconnectProvider("tailscale"); e != nil {
		t.Fatal(e)
	}
	logged := out.String()
	for _, want := range []string{"connect tailscale accepted", "connect tailscale failed", "tailscale_start_failed", "disconnect tailscale ok"} {
		if !strings.Contains(logged, want) {
			t.Fatalf("daemon log misses %q:\n%s", want, logged)
		}
	}
	if strings.Contains(logged, "SECRET") {
		t.Fatal("daemon log must never contain credentials")
	}
	a.Stop()
}

type syncBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}
func (b *syncBuffer) String() string { b.mu.Lock(); defer b.mu.Unlock(); return b.buf.String() }

// slowStatusController reads status slowly, as a loaded NAS inspects each
// running provider, and starts slowly too.
type slowStatusController struct{ *lockedController }

func (c slowStatusController) Status() Status {
	time.Sleep(300 * time.Millisecond)
	return c.lockedController.Status()
}
func TestAsyncAcceptsWithinAcceptEvenWhenStatusIsSlow(t *testing.T) {
	c := newLockedController()
	a := quickAsync(slowStatusController{c})
	a.Status() // the UI polls status every 5 s
	started := time.Now()
	s, e := a.Connect(context.Background(), Request{Provider: "tailscale"})
	if e != nil {
		t.Fatal(e)
	}
	if took := time.Since(started); took > 200*time.Millisecond {
		t.Fatal("a slow status read must not delay the acceptance", took)
	}
	if state, _ := stateOf(s, "tailscale"); state != "connecting" {
		t.Fatal(state)
	}
	close(c.release)
	a.Stop()
}
func TestAsyncConnectCanceledByDisconnectIsLoggedAsCanceled(t *testing.T) {
	var out syncBuffer
	SetLog(&out)
	defer SetLog(io.Discard)
	c := newLockedController()
	a := quickAsync(c)
	if _, e := a.Connect(context.Background(), Request{Provider: "tailscale"}); e != nil {
		t.Fatal(e)
	}
	if _, e := a.DisconnectProvider("tailscale"); e != nil {
		t.Fatal(e)
	}
	logged := out.String()
	if !strings.Contains(logged, "connect tailscale canceled") || strings.Contains(logged, "connect tailscale failed") {
		t.Fatalf("a connect canceled by a disconnect must be logged as canceled:\n%s", logged)
	}
	a.Stop()
}
func TestAsyncConnectCanceledByStopIsLoggedAsCanceled(t *testing.T) {
	var out syncBuffer
	SetLog(&out)
	defer SetLog(io.Discard)
	c := newLockedController()
	a := quickAsync(c)
	if _, e := a.Connect(context.Background(), Request{Provider: "tailscale"}); e != nil {
		t.Fatal(e)
	}
	a.Stop()
	if logged := out.String(); !strings.Contains(logged, "connect tailscale canceled") {
		t.Fatalf("a connect canceled by shutdown must be logged as canceled:\n%s", logged)
	}
}

// slowStopController stops slowly, as a loaded NAS stops a daemon in 13 s.
type slowStopController struct {
	*lockedController
	stopRelease chan struct{}
	stopErr     error
}

func newSlowStopController() *slowStopController {
	c := &slowStopController{lockedController: newLockedController(), stopRelease: make(chan struct{})}
	c.state = "connected"
	return c
}
func (c *slowStopController) Disconnect() (Status, error) { return c.DisconnectProvider("") }
func (c *slowStopController) DisconnectProvider(id string) (Status, error) {
	<-c.stopRelease
	if c.stopErr != nil {
		return c.Status(), c.stopErr
	}
	return c.lockedController.DisconnectProvider(id)
}
func TestAsyncDisconnectAcceptsSlowStopAndStatusReportsDisconnecting(t *testing.T) {
	c := newSlowStopController()
	a := quickAsync(c)
	started := time.Now()
	s, e := a.DisconnectProvider("tailscale")
	if e != nil || time.Since(started) > time.Second {
		t.Fatal("a slow stop must be accepted promptly", e)
	}
	if state, _ := stateOf(s, "tailscale"); state != "disconnecting" {
		t.Fatal("an accepted disconnect must report disconnecting", s)
	}
	if state, _ := stateOf(a.Status(), "tailscale"); state != "disconnecting" {
		t.Fatal("status must report the pending disconnect without waiting")
	}
	close(c.stopRelease)
	a.wait()
	if state, code := stateOf(a.Status(), "tailscale"); state != "disconnected" || code != "" {
		t.Fatal("a finished disconnect must read the provider again", state, code)
	}
	a.Stop()
}
func TestAsyncLateStopFailureIsReportedByStatus(t *testing.T) {
	c := newSlowStopController()
	c.stopErr = Coded("tailscale_stop_failed", "tailscale did not stop")
	a := quickAsync(c)
	if _, e := a.DisconnectProvider("tailscale"); e != nil {
		t.Fatal(e)
	}
	close(c.stopRelease)
	a.wait()
	if state, code := stateOf(a.Status(), "tailscale"); state != "failed" || code != "tailscale_stop_failed" {
		t.Fatal("a late stop failure must be visible to polling", state, code)
	}
	a.Stop()
}
func TestAsyncPendingDisconnectRefusesConnectAndJoinsRepeats(t *testing.T) {
	c := newSlowStopController()
	a := quickAsync(c)
	if _, e := a.Disconnect(); e != nil {
		t.Fatal(e)
	}
	if _, e := a.Connect(context.Background(), Request{Provider: "tailscale"}); !errors.Is(e, ErrDisconnectBusy) {
		t.Fatal("connect must wait for the pending disconnect", e)
	}
	s, e := a.DisconnectProvider("tailscale")
	if state, _ := stateOf(s, "tailscale"); e != nil || state != "disconnecting" {
		t.Fatal("a repeated disconnect joins the pending one", s, e)
	}
	close(c.stopRelease)
	a.wait()
	if len(c.disabled) != 1 {
		t.Fatal("a joined disconnect must stop once", c.disabled)
	}
	a.Stop()
}
func TestAsyncDisconnectDoesNotWaitForACanceledConnectToUnwind(t *testing.T) {
	c := newSlowStopController()
	a := quickAsync(c)
	if _, e := a.Connect(context.Background(), Request{Provider: "tailscale"}); e != nil {
		t.Fatal(e)
	}
	started := time.Now()
	if _, e := a.DisconnectProvider("tailscale"); e != nil || time.Since(started) > time.Second {
		t.Fatal("disconnect must be accepted while the canceled start unwinds", e)
	}
	close(c.stopRelease)
	a.Stop()
	if len(c.disabled) != 1 {
		t.Fatal("the disconnect must still run after the canceled start", c.disabled)
	}
}
func TestDisconnectThroughControlSocketReturnsBeforeSlowStopFinishes(t *testing.T) {
	c := newSlowStopController()
	a := quickAsync(c)
	socket := ipctest.Path(t, filepath.Join(t.TempDir(), "control.sock"))
	listener, e := ipctest.Listen(t, socket)
	if e != nil {
		t.Fatal(e)
	}
	server := &http.Server{Handler: Handler(a, func() {})}
	go server.Serve(listener)
	defer server.Close()
	client := Client{Socket: socket}
	if _, e = client.Call(context.Background(), "disconnect", map[string]string{"provider": "tailscale"}); e != nil {
		t.Fatal(e)
	}
	if _, e = client.Call(context.Background(), "connect", Request{Provider: "tailscale"}); !errors.Is(e, ErrDisconnectBusy) {
		t.Fatal("disconnect_busy must survive the control socket", e)
	}
	close(c.stopRelease)
	a.Stop()
}

type forgettingController struct {
	*lockedController
	forgot []string
}

func (c *forgettingController) Forget(id string) (Status, error) {
	c.forgot = append(c.forgot, id)
	return c.Status(), nil
}

func TestAsyncForgetWaitsForNoPendingMutation(t *testing.T) {
	c := &forgettingController{lockedController: newLockedController()}
	a := quickAsync(c)
	if _, e := a.Connect(context.Background(), Request{Provider: "tailscale"}); e != nil {
		t.Fatal(e)
	}
	if _, e := a.Forget("ipv6"); !errors.Is(e, ErrConnectBusy) || len(c.forgot) != 0 {
		t.Fatal("forget must not run beside a pending connect", e, c.forgot)
	}
	close(c.release)
	a.wait()
	if _, e := a.Forget("ipv6"); e != nil || len(c.forgot) != 1 || c.forgot[0] != "ipv6" {
		t.Fatal(e, c.forgot)
	}
	if _, e := quickAsync(newLockedController()).Forget("ipv6"); e == nil {
		t.Fatal("a controller that cannot forget must refuse")
	}
	a.Stop()
}
