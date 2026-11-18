package provider

import (
	"bytes"
	"context"
	"dsmapp/internal/cloudflare"
	"dsmapp/internal/connector"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type selectionState struct {
	Provider     string         `json:"provider"`
	Enabled      bool           `json:"enabled"`
	Settings     *Settings      `json:"settings,omitempty"`
	PortBindings map[string]int `json:"portBindings"`
}

// Selection serializes lifecycle and records a disabled gate before any switch.
// A failed stop never starts the replacement. Unknown/unwired IDs fail closed.
type Selection struct {
	mu                             sync.Mutex
	dir                            string
	registry                       map[string]Runtime
	saved                          selectionState
	failed                         bool
	generation                     uint64
	ports                          PortSource
	portEpoch                      uint64
	portBlocked                    bool
	pending                        *pendingApply
	applyStatus                    *connector.ApplyStatus
	applyChanges                   connector.ApplyStatus
	lastGood                       selectionState
	healthFailures, repairAttempts int
	nextRepair, healthySince       time.Time
	settleUntil                    time.Time // health failures before it are not counted
	recovering                     *applyJournal
	pressure                       func() bool
	// enabled mirrors saved.Enabled for readers that must not wait for a
	// connect holding mu.
	enabled atomic.Bool
}

func NewSelection(dir string, runtimes ...Runtime) *Selection {
	m := &Selection{dir: dir, registry: map[string]Runtime{"cloudflare": nil, "tailscale": nil, "ipv6": nil, "netbird": nil}, saved: selectionState{Provider: "cloudflare"}}
	for _, p := range runtimes {
		if p != nil {
			m.registry[p.ID()] = p
		}
	}
	return m
}
func (m *Selection) persist(s selectionState) error {
	if s.Settings != nil {
		copy := *s.Settings
		copy.Token = ""
		copy.Mappings = append([]PortMapping(nil), s.Settings.Mappings...)
		s.Settings = &copy
	}
	raw, _ := json.Marshal(s)
	if err := connector.WritePrivate(filepath.Join(m.dir, "provider.json"), raw); err != nil {
		return errors.New("provider settings unavailable")
	}
	m.setSaved(s)
	return nil
}

// setSaved replaces the gate; the caller holds mu.
func (m *Selection) setSaved(s selectionState) {
	m.saved = s
	m.enabled.Store(s.Enabled)
}
func (m *Selection) Connect(ctx context.Context, r connector.Request) (connector.Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := r.Provider
	if id == "" {
		id = "cloudflare"
	}
	defer func() {
		if m.saved.Enabled && m.saved.Provider == id && !m.failed {
			m.beginSettling()
		}
	}()
	if id != "ipv6" && r.ServiceIDs != nil {
		return m.status(), ErrUnavailable
	}
	if id != "cloudflare" && (r.ReuseName != nil || r.Mode != "" || r.Subdomain != "") {
		return m.status(), ErrUnavailable
	}
	p := m.registry[id]
	if p == nil {
		return m.status(), ErrUnavailable
	}
	if m.pending != nil {
		return m.status(), connector.Preserved(errApplyBusy)
	}
	if r.Retry {
		if !connector.RetryOnly(r) || !m.saved.Enabled || m.saved.Provider != id {
			return m.status(), ErrUnavailable
		}
		err := p.Restore(ctx)
		m.failed = err != nil
		if err == nil {
			m.applyStatus = nil
		}
		return m.status(), err
	}
	if id != "tailscale" && (r.LoginServer != "" || r.LoginOrigin != "") {
		return m.status(), ErrUnavailable
	}
	if id != "netbird" && (r.ManagementURL != "" || r.ConfirmManagementPolicy || len(r.LoginOrigins) != 0) {
		return m.status(), ErrUnavailable
	}
	s := Settings{LoginOrigins: r.LoginOrigins, LoginServer: r.LoginServer, LoginOrigin: r.LoginOrigin, ManagementURL: r.ManagementURL, ConfirmManagementPolicy: r.ConfirmManagementPolicy, ServiceIDs: r.ServiceIDs, ReuseName: r.ReuseName, Mode: r.Mode, Subdomain: r.Subdomain, Token: r.Token, Hostname: r.Hostname, Mappings: r.Mappings, ConfirmExposure: r.ConfirmExposure}
	if id != "netbird" && id != "tailscale" && !s.ConfirmExposure {
		return m.status(), errors.New("administrator exposure confirmation required")
	}
	if id == "ipv6" && m.ports == nil {
		return m.status(), ErrUnavailable
	}
	// Identical clicks join the provider's active login attempt. A changed
	// configuration or an explicit disconnect still replaces/stops that attempt.
	if id == "tailscale" && s.Token == "" && m.saved.Enabled && m.saved.Provider == id && m.saved.Settings != nil {
		proposed := s
		proposed.Token = ""
		saved := *m.saved.Settings
		saved.Token = ""
		left, _ := json.Marshal(proposed)
		right, _ := json.Marshal(saved)
		if bytes.Equal(left, right) {
			state := p.Status()
			if state.State == "needs-login" || state.State == "needs-machine-auth" || state.State == "connecting" {
				return m.status(), nil
			}
		}
	}
	// Diagnose missing Tailscale binaries before port resolution, while still
	// validating the request before any preflight or lifecycle effect.
	if id == "tailscale" || id == "netbird" {
		if v, ok := p.(interface{ Validate(Settings) error }); ok {
			if err := v.Validate(s); err != nil {
				return m.status(), err
			}
		}
	}
	if reusable, ok := p.(interface{ ReuseAttempt(Settings) bool }); ok && reusable.ReuseAttempt(s) {
		return m.status(), nil
	}
	if ready, ok := p.(interface{ Preflight(context.Context) error }); ok {
		if err := ready.Preflight(ctx); err != nil {
			return m.status(), err
		}
	}
	var bindings map[string]int
	if m.ports != nil && id == "ipv6" {
		var err error
		bindings, err = m.bindPorts(ctx, &s)
		if err != nil {
			return m.status(), err
		}
	}
	var mappingError error
	if id == "ipv6" && m.saved.Enabled {
		mappingError = validateIPv6Mappings(s.Mappings)
	} else {
		mappingError = p.AcceptMappings(s.Mappings)
	}
	if err := mappingError; err != nil {
		return m.status(), err
	}
	if v, ok := p.(interface{ Validate(Settings) error }); ok {
		if err := v.Validate(s); err != nil {
			return m.status(), err
		}
	}
	if ready, ok := p.(interface {
		PreflightSettings(context.Context, Settings) error
	}); ok {
		if err := ready.PreflightSettings(ctx, s); err != nil {
			return m.status(), err
		}
	}
	// Updating this domain must reach its in-place authorization path while
	// the old connector is still running. Disable is an explicit revocation,
	// not a prerequisite for changing the same connection, unless the
	// administrator accepted the interruption (Replace).
	if m.saved.Enabled && m.saved.Provider == id && p.Status().State == "connected" && !r.Replace {
		return m.safeUpdate(ctx, p, s, bindings)
	}
	// Keep the old ID until cleanup completes so failures can be retried.
	if err := m.persist(selectionState{Provider: m.saved.Provider, Settings: m.saved.Settings, PortBindings: m.saved.PortBindings}); err != nil {
		return m.status(), err
	}
	if err := m.clearApply(); err != nil {
		return m.status(), err
	}
	m.lastGood = selectionState{}
	if old := m.registry[m.saved.Provider]; old != nil {
		stop := old.Disable
		if keep, ok := old.(interface{ PreserveOnReconnect(Settings) bool }); ok && m.saved.Provider == id && keep.PreserveOnReconnect(s) {
			stop = old.Stop
		}
		if err := stop(); err != nil {
			m.failed = true
			return m.status(), err
		}
	}
	m.generation++
	m.portEpoch++
	if err := m.persist(selectionState{Provider: id, Settings: &s, PortBindings: bindings}); err != nil {
		return m.status(), err
	}
	m.failed = false
	m.portBlocked = false
	if _, err := p.Start(ctx, s); err != nil {
		m.failed = true
		_ = p.Disable()
		return m.status(), err
	}
	if err := m.persist(selectionState{Provider: id, Enabled: true, Settings: &s, PortBindings: bindings}); err != nil {
		m.failed = true
		_ = p.Disable()
		return m.status(), err
	}
	return m.status(), nil
}
func (m *Selection) Restore(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	raw, err := connector.ReadPrivate(filepath.Join(m.dir, "provider.json"))
	if os.IsNotExist(err) {
		return nil
	}
	if err == nil {
		var s selectionState
		d := json.NewDecoder(bytes.NewReader(withoutTCPPorts(raw)))
		d.DisallowUnknownFields()
		var extra any
		if d.Decode(&s) != nil || d.Decode(&extra) != io.EOF || m.registry[s.Provider] == nil || (s.Settings != nil && s.Settings.Token != "") {
			err = errors.New("invalid provider settings")
		} else {
			m.setSaved(s)
		}
	}
	if err != nil {
		m.failed = true
		return errors.New("invalid provider settings")
	}
	if !m.saved.Enabled {
		return nil
	}
	m.beginSettling()
	if err = m.recoverApply(); err != nil {
		m.failed = true
		m.applyStatus = &connector.ApplyStatus{Phase: "recovery-required", Code: applyCode(err)}
		return err
	}
	m.portEpoch++
	if m.ports != nil && m.saved.Provider == "ipv6" {
		err = m.restorePorts(ctx, m.registry[m.saved.Provider])
	} else {
		err = m.registry[m.saved.Provider].Restore(ctx)
	}
	// A provider that reports its own failure, such as Cloudflare waiting for
	// the DSM certificate review, also reports its recovery; never mask it.
	m.failed = err != nil && m.registry[m.saved.Provider].Status().State != "failed"
	return err
}
func (m *Selection) Disconnect() (connector.Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p := m.registry[m.saved.Provider]; p != nil {
		if guard, ok := p.(interface{ BeforeDisable() error }); ok {
			if err := guard.BeforeDisable(); err != nil {
				return m.status(), err
			}
		}
	}
	m.cancelApply()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := m.persist(selectionState{Provider: m.saved.Provider, Settings: m.keptSettings(ctx), PortBindings: m.saved.PortBindings}); err != nil {
		return m.status(), err
	}
	m.generation++
	m.portEpoch++
	if p := m.registry[m.saved.Provider]; p != nil {
		if err := p.Disable(); err != nil {
			m.failed = true
			return m.status(), err
		}
		// Its own settings list the same services the gate shows.
		if m.saved.Settings != nil {
			var err error
			switch p := p.(type) {
			case *IPv6Runtime:
				err = p.KeepMappings(m.saved.Settings.Mappings)
			}
			if err != nil {
				return m.status(), err
			}
		}
	}
	m.failed = false
	m.portBlocked = false
	return m.status(), nil
}

// ErrConnectionEnabled refuses to forget the services of a live connection.
var ErrConnectionEnabled = connector.Coded("connection_enabled", "disconnect before forgetting the saved services")

// Forget clears the services a disabled connection kept for display and the
// next start, gate first. Only runtimes that keep their own copy accept it.
func (m *Selection) Forget() (connector.Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.registry[m.saved.Provider].(interface{ Forget() error })
	if !ok {
		return m.status(), ErrUnavailable
	}
	if m.saved.Enabled {
		return m.status(), ErrConnectionEnabled
	}
	next := m.saved
	next.PortBindings = nil
	if next.Settings != nil {
		s := *next.Settings
		s.Mappings = nil
		next.Settings = &s
	}
	if err := m.persist(next); err != nil {
		return m.status(), err
	}
	return m.status(), p.Forget()
}
func (m *Selection) Stop() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancelApply()
	if p := m.registry[m.saved.Provider]; p != nil {
		return p.Stop()
	}
	return nil
}
func (m *Selection) Status() connector.Status { m.mu.Lock(); defer m.mu.Unlock(); return m.status() }
func (m *Selection) status() connector.Status {
	s := connector.Status{Provider: m.saved.Provider, State: "disconnected"}
	if p := m.registry[m.saved.Provider]; p != nil {
		s = p.Status()
	}
	if settings := m.saved.Settings; settings != nil && !m.saved.Enabled {
		if settings.Hostname != "" {
			s.Hostname = settings.Hostname
		}
		s.Mappings = append([]PortMapping(nil), settings.Mappings...)
	}
	if m.failed {
		s.State = "failed"
		s.ShareURL = ""
		s.Endpoints = nil
	}
	if m.applyStatus != nil {
		info := *m.applyStatus
		info.AddedPorts = append([]int(nil), m.applyChanges.AddedPorts...)
		info.RemovedPorts = append([]int(nil), m.applyChanges.RemovedPorts...)
		s.Apply = &info
	}
	for id := range m.saved.PortBindings {
		s.ServiceIDs = append(s.ServiceIDs, id)
	}
	sort.Strings(s.ServiceIDs)
	s.Generation += m.generation
	return s
}

