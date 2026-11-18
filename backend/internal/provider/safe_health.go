package provider

import (
	"context"
	"dsmapp/internal/connector"
	"dsmapp/internal/supervise"
	"errors"
	"reflect"
	"time"
)

// A loaded NAS can take a minute to answer one status read: a swapped-out
// daemon pages itself back in from disk first. Health observes for that long.
const healthBudget = time.Minute

// The repair cap holds until a connection stays healthy this long; a repair
// that passes its own check once does not reset it.
const healthyWindow = 30 * time.Minute

// healthSettle is how long a connection that was just made or restored may
// fail its health checks unseen: it is still setting up, and the checker
// shows that. It is the checker's settling window.
var healthSettle = connector.SettleWindow

func (m *Selection) beginSettling() {
	m.settleUntil = time.Now().Add(healthSettle(m.saved.Provider))
}

// errHealthWaiting marks an observation that could not finish; it is
// shared with every provider through supervise.
var errHealthWaiting = supervise.ErrWaiting

// healthStep and healthWaiting name the step and provider attempt, so the
// log says which check failed. They are the shared supervise helpers.
func healthStep(step string, attempt uint64, err error) error {
	return supervise.Step(step, attempt, err)
}
func healthWaiting(step string, attempt uint64, err error) error {
	return supervise.Wait(step, attempt, err)
}

// WatchHealth never opens a state file or journals a successful observation.
// Memory, native daemon sockets and bounded network probes are its only idle
// inputs. Recovery is a real apply and is journaled by the same transaction owner.
func (m *MultiSelection) WatchHealth(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		return
	}
	tick := time.NewTicker(interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			for _, id := range connectionIDs {
				m.children[id].healthTick(ctx)
			}
		}
	}
}

// SetPressure installs the NAS memory-pressure probe. While it reports
// pressure, a failed health check never restarts a provider: restarting a
// daemon pages its whole binary back in and makes a swapping NAS slower.
func (m *MultiSelection) SetPressure(pressure func() bool) {
	for _, id := range connectionIDs {
		child := m.children[id]
		child.mu.Lock()
		child.pressure = pressure
		child.mu.Unlock()
	}
}

