package connector

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrConnectBusy refuses a mutation while an accepted connect is still starting.
var ErrConnectBusy = Coded("connect_busy", "a connection is still starting; try again shortly")

// ErrDisconnectBusy refuses a connect while an accepted disconnect is still stopping.
var ErrDisconnectBusy = Coded("disconnect_busy", "a connection is still stopping; try again shortly")

// ErrConnectCanceled names a start that a later disconnect or shutdown canceled,
// so the provider error the cancellation caused is never reported instead.
var ErrConnectCanceled = Coded("connect_canceled", "the connection was canceled before it finished starting")

// Async runs each connect and disconnect in the daemon, detached from the CGI
// request, so a slow start or stop on a loaded NAS never meets the CGI deadline
// or its cancellation. A result within Accept is returned as before; otherwise
// the request is accepted as connecting or disconnecting and status polling
// reports the outcome.
type Async struct {
	Accept time.Duration
	Limit  time.Duration
	inner  Controller
	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	job    *mutation
	failed map[string]error
	last   *Status
}

// mutation is the one pending connect or disconnect; status reads its snapshot
// until it ends. A disconnect's empty provider means every connection.
type mutation struct {
	stop     bool
	provider string
	snapshot Status
	cancel   context.CancelFunc
	canceled bool
	done     chan struct{}
	status   Status
	err      error
}

func NewAsync(inner Controller) *Async {
	ctx, cancel := context.WithCancel(context.Background())
	return &Async{Accept: 4 * time.Second, Limit: 2 * time.Minute, inner: inner, ctx: ctx, cancel: cancel, failed: map[string]error{}}
}

func (a *Async) Connect(ctx context.Context, r Request) (Status, error) {
	id := r.Provider
	if id == "" {
		id = "cloudflare"
	}
	a.mu.Lock()
	if a.job != nil {
		a.mu.Unlock()
		return Status{}, a.job.busy()
	}
	delete(a.failed, id)
	jobCtx, cancel := context.WithTimeout(a.ctx, a.Limit)
	job := &mutation{provider: id, cancel: cancel, done: make(chan struct{})}
	// Reading every provider's status can take seconds on a loaded NAS; the
	// status last served stands in for the others while this one starts.
	if a.last != nil {
		job.snapshot = withState(*a.last, id, "connecting", "")
	} else {
		job.snapshot = withState(a.inner.Status(), id, "connecting", "")
	}
	a.job = job
	a.mu.Unlock()
	go a.run(jobCtx, job, r)
	timer := time.NewTimer(a.Accept)
	defer timer.Stop()
	select {
	case <-job.done:
		return a.settled(job, id)
	case <-timer.C:
	case <-ctx.Done():
	}
	return job.snapshot, nil
}

// busy names the refusal for a mutation that arrives while this one is pending.
func (m *mutation) busy() error {
	if m.stop {
		return ErrDisconnectBusy
	}
	return ErrConnectBusy
}

// settled returns a result that finished within Accept; the caller receives
// any refusal, so it does not linger for status polling.
func (a *Async) settled(job *mutation, ids ...string) (Status, error) {
	a.mu.Lock()
	for _, id := range ids {
		delete(a.failed, id)
	}
	a.mu.Unlock()
	return job.status, job.err
}
func (a *Async) run(ctx context.Context, job *mutation, r Request) {
	defer job.cancel()
	Logf("connect %s accepted", job.provider)
	started := time.Now()
	s, e := a.inner.Connect(ctx, r)
	a.mu.Lock()
	if e != nil && (job.canceled || a.ctx.Err() != nil) {
		e = ErrConnectCanceled
	}
	job.status, job.err = s, e
	if e != nil && !errors.Is(e, ErrConnectCanceled) && !IsPreserved(e) {
		a.failed[job.provider] = e
	}
	a.mu.Unlock()
	logOutcome("connect "+job.provider, e, time.Since(started))
	a.mu.Lock()
	close(job.done)
	a.release(job)
	a.mu.Unlock()
}

// release frees the slot unless a disconnect already took it over; a.mu is held.
func (a *Async) release(job *mutation) {
	if a.job == job {
		a.job = nil
	}
}

// wait returns once no mutation is pending.
func (a *Async) wait() {
	a.mu.Lock()
	job := a.job
	a.mu.Unlock()
	if job != nil {
		<-job.done
	}
}

// Status never waits behind a pending mutation: the provider lock is held for
// the whole start or stop, so the snapshot taken at acceptance stands in for it.
func (a *Async) Status() Status {
	a.mu.Lock()
	if a.job != nil {
		s := a.job.snapshot
		a.mu.Unlock()
		return s
	}
	failed := make(map[string]error, len(a.failed))
	for id, e := range a.failed {
		failed[id] = e
	}
	a.mu.Unlock()
	s := a.inner.Status()
	cached := s
	a.mu.Lock()
	a.last = &cached
	a.mu.Unlock()
	for id, e := range failed {
		code := ErrorCode(e)
		if code == "" {
			code = "start_failed"
		}
		s = withState(s, id, "failed", code)
	}
	return s
}

func (a *Async) Disconnect() (Status, error) { return a.disconnect("") }
func (a *Async) DisconnectProvider(id string) (Status, error) {
	if _, ok := a.inner.(interface{ DisconnectProvider(string) (Status, error) }); !ok {
		return Status{}, errors.New("scoped disconnect unavailable")
	}
	return a.disconnect(id)
}