// RevokeOffline runs after connectd stopped or disconnected (the caller removes
// only an abandoned socket under the daemon lock). It never restores a daemon
// or starts a process during uninstall.
func RevokeOffline(dir string) error {
	return revokeOffline(dir, false, false)
}

// RetainOffline preserves the saved own domain on a data-retaining uninstall.
// Explicit connection disconnect and forget keep their existing semantics.
func RetainOffline(dir string) error {
	keep, err := cloudflare.RetainIntent(filepath.Join(dir, "cloudflare"))
	if err != nil {
		return err
	}
	// Migrate a legacy gate before creating the completion marker below.
	if keep {
		if err := NewMultiSelection(dir).initialize(); err != nil {
			return err
		}
	}
	return revokeOffline(dir, keep, true)
}

func revokeOffline(dir string, keepCloudflare, retainedData bool) error {
	// Phases keep the gate-first order; steps within a phase touch separate
	// files, so their fsyncs overlap on a loaded NAS instead of adding up.
	gates := make([]func() error, 0, len(connectionIDs))
	for _, id := range connectionIDs {
		if keepCloudflare && id == "cloudflare" {
			continue
		}
		gates = append(gates, func() error {
			child := filepath.Join(dir, "connections", id)
			if err := os.MkdirAll(child, 0700); err != nil {
				return err
			}
			raw, _ := json.Marshal(selectionState{Provider: id})
			return connector.WritePrivate(filepath.Join(child, "provider.json"), raw)
		})
	}
	if err := concurrently(gates...); err != nil {
		return err
	}
	if err := concurrently(
		func() error { return writeConnectionsMarker(dir) },
		func() error {
			raw, _ := json.Marshal(selectionState{Provider: "cloudflare"})
			return connector.WritePrivate(filepath.Join(dir, "provider.json"), raw)
		},
	); err != nil {
		return err
	}
	return concurrently(
		func() error {
			// A sign-in cut off by a crash can leave its one-time auth key.
			if e := os.Remove(filepath.Join(dir, "tailscale", "auth-key")); e != nil && !os.IsNotExist(e) {
				return e
			}
			return NewTailscale(filepath.Join(dir, "tailscale"), nil).Disable()
		},
		func() error {
			if retainedData {
				_, err := cloudflare.Retain(filepath.Join(dir, "cloudflare"))
				return err
			}
			return cloudflare.Revoke(filepath.Join(dir, "cloudflare"))
		},
		func() error { return NewNetBird(filepath.Join(dir, "netbird"), nil).Disable() },
		func() error { return NewIPv6Runtime(dir, nil).Disable() },
	)
}

// concurrently runs independent steps at once and reports every failure.
func concurrently(steps ...func() error) error {
	errs := make([]error, len(steps))
	var wg sync.WaitGroup
	for i, step := range steps {
		wg.Add(1)
		go func() { defer wg.Done(); errs[i] = step() }()
	}
	wg.Wait()
	return errors.Join(errs...)
}

// withoutTCPPorts drops the "tcpPorts" setting older versions saved for
// Cloudflare TCP services, so their state still passes a strict read and
// starts without them. Anything without the setting is returned unchanged.
func withoutTCPPorts(raw []byte) []byte {
	var v any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&v) != nil || !dropKey(v, "tcpPorts") {
		return raw
	}
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return out
}

func dropKey(v any, key string) bool {
	dropped := false
	switch v := v.(type) {
	case map[string]any:
		if _, ok := v[key]; ok {
			delete(v, key)
			dropped = true
		}
		for _, child := range v {
			dropped = dropKey(child, key) || dropped
		}
	case []any:
		for _, child := range v {
			dropped = dropKey(child, key) || dropped
		}
	}
	return dropped
}
