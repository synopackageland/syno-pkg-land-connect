package provider

import (
	"bytes"
	"context"
	"dsmapp/internal/connector"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"time"
)

// liveUpdater changes only this connection's owned resources. The shared owner
// supplies journaling, commit, rollback, deadlines and health checks for every
// provider. Unsupported in-place changes must refuse before any side effect.
type liveUpdater interface {
	PrecheckUpdate(context.Context, Settings) error
	ApplyUpdate(context.Context, Settings) error
	VerifyUpdate(context.Context, Settings) (bool, error)
	RollbackUpdate(context.Context, Settings) error
	CheckHealth(context.Context) error
}

var errApplyBusy = connector.Coded("apply_busy", "wait for the current configuration change")
var errApplyUnsupported = connector.Coded("apply_unavailable", "this change cannot preserve the existing connection")
var errApplyRecovery = connector.Coded("apply_recovery_required", "the previous connection could not be verified; review its status")

type applyJournal struct {
	RuntimeBefore json.RawMessage `json:"runtimeBefore,omitempty"`
	Schema        int             `json:"schema"`
	Phase         string          `json:"phase"`
	Code          string          `json:"code,omitempty"`
	Before        selectionState  `json:"before"`
	After         selectionState  `json:"after"`
}
type pendingApply struct {
	journal applyJournal
	runtime liveUpdater
	cancel  context.CancelFunc
	done    chan struct{}
}

func applyCode(e error) string {
	if c := connector.ErrorCode(e); c != "" {
		return c
	}
	return "apply_failed"
}
func (m *Selection) journal(j applyJournal) error {
	raw, e := json.Marshal(j)
	if e != nil || len(raw) > 8192 {
		return errors.New("apply snapshot exceeds private storage bound")
	}
	return connector.WritePrivate(filepath.Join(m.dir, "apply.json"), raw)
}
func cloneState(s selectionState) selectionState {
	if s.Settings != nil {
		copy := *s.Settings
		copy.Token = ""
		s.Settings = &copy
	}
	raw, _ := json.Marshal(s)
	var out selectionState
	_ = json.Unmarshal(raw, &out)
	return out
}
func sameSettings(a, b Settings) bool { a.Token, b.Token = "", ""; return reflect.DeepEqual(a, b) }