// healthTick observes without the owner lock, so a minute-long read on a
// loaded NAS never blocks status. A change while it observed discards it.
func (m *Selection) healthTick(ctx context.Context) {
	m.mu.Lock()
	if ctx.Err() != nil || !m.saved.Enabled || m.pending != nil {
		m.mu.Unlock()
		return
	}
	p := m.registry[m.saved.Provider]
	u, ok := p.(liveUpdater)
	if !ok {
		m.mu.Unlock()
		return
	}
	generation, epoch := m.generation, m.portEpoch
	m.mu.Unlock()
	check, done := context.WithTimeout(ctx, healthBudget)
	e := u.CheckHealth(check)
	done()
	m.mu.Lock()
	defer m.mu.Unlock()
	if ctx.Err() != nil || m.generation != generation || m.portEpoch != epoch || !m.saved.Enabled || m.pending != nil || m.registry[m.saved.Provider] != p {
		return
	}
	step, attempt := "check", uint64(0)
	var stepErr *supervise.StepError
	if errors.As(e, &stepErr) {
		step, attempt = stepErr.Step, stepErr.Attempt
	}
	if errors.Is(e, errHealthWaiting) {
		connector.Logf("%s: health check incomplete at %s (attempt %d): %v", p.ID(), step, attempt, e)
		return
	}
	if e == nil {
		m.healthFailures = 0
		if m.healthySince.IsZero() {
			m.healthySince = time.Now()
		}
		if m.repairAttempts > 0 && time.Since(m.healthySince) >= healthyWindow {
			connector.Logf("%s: healthy for %s; repair limit reset", p.ID(), healthyWindow)
			m.repairAttempts = 0
			m.nextRepair = time.Time{}
		}
		// A connection that passes again clears what its failed checks and
		// repairs reported. A journal that could not be written stays.
		if m.applyStatus != nil && (m.applyStatus.Phase == "check-failed" || m.applyStatus.Phase == "recovery-required" && m.applyStatus.Code != "apply_failed") {
			m.applyStatus = nil
		}
		if m.recovering != nil {
			j := *m.recovering
			// A repair changed nothing: a healthy connection settles it.
			// Only an interrupted change was rolled back to its old state.
			repair := reflect.DeepEqual(cloneState(j.Before), cloneState(j.After))
			j.Phase = "rolled-back"
			if repair {
				j.Phase = "committed"
			}
			if e := m.journal(j); e != nil {
				m.applyStatus = &connector.ApplyStatus{Phase: "recovery-required", Code: "apply_failed"}
				return
			}
			m.recovering = nil
			m.applyStatus = &connector.ApplyStatus{Phase: "rolled-back", Code: "apply_failed"}
			if repair {
				m.applyStatus = nil
			}
		}
		m.lastGood = cloneState(m.saved)
		return
	}
	m.healthySince = time.Time{}
	if time.Now().Before(m.settleUntil) {
		connector.Logf("%s: health check failed at %s while setting up (attempt %d): %v", p.ID(), step, attempt, e)
		return
	}
	// Provider process recovery belongs to its SDK policy. Health observations
	// never start a new series, including after failed; Retry is an explicit action.
	if _, owns := p.(backgroundRestorer); owns {
		phase := "check-failed"
		if applyCode(e) == "apply_reauth_required" || applyCode(e) == "apply_cloudflare_authorization_required" {
			phase = "reauth-required"
		}
		m.applyStatus = &connector.ApplyStatus{Phase: phase, Code: applyCode(e)}
		return
	}
	m.healthFailures++
	connector.Logf("%s: health check failed at %s (attempt %d, %d in a row, repairs %d of 3): %v", p.ID(), step, attempt, m.healthFailures, m.repairAttempts, e)
	if m.healthFailures < 3 {
		return
	}
	code := applyCode(e)
	// Reauthorizing an identity or modifying third-party policy is never an
	// automatic recovery step. Expose it even when no browser is polling.
	if code == "apply_reauth_required" || code == "apply_cloudflare_authorization_required" {
		m.applyStatus = &connector.ApplyStatus{Phase: "reauth-required", Code: code}
		return
	}
	if m.lastGood.Settings == nil && m.recovering == nil && (m.applyStatus == nil || m.applyStatus.Phase == "check-failed") {
		// No accepted snapshot exists and no recovery has been attempted.
		// Report the health check itself, not a failed rollback.
		m.applyStatus = &connector.ApplyStatus{Phase: "check-failed", Code: code}
		return
	}
	if m.repairAttempts >= 3 || m.lastGood.Settings == nil {
		if m.applyStatus == nil || m.applyStatus.Phase != "recovery-required" {
			connector.Logf("%s: repair limit reached; recovery required", p.ID())
		}
		m.applyStatus = &connector.ApplyStatus{Phase: "recovery-required", Code: code}
		return
	}
	if time.Now().Before(m.nextRepair) {
		return
	}
	if m.pressure != nil && m.pressure() {
		connector.Logf("%s: repair postponed while the NAS is short of memory", p.ID())
		return
	}
	m.repairAttempts++
	m.nextRepair = time.Now().Add(time.Duration(m.repairAttempts) * time.Minute)
	repair, ok := p.(repairUpdater)
	if !ok {
		m.applyStatus = &connector.ApplyStatus{Phase: "recovery-required", Code: code}
		return
	}
	connector.Logf("%s: repairing (repair %d of 3)", p.ID(), m.repairAttempts)
	m.beginRepair(ctx, p, u, repair)
}

// backgroundRestorer is a provider whose daemon restores through the shared
// supervise.Restorer, which keeps waiting on a slow NAS.
type backgroundRestorer interface{ restoresInBackground() }

func (*NetBird) restoresInBackground()          {}
func (*Tailscale) restoresInBackground()        {}
func (*CloudflareTunnel) restoresInBackground() {}

// slowRead clears a read that did not answer, as on a swapping NAS: a repair
// keeps waiting on it. An exited daemon or a code the daemon reported fails.
func slowRead(e error) error {
	if e == nil || errors.Is(e, ErrDaemonExited) || connector.ErrorCode(e) != "" {
		return e
	}
	return nil
}

type repairUpdater interface {
	PrecheckRepair(context.Context) error
	RepairUpdate(context.Context) error
	VerifyRepair(context.Context) (bool, error)
}

