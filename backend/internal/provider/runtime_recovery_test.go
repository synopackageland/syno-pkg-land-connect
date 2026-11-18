package provider

import (
	"context"
	"dsmapp/internal/connector"
	"dsmapp/internal/transient"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/supervision"
)

// Exercise the real restorer/selection Retry path without a network or upstream
// executable. The session owns the same runtime builder, monitor and cleanup.
type storageNetBirdSession struct {
	readySession
	storage *runtimeMonitor
	dir     string
	exited  chan struct{}
	once    sync.Once
}

func (s *storageNetBirdSession) Alive() bool {
	return !s.storage.failed.Load() && s.readySession.Alive()
}
func (s *storageNetBirdSession) LastExit() supervision.Exit {
	return (&netbirdExecSession{storage: s.storage}).LastExit()
}
func (s *storageNetBirdSession) exit() error {
	s.set(func(s *slowNetBirdSession) { s.exits = true })
	s.once.Do(func() { close(s.exited) })
	return nil
}
func (s *storageNetBirdSession) Stop() error {
	s.storage.Close()
	_ = s.exit()
	return transient.Clear(s.dir, "netbird")
}

type storageNetBirdDriver struct {
	mu      sync.Mutex
	dir     string
	session *storageNetBirdSession
	opens   int
}

func (d *storageNetBirdDriver) Open(context.Context) (NetBirdSession, error) {
	if err := os.MkdirAll(d.dir, 0700); err != nil {
		return nil, err
	}
	if _, err := netbirdRuntimeCommands(d.dir); err != nil {
		return nil, err
	}
	runtime, err := transient.For(d.dir, "netbird", false)
	if err != nil {
		return nil, err
	}
	s := &storageNetBirdSession{readySession: readySession{&slowNetBirdSession{snapshot: connectedSnapshot()}}, dir: d.dir, exited: make(chan struct{})}
	s.storage = watchRuntime(runtime, "netbird", time.Millisecond, s.exit, s.exited)
	d.mu.Lock()
	d.session, d.opens = s, d.opens+1
	d.mu.Unlock()
	return s, nil
}
func (d *storageNetBirdDriver) latest() (*storageNetBirdSession, int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.session, d.opens
}

func TestRuntimeOverflowStatusAndRetryClearPrivateStateAndReconnect(t *testing.T) {
	dir := t.TempDir()
	client := filepath.Join(dir, "client")
	identity := filepath.Join(client, "default.json")
	driver := &storageNetBirdDriver{dir: client}
	p := NewNetBird(dir, driver)
	management, err := netbirdManagementURL("https://api.netbird.io")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.selectIdentity(management); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(client, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(identity, []byte(`{"key":"retained-fixture"}`), 0600); err != nil {
		t.Fatal(err)
	}
	p.RetryDelay = time.Millisecond
	m := NewMultiSelection(t.TempDir(), p)
	t.Cleanup(func() { p.Stop() })
	if _, err := m.Connect(context.Background(), connector.Request{Provider: "netbird", ManagementURL: "https://api.netbird.io", ConfirmManagementPolicy: true}); err != nil {
		t.Fatal(err)
	}
	awaitState := func(want string) connector.Status {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			s := m.StatusFor("netbird")
			if s.State == want {
				return s
			}
			time.Sleep(time.Millisecond)
		}
		t.Fatal("missing provider state", want, m.StatusFor("netbird"))
		return connector.Status{}
	}
	awaitState("connected")
	runtime, err := transient.For(client, "netbird", false)
	if err != nil {
		t.Fatal(err)
	}
	// A stale part must be removed before the unreclaimable state fails.
	storageFile(t, runtime, ".123state.json", 4096, time.Hour)
	staged := t.TempDir()
	for i := 0; i < 9; i++ {
		storageFile(t, staged, string(rune('a'+i)), ProviderFileLimit, time.Hour)
	}
	if err := os.Rename(staged, filepath.Join(runtime, "required")); err != nil {
		t.Fatal(err)
	}
	s := awaitState("failed")
	if s.Code != "tmpfs_budget_exceeded" || s.Supervision == nil || s.Supervision.LastExit == nil || !s.Supervision.LastExit.Permanent {
		t.Fatal("UI lost actionable cause", s)
	}
	first, _ := driver.latest()
	select {
	case <-first.storage.done:
	default:
		t.Fatal("failed provider retained storage ticker")
	}
	if _, err := os.Stat(runtime); !os.IsNotExist(err) {
		t.Fatal("failed session retained tmpfs", err)
	}
	if m.NeedsPolling() {
		t.Fatal("failed terminal provider retained polling")
	}
	if _, err := m.Connect(context.Background(), connector.Request{Provider: "netbird", Retry: true}); err != nil {
		t.Fatal("one-click retry failed", err)
	}
	s = awaitState("connected")
	second, opens := driver.latest()
	if opens != 2 || second == first || s.Code != "" {
		t.Fatal("retry did not start a fresh healthy session", opens, s)
	}
	fresh, err := transient.For(client, "netbird", false)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(fresh)
	if err != nil || len(entries) != 0 {
		t.Fatal("retry reused overflow files", entries, err)
	}
	if raw, err := os.ReadFile(identity); err != nil || string(raw) != `{"key":"retained-fixture"}` {
		t.Fatal("retry lost saved identity", err)
	}
	if err := p.Stop(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-second.storage.done:
	default:
		t.Fatal("stop retained storage ticker")
	}
}