// safeUpdate is called under the same owner lock as disconnect, port tracking
// and binary activation. It never disables the existing provider to stage work.
func (m *Selection) safeUpdate(ctx context.Context, p Runtime, s Settings, bindings map[string]int) (result connector.Status, failure error) {
	defer func() {
		if failure != nil && (m.applyStatus == nil || m.applyStatus.Phase != "recovery-required") {
			failure = connector.Preserved(failure)
		}
	}()
	if m.pending != nil {
		return m.status(), errApplyBusy
	}
	u, ok := p.(liveUpdater)
	if !ok {
		return m.status(), errApplyUnsupported
	}
	before := cloneState(m.saved)
	if before.Settings != nil && s.Token == "" && sameSettings(*before.Settings, s) && reflect.DeepEqual(before.PortBindings, bindings) {
		// An identical request is also the non-disruptive recovery action. Never
		// hide an old failure until the current session and services pass again.
		if m.applyStatus != nil && m.applyStatus.Phase != "committed" {
			check, done := context.WithTimeout(ctx, healthBudget)
			defer done()
			if e := u.PrecheckUpdate(check, s); e != nil {
				return m.status(), e
			}
			if e := u.CheckHealth(check); e != nil {
				return m.status(), e
			}
			if e := m.clearApply(); e != nil {
				return m.status(), e
			}
			m.lastGood = cloneState(m.saved)
			m.applyStatus = &connector.ApplyStatus{Phase: "committed"}
			m.failed, m.portBlocked = false, false
		}
		return m.status(), nil
	}
	m.applyChanges = configurationDiff(before.Settings, s)
	checks, cancel := context.WithTimeout(ctx, 10*time.Second)
	e := u.PrecheckUpdate(checks, s)
	if e == nil {
		e = u.CheckHealth(checks)
	}
	cancel()
	if e != nil {
		// A refused change is the result of that request, never the
		// connection's status: nothing changed, and nothing waits on the user.
		return m.status(), e
	}
	after := cloneState(selectionState{Provider: p.ID(), Enabled: true, Settings: &s, PortBindings: bindings})
	tx := &pendingApply{journal: applyJournal{Schema: 1, Phase: "applying", Before: before, After: after}, runtime: u, done: make(chan struct{})}
	if snapshotter, ok := p.(updateSnapshotter); ok {
		tx.journal.RuntimeBefore, e = snapshotter.SnapshotUpdate()
		if e != nil {
			return m.status(), e
		}
	}
	if e = m.journal(tx.journal); e != nil {
		return m.status(), e
	}
	// A CGI cancellation must not strand an accepted transaction or its rollback.
	limit := 30 * time.Second
	if p.ID() == "cloudflare" {
		limit = 12 * time.Minute
	}
	jobCtx, stop := context.WithTimeout(context.Background(), limit)
	tx.cancel = stop
	m.pending = tx
	m.applyStatus = &connector.ApplyStatus{Phase: "applying"}
	m.generation++
	if e = u.ApplyUpdate(jobCtx, s); e != nil {
		e = m.finishApply(tx, e)
		stop()
		close(tx.done)
		return m.status(), e
	}
	if ready, e := u.VerifyUpdate(ctx, s); e != nil || ready {
		e = m.finishApply(tx, e)
		stop()
		close(tx.done)
		return m.status(), e
	}
	go m.watchApply(jobCtx, tx)
	return m.status(), nil
}
func (m *Selection) watchApply(ctx context.Context, tx *pendingApply) {
	defer close(tx.done)
	defer tx.cancel()
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
		case <-tick.C:
		}
		m.mu.Lock()
		if m.pending != tx {
			m.mu.Unlock()
			return
		}
		e := ctx.Err()
		ready := false
		if e == nil {
			check, done := context.WithTimeout(ctx, 5*time.Second)
			ready, e = tx.runtime.VerifyUpdate(check, *tx.journal.After.Settings)
			done()
		}
		if e != nil || ready {
			m.finishApply(tx, e)
			m.mu.Unlock()
			return
		}
		m.mu.Unlock()
	}
}
func (m *Selection) finishApply(tx *pendingApply, cause error) error {
	committed := false
	defer func() {
		if c, ok := tx.runtime.(interface{ FinishUpdate(bool) }); ok {
			c.FinishUpdate(committed)
		}
	}()
	if cause == nil {
		if c, ok := tx.runtime.(interface{ CommitUpdate(context.Context) error }); ok {
			ctx, done := context.WithTimeout(context.Background(), 15*time.Second)
			cause = c.CommitUpdate(ctx)
			done()
		}
	}
	if cause == nil {
		health, cancel := context.WithTimeout(context.Background(), healthBudget)
		cause = tx.runtime.CheckHealth(health)
		cancel()
	}
	if cause == nil {
		// Keep the rollback snapshot durable until both provider and owner readback
		// have passed. A crash before committed replays the old accepted intent.
		cause = m.persist(tx.journal.After)
		if cause == nil {
			raw, e := connector.ReadPrivate(filepath.Join(m.dir, "provider.json"))
			want, _ := json.Marshal(tx.journal.After)
			if e != nil || !bytes.Equal(raw, want) {
				cause = errors.New("owner readback failed")
			}
		}
		if cause == nil {
			tx.journal.Phase = "committed"
			cause = m.journal(tx.journal)
		}
	}
	_, repairing := tx.runtime.(*repairPlan)
	if repairing {
		connector.Logf("%s: repair %d of 3 finished: %v", m.saved.Provider, m.repairAttempts, cause)
	}
	if cause == nil {
		committed = true
		m.healthFailures = 0
		// A repair passing its own check once is not a stable connection; only
		// healthyWindow of passing health checks resets the repair cap.
		if !repairing {
			m.repairAttempts = 0
		}
		m.lastGood = cloneState(tx.journal.After)
		m.applyStatus = &connector.ApplyStatus{Phase: "committed"}
		m.failed = false
		m.portBlocked = false
		m.pending = nil
		m.recovering = nil
		return nil
	}
	m.applyStatus = &connector.ApplyStatus{Phase: "rolling-back", Code: applyCode(cause)}
	recovery, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	e := tx.runtime.RollbackUpdate(recovery, *tx.journal.Before.Settings)
	if e == nil {
		e = tx.runtime.CheckHealth(recovery)
	}
	if e == nil {
		e = m.persist(tx.journal.Before)
	}
	if e == nil {
		tx.journal.Phase = "rolled-back"
		e = m.journal(tx.journal)
	}
	m.pending = nil
	if e != nil {
		// A failed compensation is terminal for this worker, but its snapshots
		// remain available for restart recovery. Do not leave it looking active.
		tx.journal.Phase = "recovery-required"
		tx.journal.Code = applyCode(cause)
		if journalError := m.journal(tx.journal); journalError != nil {
			m.applyStatus = &connector.ApplyStatus{Phase: "recovery-required", Code: "apply_failed"}
			return errApplyRecovery
		}
		m.applyStatus = &connector.ApplyStatus{Phase: "recovery-required", Code: applyCode(cause)}
		if repairing {
			// The daemon a repair left behind may still come up on its own;
			// the next healthy check settles this journal.
			j := tx.journal
			m.recovering = &j
		}
		return errApplyRecovery
	}
	m.lastGood = cloneState(tx.journal.Before)
	m.applyStatus = &connector.ApplyStatus{Phase: "rolled-back", Code: applyCode(cause)}
	if applyCode(cause) == "apply_unavailable" {
		// Refused, not failed: the request reports it, the connection is as it was.
		m.applyStatus = nil
	}
	m.failed = false
	m.recovering = nil
	return cause
}