func (m *Selection) beginRepair(ctx context.Context, p Runtime, u liveUpdater, repair repairUpdater) {
	if e := repair.PrecheckRepair(ctx); e != nil {
		phase := "recovery-required"
		if applyCode(e) == "apply_reauth_required" {
			phase = "reauth-required"
		}
		m.applyStatus = &connector.ApplyStatus{Phase: phase, Code: applyCode(e)}
		return
	}
	before := cloneState(m.lastGood)
	tx := &pendingApply{journal: applyJournal{Schema: 1, Phase: "applying", Before: before, After: before}, runtime: &repairPlan{liveUpdater: u, repairUpdater: repair}, done: make(chan struct{})}
	if snap, ok := p.(updateSnapshotter); ok {
		raw, e := snap.SnapshotUpdate()
		if e != nil {
			return
		}
		tx.journal.RuntimeBefore = raw
	}
	if e := m.journal(tx.journal); e != nil {
		m.applyStatus = &connector.ApplyStatus{Phase: "recovery-required", Code: "apply_failed"}
		return
	}
	// A daemon restarted on a loaded NAS keeps starting in the background;
	// its repair waits for that rather than failing at once.
	limit := 30 * time.Second
	if _, ok := p.(backgroundRestorer); ok {
		limit = 15 * time.Minute
	}
	job, cancel := context.WithTimeout(context.Background(), limit)
	tx.cancel = cancel
	m.pending = tx
	m.applyStatus = &connector.ApplyStatus{Phase: "repairing"}
	if e := repair.RepairUpdate(job); e != nil {
		m.finishApply(tx, e)
		cancel()
		close(tx.done)
		return
	}
	go m.watchApply(job, tx)
}

type repairPlan struct {
	liveUpdater
	repairUpdater
}

func (p *repairPlan) VerifyUpdate(ctx context.Context, _ Settings) (bool, error) {
	return p.VerifyRepair(ctx)
}

// Failed repair has no working previous runtime to restore. Never report it as
// rolled-back merely because a saved file was written successfully.
func (*repairPlan) RollbackUpdate(context.Context, Settings) error { return errApplyRecovery }
func (p *CloudflareTunnel) RepairUpdate(ctx context.Context) error { return p.Tunnel.Repair(ctx) }
func (p *CloudflareTunnel) VerifyRepair(context.Context) (bool, error) {
	s := p.Tunnel.Status()
	if s.State == "failed" {
		return false, errApplyRecovery
	}
	return s.State == "connected", nil
}
func (p *Tailscale) RepairUpdate(ctx context.Context) error { return p.Restore(ctx) }
func (p *Tailscale) VerifyRepair(ctx context.Context) (bool, error) {
	p.mu.Lock()
	s, restoring := p.saved.Settings, p.restoring.Active()
	p.mu.Unlock()
	if restoring {
		return false, nil
	}
	ready, e := p.VerifyUpdate(ctx, s)
	return ready, slowRead(e)
}
func (p *NetBird) RepairUpdate(ctx context.Context) error { return p.Restore(ctx) }
func (p *NetBird) VerifyRepair(ctx context.Context) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.restoring.Active() {
		return false, nil
	}
	if p.session != nil {
		s, e := p.session.Inspect(ctx)
		if e != nil {
			return false, slowRead(e)
		}
		if s.Code != "" {
			return false, nbError(s.Code)
		}
		return s.ManagementConnected && s.SignalConnected && s.IP != "", nil
	}
	return p.sessionSnapshot(ctx)
}

func (p *CloudflareTunnel) PrecheckRepair(context.Context) error {
	s := p.Tunnel.Status()
	if s.Mode != "domain" && s.Mode != "token" {
		return errApplyUnsupported
	}
	return nil
}
func (p *Tailscale) PrecheckRepair(ctx context.Context) error {
	p.mu.Lock()
	enabled := p.saved.Enabled
	p.mu.Unlock()
	if !enabled {
		return connector.Coded("apply_reauth_required", "sign in to Tailscale again")
	}
	return p.Preflight(ctx)
}
func (p *NetBird) PrecheckRepair(ctx context.Context) error {
	p.mu.Lock()
	enabled := p.saved.Enabled
	p.mu.Unlock()
	if !enabled {
		return errApplyUnsupported
	}
	return p.Preflight(ctx)
}
