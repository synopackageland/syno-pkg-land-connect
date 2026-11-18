package provider

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"dsmapp/internal/connector"
)

type safeFixture struct {
	wait bool
	fixtureRuntime
	checks, updates, rollbacks int
	reject, badReadback        bool
}

func (p *safeFixture) PrecheckUpdate(context.Context, Settings) error {
	p.checks++
	if p.reject {
		return errors.New("fixture precheck")
	}
	return nil
}
func (p *safeFixture) ApplyUpdate(context.Context, Settings) error { p.updates++; return nil }
func (p *safeFixture) VerifyUpdate(context.Context, Settings) (bool, error) {
	if p.wait {
		return false, nil
	}
	if p.badReadback {
		return false, errors.New("fixture readback")
	}
	return true, nil
}
func (p *safeFixture) RollbackUpdate(context.Context, Settings) error { p.rollbacks++; return nil }
func (*safeFixture) CheckHealth(context.Context) error                { return nil }
func safeSelection(t *testing.T) (*MultiSelection, *safeFixture) {
	t.Helper()
	p := &safeFixture{fixtureRuntime: fixtureRuntime{id: "cloudflare", events: &[]string{}}}
	m := NewMultiSelection(t.TempDir(), p)
	if _, e := m.Connect(context.Background(), connector.Request{Provider: "cloudflare", Mode: "domain", ConfirmExposure: true}); e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { m.Stop() })
	return m, p
}
func TestSafeUpdatePrecheckDoesNotChangeGateOrStopRuntime(t *testing.T) {
	m, p := safeSelection(t)
	path := filepath.Join(m.children["cloudflare"].dir, "provider.json")
	before, _ := os.ReadFile(path)
	p.reject = true
	_, e := m.Connect(context.Background(), connector.Request{Provider: "cloudflare", Mode: "domain", Subdomain: "other", ConfirmExposure: true})
	after, _ := os.ReadFile(path)
	if e == nil || !p.live || p.updates != 0 || !reflect.DeepEqual(before, after) {
		t.Fatal("precheck changed existing connection", e, p.updates)
	}
}
func TestSafeUpdateReadbackFailureRollsBackBeforeCommitting(t *testing.T) {
	m, p := safeSelection(t)
	p.badReadback = true
	child := m.children["cloudflare"]
	path := filepath.Join(child.dir, "provider.json")
	before, _ := os.ReadFile(path)
	if _, e := m.Connect(context.Background(), connector.Request{Provider: "cloudflare", Mode: "domain", Subdomain: "other", ConfirmExposure: true}); !connector.IsPreserved(e) {
		t.Fatal("readback failure must preserve original connection", e)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		s := child.Status()
		if s.Apply != nil && s.Apply.Phase == "rolled-back" {
			break
		}
		time.Sleep(time.Millisecond)
	}
	child.mu.Lock()
	defer child.mu.Unlock()
	after, _ := os.ReadFile(path)
	if p.rollbacks != 1 || !p.live || !reflect.DeepEqual(before, after) {
		t.Fatal("failed candidate replaced accepted settings", p.rollbacks)
	}
}