// Explicitly accepted replacement intent supersedes every older transaction.
// Called only under the owner lock, after the new durable gate is saved.
func (m *Selection) clearApply() error {
	e := os.Remove(filepath.Join(m.dir, "apply.json"))
	if e == nil {
		// Match WritePrivate's durability: a crash must not resurrect the unlink.
		var dir *os.File
		dir, e = os.Open(m.dir)
		if e == nil {
			e = dir.Sync()
			dir.Close()
		}
	}
	if e != nil && !os.IsNotExist(e) {
		m.applyStatus = &connector.ApplyStatus{Phase: "recovery-required", Code: "apply_failed"}
		return e
	}
	m.recovering = nil
	m.applyStatus = nil
	m.applyChanges = connector.ApplyStatus{}
	m.healthFailures, m.repairAttempts = 0, 0
	m.nextRepair, m.healthySince = time.Time{}, time.Time{}
	return nil
}

// cancelApply is only used for explicit shutdown/revocation. It must not replay
// a snapshot after disable; the durable disabled gate wins over any old journal.
func (m *Selection) cancelApply() {
	m.lastGood = selectionState{}
	m.recovering = nil
	m.healthFailures = 0
	if tx := m.pending; tx != nil {
		tx.cancel()
		m.pending = nil
		m.applyStatus = &connector.ApplyStatus{Phase: "canceled"}
	}
}
func (m *Selection) recoverApply() error {
	raw, e := connector.ReadPrivate(filepath.Join(m.dir, "apply.json"))
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return errApplyRecovery
	}
	var j applyJournal
	dec := json.NewDecoder(bytes.NewReader(withoutTCPPorts(raw)))
	dec.DisallowUnknownFields()
	if dec.Decode(&j) != nil || dec.Decode(new(any)) != io.EOF || j.Schema != 1 || j.Before.Provider != m.saved.Provider || j.After.Provider != j.Before.Provider || j.Before.Settings == nil || j.Before.Settings.Token != "" || j.After.Settings == nil || j.After.Settings.Token != "" {
		return errApplyRecovery
	}
	if !m.saved.Enabled {
		return nil
	}
	// A newer explicit withdrawal/reconfiguration supersedes an old journal.
	if !reflect.DeepEqual(cloneState(m.saved), cloneState(j.Before)) && !reflect.DeepEqual(cloneState(m.saved), cloneState(j.After)) {
		return m.clearApply()
	}
	switch j.Phase {
	case "committed":
		m.lastGood = cloneState(j.After)
	case "rolled-back":
		m.lastGood = cloneState(j.Before)
	case "applying", "recovery-required":
		if len(j.RuntimeBefore) > 0 {
			snap, ok := m.registry[m.saved.Provider].(updateSnapshotter)
			if !ok {
				return errApplyRecovery
			}
			if e = snap.RestoreUpdate(j.RuntimeBefore); e != nil {
				if !connector.IsPreserved(e) {
					return e
				}
				m.applyStatus = &connector.ApplyStatus{Phase: "recovery-required", Code: applyCode(e)}
			}
		}
		if e = m.persist(j.Before); e != nil {
			return e
		}
		m.lastGood = cloneState(j.Before)
		if m.applyStatus == nil || m.applyStatus.Phase != "recovery-required" {
			m.recovering = &j
			m.applyStatus = &connector.ApplyStatus{Phase: "recovering"}
		}
	default:
		return errApplyRecovery
	}
	return nil
}

func configurationDiff(before *Settings, after Settings) connector.ApplyStatus {
	ports := func(s Settings) []int {
		out := []int{}
		for _, p := range s.Mappings {
			out = append(out, p.PublicPort)
		}
		return out
	}
	old := []int{}
	if before != nil {
		old = ports(*before)
	}
	next := ports(after)
	var d connector.ApplyStatus
	for _, p := range next {
		if !slices.Contains(old, p) {
			d.AddedPorts = append(d.AddedPorts, p)
		}
	}
	for _, p := range old {
		if !slices.Contains(next, p) {
			d.RemovedPorts = append(d.RemovedPorts, p)
		}
	}
	return d
}