// disconnect cancels a pending connect of the same provider (all for "") and
// stops in the background; another provider's pending connect refuses it. A
// repeated disconnect joins the pending one instead of stopping twice.
func (a *Async) disconnect(id string) (Status, error) {
	a.mu.Lock()
	prev := a.job
	if prev != nil && prev.stop {
		a.mu.Unlock()
		if prev.provider == "" || prev.provider == id {
			return prev.snapshot, nil
		}
		return a.Status(), ErrDisconnectBusy
	}
	if prev != nil && id != "" && id != prev.provider {
		a.mu.Unlock()
		return a.Status(), ErrConnectBusy
	}
	job := &mutation{stop: true, provider: id, done: make(chan struct{})}
	switch {
	case prev != nil:
		prev.canceled = true
		prev.cancel()
		job.snapshot = disconnecting(prev.snapshot, id)
	case a.last != nil:
		job.snapshot = disconnecting(*a.last, id)
	default:
		job.snapshot = disconnecting(a.inner.Status(), id)
	}
	a.job = job
	a.mu.Unlock()
	go a.stop(prev, job)
	timer := time.NewTimer(a.Accept)
	defer timer.Stop()
	select {
	case <-job.done:
		return a.settled(job, stopping(job.snapshot)...)
	case <-timer.C:
	}
	return job.snapshot, nil
}
func (a *Async) stop(prev, job *mutation) {
	if prev != nil {
		<-prev.done
	}
	ids := stopping(job.snapshot)
	a.mu.Lock()
	if job.provider == "" {
		a.failed = map[string]error{}
	}
	for _, id := range ids {
		delete(a.failed, id)
	}
	a.mu.Unlock()
	what := "disconnect all"
	if job.provider != "" {
		what = "disconnect " + job.provider
	}
	started := time.Now()
	var s Status
	var e error
	if job.provider == "" {
		s, e = a.inner.Disconnect()
	} else {
		s, e = a.inner.(interface{ DisconnectProvider(string) (Status, error) }).DisconnectProvider(job.provider)
	}
	logOutcome(what, e, time.Since(started))
	a.mu.Lock()
	job.status, job.err = s, e
	if e != nil {
		for _, id := range ids {
			a.failed[id] = e
		}
	}
	a.release(job)
	a.mu.Unlock()
	close(job.done)
}

// disconnecting marks the connections a disconnect stops (all for "").
func disconnecting(s Status, id string) Status {
	if id != "" {
		return withState(s, id, "disconnecting", "")
	}
	for _, c := range append([]Status{s}, s.Connections...) {
		if c.Provider != "" && c.State != "disconnected" {
			s = withState(s, c.Provider, "disconnecting", "")
		}
	}
	return s
}

// Stopping reports an accepted disconnect that has not finished yet.
func (s Status) Stopping() bool { return len(stopping(s)) != 0 }

// stopping lists the providers a disconnect snapshot marks as stopping.
func stopping(s Status) []string {
	var ids []string
	for _, c := range s.Connections {
		if c.State == "disconnecting" {
			ids = append(ids, c.Provider)
		}
	}
	if len(ids) == 0 && s.State == "disconnecting" {
		ids = append(ids, s.Provider)
	}
	return ids
}

// logOutcome records one mutation's result with its code and duration.
func logOutcome(what string, e error, took time.Duration) {
	took = took.Round(10 * time.Millisecond)
	if e == nil {
		Logf("%s ok in %s", what, took)
		return
	}
	if errors.Is(e, ErrConnectCanceled) {
		Logf("%s canceled in %s", what, took)
		return
	}
	Logf("%s failed in %s: code=%s reason=%q", what, took, ErrorCode(e), e.Error())
}

// Forget clears a disconnected provider's saved services; it never runs beside
// a pending connect or disconnect.
func (a *Async) Forget(id string) (Status, error) {
	forgetter, ok := a.inner.(interface{ Forget(string) (Status, error) })
	if !ok {
		return Status{}, errors.New("forgetting services unavailable")
	}
	a.mu.Lock()
	job := a.job
	a.mu.Unlock()
	if job != nil {
		return a.Status(), job.busy()
	}
	return forgetter.Forget(id)
}
func (a *Async) Restore(ctx context.Context) error {
	if restore, ok := a.inner.(interface{ Restore(context.Context) error }); ok {
		return restore.Restore(ctx)
	}
	return nil
}

// Stop cancels a pending connect, or waits for a pending disconnect, before
// stopping the owned processes.
func (a *Async) Stop() error {
	a.cancel()
	a.wait()
	return a.inner.Stop()
}

// withState overrides one provider's state in a selector or single status.
func withState(s Status, id, state, code string) Status {
	s.Connections = append([]Status(nil), s.Connections...)
	found := false
	for i := range s.Connections {
		if s.Connections[i].Provider == id {
			s.Connections[i].State, s.Connections[i].Code = state, code
			found = true
		}
	}
	if s.Provider == id {
		s.State, s.Code = state, code
		found = true
	}
	if !found {
		s.Connections = append(s.Connections, Status{Provider: id, State: state, Code: code})
	}
	return s
}
