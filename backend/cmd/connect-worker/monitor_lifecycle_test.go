package main

import (
	"context"
	"dsmapp/internal/connector"
	"dsmapp/internal/control"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestRestoreCompletesBeforePortMonitorAndShutdownWaitsForWithdrawal(t *testing.T) {
	restoring, release, monitoring, withdrawn := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	ready := make(chan func(), 1)
	monitor := &connector.Demand{Active: func() bool { return true }, Loop: func(ctx context.Context) {
		close(monitoring)
		<-ctx.Done()
		close(withdrawn)
	}}
	go func() {
		ready <- restoreAndMonitor(context.Background(), func(context.Context) error {
			close(restoring)
			<-release
			return nil
		}, monitor)
	}()
	<-restoring
	select {
	case <-monitoring:
		t.Fatal("monitor raced connection restore")
	default:
	}
	close(release)
	stop := <-ready
	<-monitoring
	stop()
	select {
	case <-withdrawn:
	default:
		t.Fatal("shutdown returned before mappings were withdrawn")
	}
}

// With nothing restored as enabled, no monitor loop starts at all.
func TestNoMonitorRunsWhenNothingIsEnabled(t *testing.T) {
	started := false
	monitor := &connector.Demand{Active: func() bool { return false }, Loop: func(context.Context) { started = true }}
	stop := restoreAndMonitor(context.Background(), func(context.Context) error { return nil }, monitor)
	stop()
	if started || monitor.Running() {
		t.Fatal("a monitor ran with no connection enabled")
	}
}

// Only a request that can change App state re-reads whether the safety
// checks are needed; the reply does not wait for that.
func TestAppChangesUpdateTheSafetyChecks(t *testing.T) {
	var changes atomic.Int32
	h := control.AfterChange(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }), func() { changes.Add(1) })
	for _, path := range []string{"/apps/list", "/apps/inspect", "/apps/setprivate"} {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, path, nil))
	}
	deadline := time.Now().Add(time.Second)
	for changes.Load() != 1 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(10 * time.Millisecond)
	if changes.Load() != 1 {
		t.Fatalf("%d updates for one change", changes.Load())
	}
}
