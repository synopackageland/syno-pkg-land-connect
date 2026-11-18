package nbserver

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"dsmapp/internal/connector"
	"dsmapp/internal/dependency"
)

func TestSetupKeepsVerifiedZoneWhileItsRouteRestartsTheTunnel(t *testing.T) {
	h := newHarness(t)
	var restarting atomic.Bool
	h.s.d.Zone = func() (string, error) {
		if restarting.Load() {
			return "", connector.Coded("cloudflare_not_connected", "tunnel reconnecting after route update")
		}
		return "example.com", nil
	}
	route := h.s.d.Route
	h.s.d.Route = func(ctx context.Context, host string) error {
		if e := route(ctx, host); e != nil {
			return e
		}
		restarting.Store(true)
		return nil
	}
	if e := h.s.Setup(); e != nil {
		t.Fatal(e)
	}
	st := h.wait("running")
	if !st.Joined {
		t.Fatal("setup did not finish", st)
	}
}

func TestFailedSetupReleasesJobAndRetryCanFinish(t *testing.T) {
	h := newHarness(t)
	var fail atomic.Bool
	fail.Store(true)
	h.s.d.Install = func(context.Context) error {
		if fail.Load() {
			return errors.New("download failed")
		}
		return nil
	}
	if e := h.s.Setup(); e != nil {
		t.Fatal(e)
	}
	h.wait("failed")
	deadline := time.Now().Add(time.Second)
	for {
		h.s.smu.Lock()
		released := h.s.jobCancel == nil
		h.s.smu.Unlock()
		if released {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("failed job still owns setup")
		}
		time.Sleep(time.Millisecond)
	}
	fail.Store(false)
	if e := h.s.Setup(); e != nil {
		t.Fatal(e)
	}
	h.wait("running")
}

func TestPanickedSetupReleasesJobAndRetryCanFinish(t *testing.T) {
	h := newHarness(t)
	h.s.d.Install = func(context.Context) error { panic("synthetic worker failure") }
	if e := h.s.Setup(); e != nil {
		t.Fatal(e)
	}
	h.wait("failed")
	h.s.smu.Lock()
	done := h.s.jobDone
	h.s.smu.Unlock()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("panic kept job ownership")
	}
	h.s.d.Install = func(context.Context) error { return nil }
	if e := h.s.Setup(); e != nil {
		t.Fatal(e)
	}
	h.wait("running")
}

func TestRestartHasNoStaleSetupLockAndOffersResume(t *testing.T) {
	h := newHarness(t)
	started := make(chan struct{})
	h.s.d.Bridge = func(ctx context.Context, _, _ string) error { close(started); <-ctx.Done(); return ctx.Err() }
	if e := h.s.Setup(); e != nil {
		t.Fatal(e)
	}
	<-started
	h.s.Close()
	h.s = h.server()
	t.Cleanup(h.s.Close)
	st := h.s.Status(context.Background())
	if st.Job != nil || !st.ResumeRequired {
		t.Fatal("restart lacks a recoverable setup state", st)
	}
	if e := h.s.Setup(); e != nil {
		t.Fatal(e)
	}
	h.wait("running")
}

func TestAnOwnedDependencyRecoveryDoesNotHideANameConflict(t *testing.T) {
	h := newHarness(t)
	h.s.d.Dependencies = func(context.Context, string, bool) dependency.Report {
		return dependency.Report{Enabled: true, UpstreamsReady: true}
	}
	h.bridge = connector.Coded("workers_name_conflict", "name belongs to someone else")
	if e := h.s.Setup(); e != nil {
		t.Fatal(e)
	}
	if st := h.wait("failed"); st.Code != "workers_name_conflict" {
		t.Fatal("name conflict was hidden by healthy tunnel", st.Code)
	}
}

func TestSetupReturnsImmediatelyWhilePrerequisiteCheckIsInProgress(t *testing.T) {
	h := newHarness(t)
	entered, release := make(chan struct{}), make(chan struct{})
	h.s.d.Preflight = func(ctx context.Context) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	returned := make(chan error, 1)
	go func() { returned <- h.s.Setup() }()
	<-entered
	select {
	case e := <-returned:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(100 * time.Millisecond):
		close(release)
		t.Fatal("setup RPC waits for remote prerequisite")
	}
	h.s.smu.Lock()
	active := h.s.jobCancel != nil
	h.s.smu.Unlock()
	close(release)
	if !active {
		t.Fatal("no observable job owns prerequisite check")
	}
	h.wait("running")
}

func TestReadOnlyDependencyPassDoesNotHideWorkerWritePermissionFailure(t *testing.T) {
	h := newHarness(t)
	h.s.d.Dependencies = func(context.Context, string, bool) dependency.Report {
		return dependency.Report{Enabled: true, UpstreamsReady: true}
	}
	h.bridge = connector.Coded("workers_permission_denied", "Worker upload refused")
	if e := h.s.Setup(); e != nil {
		t.Fatal(e)
	}
	if st := h.wait("failed"); st.Code != "workers_permission_denied" {
		t.Fatal("write failure hidden", st.Code)
	}
}

func TestServerDNSGuideDoesNotOfferAnUnsupportedWorkerRename(t *testing.T) {
	h := newHarness(t)
	h.s.d.Route = func(context.Context, string) error {
		return connector.WithNameConflict(connector.Coded("cloudflare_name_taken", "name is used"), connector.NameConflict{ObjectType: "dns", ConflictName: "netbird.example.com", SuggestedName: "netbird-2.example.com"})
	}
	if e := h.s.Setup(); e != nil {
		t.Fatal(e)
	}
	st := h.wait("failed")
	for _, step := range st.Steps {
		if step.ID == StepRoute {
			if step.Conflict == nil || step.Conflict.SuggestedName != "" {
				t.Fatal("Worker-only rename offered for server DNS", step.Conflict)
			}
			return
		}
	}
	t.Fatal("missing route guide")
}
