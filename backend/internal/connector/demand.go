package connector

import (
	"context"
	"errors"
	"sync"
)

// ErrIdle is the cause a Demand gives its loop when the feature was turned off.
var ErrIdle = errors.New("feature no longer in use")

var errDemandClosed = errors.New("daemon stopping")

// Demand runs a background loop only while its feature is in use, so a
// feature nobody enabled keeps no goroutine, timer or socket. The owner calls
// Update after every change that can turn the feature on or off.
//
// Update waits for a stopped loop to return, so Loop must never call Update.
type Demand struct {
	Active func() bool
	Loop   func(context.Context)
	mu     sync.Mutex
	cancel context.CancelCauseFunc
	done   chan struct{}
	closed bool
}

// Update starts the loop if the feature is in use and stops it if not.
func (d *Demand) Update() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return
	}
	want := d.Active()
	switch running := d.running(); {
	case want && !running:
		ctx, cancel := context.WithCancelCause(context.Background())
		done := make(chan struct{})
		d.cancel, d.done = cancel, done
		go func() {
			defer close(done)
			defer cancel(nil)
			d.Loop(ctx)
		}()
	case !want && running:
		d.stop(ErrIdle)
	}
}

// Running reports whether the loop is running.
func (d *Demand) Running() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.running()
}

// Close stops the loop for good and waits for it.
func (d *Demand) Close() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	if d.running() {
		d.stop(errDemandClosed)
	}
}

func (d *Demand) running() bool {
	if d.done == nil {
		return false
	}
	select {
	case <-d.done:
		return false
	default:
		return true
	}
}

func (d *Demand) stop(cause error) {
	d.cancel(cause)
	<-d.done
}
