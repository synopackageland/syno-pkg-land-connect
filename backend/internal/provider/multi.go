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
	"strings"
	"sync"
)

var connectionIDs = []string{"cloudflare", "tailscale", "ipv6", "netbird"}

// MultiSelection owns independent durable gates. Runtime state stays in its
// existing private directories; lifecycle, port changes and updates share a lock.
type MultiSelection struct {
	mu          sync.Mutex
	dir         string
	children    map[string]*Selection
	initialized bool
	failed      bool
	changed     func()
}

// OnChange installs f, called after any change that can turn a connection on
// or off, once the selection lock is released.
func (m *MultiSelection) OnChange(f func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.changed = f
}

func (m *MultiSelection) notify() {
	m.mu.Lock()
	f := m.changed
	m.mu.Unlock()
	if f != nil {
		f()
	}
}

// Enabled reports whether the administrator has any connection turned on. It
// never waits for a connection that is still starting.
func (m *MultiSelection) Enabled() bool {
	for _, id := range connectionIDs {
		if m.children[id].enabled.Load() {
			return true
		}
	}
	return false
}

func NewMultiSelection(dir string, runtimes ...Runtime) *MultiSelection {
	m := &MultiSelection{dir: dir, children: map[string]*Selection{}}
	for _, id := range connectionIDs {
		var owned []Runtime
		for _, r := range runtimes {
			if r != nil && r.ID() == id {
				owned = append(owned, r)
			}
		}
		child := NewSelection(filepath.Join(dir, "connections", id), owned...)
		child.saved.Provider = id
		m.children[id] = child
	}
	return m
}
func readGate(path string) (selectionState, error) {
	var s selectionState
	raw, err := connector.ReadPrivate(path)
	if err != nil {
		return s, err
	}
	d := json.NewDecoder(bytes.NewReader(withoutTCPPorts(raw)))
	d.DisallowUnknownFields()
	var extra any
	if d.Decode(&s) != nil || d.Decode(&extra) != io.EOF || (s.Provider != "cloudflare" && s.Provider != "tailscale" && s.Provider != "ipv6" && s.Provider != "netbird") || (s.Settings != nil && s.Settings.Token != "") {
		return s, errors.New("invalid provider settings")
	}
	return s, nil
}
func writeConnectionsMarker(dir string) error {
	return connector.WritePrivate(filepath.Join(dir, "connections.json"), []byte(`{"schema":1}`))
}
func (m *MultiSelection) initialize() error {
	if m.initialized {
		return nil
	}
	raw, err := connector.ReadPrivate(filepath.Join(m.dir, "connections.json"))
	if err == nil {
		if string(raw) != `{"schema":1}` {
			return errors.New("invalid connections settings")
		}
		// Schema 1 predates NetBird. A missing new gate starts disabled.
		nb := filepath.Join(m.dir, "connections", "netbird", "provider.json")
		if _, e := os.Lstat(nb); os.IsNotExist(e) {
			if e = os.MkdirAll(filepath.Dir(nb), 0700); e != nil {
				return e
			}
			if e = connector.WritePrivate(nb, []byte(`{"provider":"netbird","enabled":false}`)); e != nil {
				return e
			}
		} else if e != nil {
			return e
		}
		m.initialized = true
		return nil
	}
	if !os.IsNotExist(err) {
		return err
	}
	// Stage the legacy gate before withdrawing it. A crash during migration can
	// resume from this public-settings snapshot, never from credentials.
	stage := filepath.Join(m.dir, "connections-migration.json")
	legacy, err := readGate(stage)
	if os.IsNotExist(err) {
		legacy, err = readGate(filepath.Join(m.dir, "provider.json"))
		if os.IsNotExist(err) {
			legacy = selectionState{Provider: "cloudflare"}
			err = nil
		}
		if err != nil {
			return err
		}
		raw, _ = json.Marshal(legacy)
		if err = connector.WritePrivate(stage, raw); err != nil {
			return err
		}
	}
	if err != nil {
		return err
	}
	// Legacy IPv6 enabled all then-current services. Freeze that accepted scope
	// to stable IDs; newly discovered services must not be exposed on upgrade.
	if legacy.Provider == "ipv6" && legacy.PortBindings == nil {
		legacy.PortBindings = map[string]int{}
		source := m.children["ipv6"].ports
		if source != nil && legacy.Settings != nil {
			resolved, e := source.ResolvePorts(context.Background())
			if e != nil {
				return e
			}
			for _, r := range resolved {
				for _, old := range legacy.Settings.Mappings {
					if old.LocalPort == r.Mapping.LocalPort && old.Protocol == r.Mapping.Protocol {
						legacy.PortBindings[r.ID] = r.Mapping.PublicPort
					}
				}
			}
		}
	}
	// All files precede the completion marker. No child starts in this phase.
	for _, id := range connectionIDs {
		child := m.children[id]
		if err = os.MkdirAll(child.dir, 0700); err != nil {
			return err
		}
		s := selectionState{Provider: id}
		if legacy.Provider == id {
			s = legacy
		}
		if err = child.persist(s); err != nil {
			return err
		}
	}
	legacy.Enabled = false
	raw, _ = json.Marshal(legacy)
	if err = connector.WritePrivate(filepath.Join(m.dir, "provider.json"), raw); err != nil {
		return err
	}
	if err = writeConnectionsMarker(m.dir); err != nil {
		return err
	}
	m.initialized = true
	return nil
}
func (m *MultiSelection) Restore(ctx context.Context) error {
	defer m.notify()
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.initialize(); err != nil {
		m.failed = true
		return err
	}
	var errs []error
	for _, id := range connectionIDs {
		child := m.children[id]
		if child.registry[id] == nil {
			s, e := readGate(filepath.Join(child.dir, "provider.json"))
			if e != nil || s.Enabled {
				child.failed = true
				errs = append(errs, ErrUnavailable)
			} else {
				child.setSaved(s)
			}
			continue
		}
		if e := child.Restore(ctx); e != nil {
			errs = append(errs, e)
		}
	}
	return errors.Join(errs...)
}
func (m *MultiSelection) status() connector.Status {
	s := connector.Status{Provider: "multiple", State: "disconnected"}
	for _, id := range connectionIDs {
		c := m.children[id].Status()
		s.Connections = append(s.Connections, c)
		if c.State != "disconnected" {
			s.State = c.State
		}
	}
	if m.failed {
		s.State = "failed"
	}
	return s
}
func (m *MultiSelection) Status() connector.Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status()
}
func (m *MultiSelection) StatusFor(id string) connector.Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p := m.children[id]; p != nil {
		return p.Status()
	}
	return connector.Status{Provider: id, State: "failed"}
}
func (m *MultiSelection) Connect(ctx context.Context, r connector.Request) (connector.Status, error) {
	defer m.notify()
	m.mu.Lock()
	defer m.mu.Unlock()
	if r.Provider == "" {
		r.Provider = "cloudflare"
	}
	p := m.children[r.Provider]
	if p == nil {
		return m.status(), ErrUnavailable
	}
	if err := m.initialize(); err != nil {
		m.failed = true
		return m.status(), err
	}
	// Load public intent before editing a connection not yet observed after boot.
	if p.saved.Settings == nil {
		if s, e := readGate(filepath.Join(p.dir, "provider.json")); e == nil {
			p.setSaved(s)
		}
	}
	s, err := p.Connect(ctx, r)
	return s, err
}
func (m *MultiSelection) DisconnectProvider(id string) (connector.Status, error) {
	defer m.notify()
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.children[id]
	if p == nil {
		return m.status(), ErrUnavailable
	}
	if err := m.initialize(); err != nil {
		m.failed = true
		return m.status(), err
	}
	return p.Disconnect()
}
func (m *MultiSelection) Forget(id string) (connector.Status, error) {
	defer m.notify()
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.children[id]
	if p == nil {
		return m.status(), ErrUnavailable
	}
	if err := m.initialize(); err != nil {
		m.failed = true
		return m.status(), err
	}
	if _, err := p.Forget(); err != nil {
		return m.status(), err
	}
	return m.status(), nil
}
func (m *MultiSelection) Disconnect() (connector.Status, error) {
	defer m.notify()
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.initialize(); err != nil {
		m.failed = true
		return m.status(), err
	}
	// Refuse before any other connection is withdrawn.
	for _, child := range m.children {
		if p := child.registry[child.saved.Provider]; p != nil {
			if guard, ok := p.(interface{ BeforeDisable() error }); ok {
				if e := guard.BeforeDisable(); e != nil {
					return m.status(), e
				}
			}
		}
	}
	var errs []error
	for _, id := range connectionIDs {
		if _, e := m.children[id].Disconnect(); e != nil {
			errs = append(errs, e)
		}
	}
	return m.status(), errors.Join(errs...)
}
func (m *MultiSelection) Stop() error {
	defer m.notify()
	m.mu.Lock()
	defer m.mu.Unlock()
	var errs []error
	for _, id := range connectionIDs {
		if e := m.children[id].Stop(); e != nil {
			errs = append(errs, e)
		}
	}
	return errors.Join(errs...)
}
func (m *MultiSelection) SetPortSource(s PortSource) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range connectionIDs {
		m.children[id].SetPortSource(s)
	}
}
func (m *MultiSelection) PortEpoch() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var keys []string
	for _, id := range connectionIDs {
		keys = append(keys, m.children[id].PortEpoch())
	}
	return strings.Join(keys, "|")
}
func (m *MultiSelection) ReplacePorts(ctx context.Context, p []ResolvedPort) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var errs []error
	active := false
	for _, id := range connectionIDs {
		e := m.children[id].ReplacePorts(ctx, p)
		if errors.Is(e, ErrPortsInactive) {
			continue
		}
		active = true
		if e != nil {
			errs = append(errs, e)
		}
	}
	if !active {
		return ErrPortsInactive
	}
	return errors.Join(errs...)
}
func (m *MultiSelection) WithdrawPorts(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	var errs []error
	for _, id := range connectionIDs {
		if e := m.children[id].WithdrawPorts(ctx); e != nil {
			errs = append(errs, e)
		}
	}
	return errors.Join(errs...)
}
func (m *MultiSelection) UpdateBinaries(ctx context.Context, id string, prepare func() (func() error, func() error, error)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p := m.children[id]; p != nil {
		return p.UpdateBinaries(ctx, id, prepare)
	}
	return ErrUnavailable
}

// DependencyChanged is an event trigger, never a health poll. It re-arms only
// failed enabled children with a transient last exit; running children stay put.
func (m *MultiSelection) DependencyChanged(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range connectionIDs {
		child := m.children[id]
		child.mu.Lock()
		runtime := child.registry[id]
		if child.saved.Enabled && runtime != nil && child.pending == nil {
			status := runtime.Status()
			s := status.Supervision
			if s != nil && s.State == "failed" && (s.LastExit == nil || !s.LastExit.Permanent) {
				_ = runtime.Restore(ctx)
			}
		}
		child.mu.Unlock()
	}
}

// NeedsPolling excludes terminal child series. Its caller keeps a separate
// event-only network watcher while saved enabled intent exists.
func (m *MultiSelection) NeedsPolling() bool {
	for _, id := range connectionIDs {
		child := m.children[id]
		if !child.enabled.Load() {
			continue
		}
		p := child.registry[id]
		if r, ok := p.(interface{ RecoveryTerminal() bool }); ok && r.RecoveryTerminal() {
			continue
		}
		return true
	}
	return false
}
