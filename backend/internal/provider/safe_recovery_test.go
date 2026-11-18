package provider

import (
	"context"
	"dsmapp/internal/connector"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type recoveryFixture struct {
	safeFixture
	repairError, verifyError, healthError error
}

func (*recoveryFixture) PrecheckRepair(context.Context) error         { return nil }
func (p *recoveryFixture) RepairUpdate(context.Context) error         { return p.repairError }
func (p *recoveryFixture) VerifyRepair(context.Context) (bool, error) { return false, p.verifyError }
func (p *recoveryFixture) CheckHealth(context.Context) error          { return p.healthError }

func readApplyJournal(t *testing.T, m *Selection) applyJournal {
	t.Helper()
	raw, e := os.ReadFile(filepath.Join(m.dir, "apply.json"))
	if e != nil {
		t.Fatal(e)
	}
	var j applyJournal
	if e = json.Unmarshal(raw, &j); e != nil {
		t.Fatal(e)
	}
	return j
}
func recoverySelection(t *testing.T) (*Selection, *recoveryFixture) {
	t.Helper()
	p := &recoveryFixture{safeFixture: safeFixture{fixtureRuntime: fixtureRuntime{id: "netbird", events: &[]string{}}}}
	m := NewSelection(t.TempDir(), p)
	if _, e := m.Connect(context.Background(), connector.Request{Provider: "netbird"}); e != nil {
		t.Fatal(e)
	}
	m.lastGood = cloneState(m.saved)
	return m, p
}
func TestRepairFailureClosesJournalAndAllowsRetry(t *testing.T) {
	for _, async := range []bool{false, true} {
		t.Run(map[bool]string{false: "start", true: "login_url_timeout"}[async], func(t *testing.T) {
			m, p := recoverySelection(t)
			failure := connector.Coded("login_url_timeout", "synthetic timeout")
			if async {
				p.verifyError = failure
			} else {
				p.repairError = failure
			}
			m.mu.Lock()
			m.beginRepair(context.Background(), p, p, p)
			tx := m.pending
			m.mu.Unlock()
			if async {
				if tx == nil {
					t.Fatal("no pending repair")
				}
				select {
				case <-tx.done:
				case <-time.After(time.Second):
					t.Fatal("repair did not settle")
				}
			}
			if j := readApplyJournal(t, m); j.Phase == "applying" {
				t.Fatal("failed repair stranded applying journal")
			}
			if m.pending != nil || m.applyStatus == nil || m.applyStatus.Phase != "recovery-required" {
				t.Fatal("failed repair not retryable", m.applyStatus)
			}
			// A healthy existing session must be recoverable without stop/start or a key.
			events := len(*p.events)
			m.failed = true
			if status, e := m.Connect(context.Background(), connector.Request{Provider: "netbird"}); e != nil || status.State != "connected" {
				t.Fatal("verified retry retained failed owner status", status, e)
			}
			if m.applyStatus != nil && m.applyStatus.Phase != "committed" {
				t.Fatal("retry retained old failure", m.applyStatus)
			}
			if len(*p.events) != events {
				t.Fatal("repair disrupted existing connection")
			}
		})
	}
}
func TestReconnectSupersedesOldJournalAcrossRestart(t *testing.T) {
	m, p := recoverySelection(t)
	before := cloneState(m.saved)
	if e := m.journal(applyJournal{Schema: 1, Phase: "applying", Before: before, After: before}); e != nil {
		t.Fatal(e)
	}
	m.recovering = &applyJournal{Schema: 1, Phase: "applying", Before: before, After: before}
	m.applyStatus = &connector.ApplyStatus{Phase: "recovery-required", Code: "local_check_failed"}
	p.live = false
	if _, e := m.Connect(context.Background(), connector.Request{Provider: "netbird", Token: "SYNTHETIC-KEY"}); e != nil {
		t.Fatal(e)
	}
	m.healthTick(context.Background())
	if m.recovering != nil || m.applyStatus != nil && m.applyStatus.Phase != "committed" {
		t.Fatal("successful reconnect retained recovery", m.applyStatus)
	}
	fresh := NewSelection(m.dir, p)
	if e := fresh.Restore(context.Background()); e != nil {
		t.Fatal(e)
	}
	if fresh.recovering != nil {
		t.Fatal("restart resurrected superseded journal")
	}
}
func TestFailedRepairJournalCanRecoverAfterRestart(t *testing.T) {
	m, p := recoverySelection(t)
	p.repairError = connector.Coded("login_url_timeout", "synthetic timeout")
	m.beginRepair(context.Background(), p, p, p)
	fresh := NewSelection(m.dir, p)
	if e := fresh.Restore(context.Background()); e != nil {
		t.Fatal(e)
	}
	if a := fresh.Status().Apply; a == nil || a.Phase != "recovering" {
		t.Fatal("restart must resume the interrupted repair", a)
	}
	fresh.healthTick(context.Background())
	// A repair changed nothing, so a healthy connection has nothing to roll back.
	if j := readApplyJournal(t, fresh); j.Phase != "committed" {
		t.Fatal("healthy restart did not settle journal", j.Phase)
	}
	if a := fresh.Status().Apply; a != nil {
		t.Fatal("a settled repair must leave no recovery message", a)
	}
}

// Regression: a repair that timed out left "recovery-required" on
// the page even after NetBird later became healthy on its own.
func TestFailedRepairClearsOnceHealthy(t *testing.T) {
	m, p := recoverySelection(t)
	p.repairError = connector.Coded("daemon_start_timeout", "synthetic timeout")
	m.mu.Lock()
	m.beginRepair(context.Background(), p, p, p)
	m.mu.Unlock()
	if a := m.Status().Apply; a == nil || a.Phase != "recovery-required" {
		t.Fatal("a failed repair must say so", a)
	}
	m.healthTick(context.Background())
	if a := m.Status().Apply; a != nil {
		t.Fatal("a healthy connection must clear the failed repair", a)
	}
	if j := readApplyJournal(t, m); j.Phase != "committed" {
		t.Fatal("the failed repair's journal must be settled", j.Phase)
	}
	fresh := NewSelection(m.dir, p)
	if e := fresh.Restore(context.Background()); e != nil {
		t.Fatal(e)
	}
	if a := fresh.Status().Apply; a != nil {
		t.Fatal("a restart must not resurrect a settled repair", a)
	}
}

// Exhausted repairs say recovery is required; once health passes again on
// its own, the message goes.
func TestRepairLimitClearsOnceHealthy(t *testing.T) {
	m, p := recoverySelection(t)
	m.mu.Lock()
	m.repairAttempts = 3
	m.mu.Unlock()
	p.healthError = nbError("control_unreachable")
	for i := 0; i < 3; i++ {
		m.healthTick(context.Background())
	}
	if a := m.Status().Apply; a == nil || a.Phase != "recovery-required" {
		t.Fatal("exhausted repairs must say recovery is required", a)
	}
	p.healthError = nil
	m.healthTick(context.Background())
	if a := m.Status().Apply; a != nil {
		t.Fatal("a healthy connection must clear the repair limit message", a)
	}
}

// A durable journal failure stays visible even while the connection works.
func TestJournalFailureStaysVisibleWhenHealthy(t *testing.T) {
	m, _ := recoverySelection(t)
	m.mu.Lock()
	m.applyStatus = &connector.ApplyStatus{Phase: "recovery-required", Code: "apply_failed"}
	m.mu.Unlock()
	m.healthTick(context.Background())
	if a := m.Status().Apply; a == nil || a.Code != "apply_failed" {
		t.Fatal("a journal failure must stay visible", a)
	}
}

func TestNetBirdRepairSurfacesTerminalLoginFailure(t *testing.T) {
	p := NewNetBird(t.TempDir(), nil)
	p.session = &nbSession{snapshot: NetBirdSnapshot{DaemonStatus: "LoginFailed", Code: "login_url_timeout"}}
	if ready, e := p.VerifyRepair(context.Background()); ready || connector.ErrorCode(e) != "login_url_timeout" {
		t.Fatal("repair kept waiting after terminal login failure", ready, e)
	}
}
func TestRecoveryRetryKeepsFailureWhenHealthIsStillBad(t *testing.T) {
	m, p := recoverySelection(t)
	old := cloneState(m.saved)
	if e := m.journal(applyJournal{Schema: 1, Phase: "recovery-required", Before: old, After: old}); e != nil {
		t.Fatal(e)
	}
	m.applyStatus = &connector.ApplyStatus{Phase: "recovery-required", Code: "login_url_timeout"}
	p.healthError = connector.Coded("local_check_failed", "synthetic failure")
	if _, e := m.Connect(context.Background(), connector.Request{Provider: "netbird"}); connector.ErrorCode(e) != "local_check_failed" {
		t.Fatal("unhealthy retry succeeded", e)
	}
	if j := readApplyJournal(t, m); j.Phase != "recovery-required" {
		t.Fatal("unhealthy retry discarded recovery snapshot")
	}
}