func TestHealthyWatchdogDoesNotReadOrWriteThePersistentStore(t *testing.T) {
	m, p := safeSelection(t)
	child := m.children["cloudflare"]
	dir := child.dir
	if e := os.Rename(dir, dir+"-offline"); e != nil {
		t.Fatal(e)
	}
	defer os.Rename(dir+"-offline", dir)
	for i := 0; i < 5; i++ {
		child.healthTick(context.Background())
	}
	if _, e := os.Stat(dir); !os.IsNotExist(e) {
		t.Fatal("idle health observation recreated persistent state")
	}
	if p.updates != 0 || p.rollbacks != 0 {
		t.Fatal("healthy observation changed runtime")
	}
}
func TestDisconnectCancelsPendingApplyAndNeverRestoresItsGate(t *testing.T) {
	m, p := safeSelection(t)
	child := m.children["cloudflare"]
	p.wait = true
	if _, e := m.Connect(context.Background(), connector.Request{Provider: "cloudflare", Mode: "domain", Subdomain: "other", ConfirmExposure: true}); e != nil {
		t.Fatal(e)
	}
	child.mu.Lock()
	tx := child.pending
	child.mu.Unlock()
	if tx == nil {
		t.Fatal("expected accepted pending update")
	}
	if _, e := m.DisconnectProvider("cloudflare"); e != nil {
		t.Fatal(e)
	}
	select {
	case <-tx.done:
	case <-time.After(time.Second):
		t.Fatal("pending worker outlived cancellation")
	}
	gate, e := readGate(filepath.Join(child.dir, "provider.json"))
	if e != nil || gate.Enabled || p.live || p.rollbacks != 0 {
		t.Fatal("revoked connection was restored", e)
	}
}
func TestApplyJournalContainsNoSubmittedCredentials(t *testing.T) {
	m, _ := safeSelection(t)
	key := "SYNTHETIC-DO-NOT-PERSIST"
	if _, e := m.Connect(context.Background(), connector.Request{Provider: "cloudflare", Mode: "domain", Token: key, Subdomain: "other", ConfirmExposure: true}); e != nil {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(filepath.Join(m.children["cloudflare"].dir, "apply.json"))
	if e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(raw, []byte(key)) {
		t.Fatal("submitted credential entered generic journal")
	}
}

func TestTailscaleHealthObservationCannotPersistARefusalOrStopTheDaemon(t *testing.T) {
	session := &tsSession{snapshot: TailscaleSnapshot{BackendState: "Running", IPs: []string{"100.64.0.1"}, DNSName: "nas.fixture.ts.net", Serving: true}}
	dir := t.TempDir()
	p := NewTailscale(dir, &tsDriver{session: session})
	if _, e := p.Start(context.Background(), tsSettings()); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(dir, "settings.json")
	before, _ := os.ReadFile(path)
	session.inspectErr = connector.Coded("provider_status_invalid", "synthetic invalid socket response")
	_ = p.CheckHealth(context.Background())
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) || session.stopped {
		t.Fatal("read-only health probe wrote a failed gate or stopped the daemon")
	}
}

func TestOldApplyJournalCannotResurrectAWithdrawnMapping(t *testing.T) {
	m, p := safeSelection(t)
	child := m.children["cloudflare"]
	before := cloneState(child.saved)
	after := cloneState(before)
	after.Settings.Subdomain = "other"
	if e := child.journal(applyJournal{Schema: 1, Phase: "applying", Before: before, After: after}); e != nil {
		t.Fatal(e)
	}
	revoked := cloneState(before)
	revoked.Settings.Mode = "token"
	if e := child.persist(revoked); e != nil {
		t.Fatal(e)
	}
	fresh := NewSelection(child.dir, p)
	if e := fresh.Restore(context.Background()); e != nil {
		t.Fatal(e)
	}
	if fresh.saved.Settings.Mode != "token" {
		t.Fatal("stale journal replayed revoked configuration")
	}
	if _, e := os.Stat(filepath.Join(child.dir, "apply.json")); !os.IsNotExist(e) {
		t.Fatal("superseded journal was not retired", e)
	}
}

// Versions with Cloudflare TCP services saved "tcpPorts" in the gate and the
// apply journal. Both still restore, without them, instead of failing.
func TestOldTCPPortsSettingRestoresWithoutThem(t *testing.T) {
	m, p := safeSelection(t)
	child := m.children["cloudflare"]
	path := filepath.Join(child.dir, "provider.json")
	raw, _ := os.ReadFile(path)
	legacy := bytes.Replace(raw, []byte(`"mode":"domain"`), []byte(`"mode":"domain","tcpPorts":[6690,22]`), 1)
	if bytes.Equal(raw, legacy) {
		t.Fatalf("fixture gate %s", raw)
	}
	if e := os.WriteFile(path, legacy, 0600); e != nil {
		t.Fatal(e)
	}
	journal := `{"schema":1,"phase":"committed","before":` + string(legacy) + `,"after":` + string(legacy) + `}`
	if e := os.WriteFile(filepath.Join(child.dir, "apply.json"), []byte(journal), 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := readGate(path); e != nil {
		t.Fatal("old gate refused", e)
	}
	fresh := NewSelection(child.dir, p)
	if e := fresh.Restore(context.Background()); e != nil {
		t.Fatal(e)
	}
	if s := fresh.Status(); s.State == "failed" || !fresh.saved.Enabled || fresh.saved.Settings.Mode != "domain" {
		t.Fatalf("old gate did not restore: %+v", s)
	}
}
