package connector

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// An unused feature keeps no goroutine: the loop starts when the feature is
// turned on and has returned by the time Update reports it off.
func TestDemandRunsItsLoopOnlyWhileActive(t *testing.T) {
	var active, running atomic.Bool
	var starts atomic.Int32
	d := &Demand{Active: active.Load, Loop: func(ctx context.Context) {
		starts.Add(1)
		running.Store(true)
		defer running.Store(false)
		<-ctx.Done()
	}}
	defer d.Close()
	d.Update()
	if d.Running() || starts.Load() != 0 {
		t.Fatal("an inactive feature started its loop")
	}
	active.Store(true)
	d.Update()
	d.Update()
	if !d.Running() {
		t.Fatal("an active feature has no loop")
	}
	waitFor(t, running.Load)
	if starts.Load() != 1 {
		t.Fatalf("%d loops for one feature", starts.Load())
	}
	active.Store(false)
	d.Update()
	if d.Running() || running.Load() {
		t.Fatal("the loop outlived its feature")
	}
	active.Store(true)
	d.Update()
	waitFor(t, running.Load)
	if starts.Load() != 2 {
		t.Fatal("turning the feature on again did not restart its loop")
	}
}

// The loop learns why it stopped: an idle stop may clean up what only the
// running feature needed; a shutdown leaves that to the next start.
func TestDemandTellsTheLoopWhyItStopped(t *testing.T) {
	var active atomic.Bool
	causes := make(chan error, 2)
	d := &Demand{Active: active.Load, Loop: func(ctx context.Context) {
		<-ctx.Done()
		causes <- context.Cause(ctx)
	}}
	active.Store(true)
	d.Update()
	active.Store(false)
	d.Update()
	if e := <-causes; !errors.Is(e, ErrIdle) {
		t.Fatalf("idle stop cause %v", e)
	}
	active.Store(true)
	d.Update()
	d.Close()
	if e := <-causes; errors.Is(e, ErrIdle) {
		t.Fatal("a shutdown reported idle")
	}
	d.Update()
	if d.Running() {
		t.Fatal("a closed demand started again")
	}
}

// A loop that ends on its own is started again by the next Update.
func TestDemandRestartsALoopThatReturned(t *testing.T) {
	var starts atomic.Int32
	d := &Demand{Active: func() bool { return true }, Loop: func(context.Context) { starts.Add(1) }}
	defer d.Close()
	d.Update()
	waitFor(t, func() bool { return !d.Running() })
	d.Update()
	waitFor(t, func() bool { return starts.Load() == 2 })
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(time.Millisecond)
	}
}
