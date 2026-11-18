package provider

import (
	"context"
	"dsmapp/internal/connector"
	"errors"

	"strconv"
	"strings"
)

// ResolvedPort is package-owned resolution metadata, separate from the stable
// Runtime/PortMapping contract. Browser requests may select stable IDs, but
// only this trusted source may resolve their ports, protocols and targets.
type ResolvedPort struct {
	ID      string
	Mapping PortMapping
}
type PortSource interface {
	ResolvePorts(context.Context) ([]ResolvedPort, error)
}

var ErrPortsInactive = errors.New("selected provider does not own port mappings")

// SetPortSource installs a trusted dependency before the daemon accepts requests.
func (m *Selection) SetPortSource(s PortSource) { m.mu.Lock(); defer m.mu.Unlock(); m.ports = s }
func (m *Selection) PortEpoch() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saved.Provider + ":" + strconv.FormatBool(m.saved.Enabled) + ":" + strconv.FormatUint(m.portEpoch, 10)
}
func (m *Selection) resolvePorts(ctx context.Context) ([]ResolvedPort, error) {
	if m.ports == nil {
		return nil, errors.New("trusted resolved ports unavailable")
	}
	resolved, e := m.ports.ResolvePorts(ctx)
	if e != nil {
		return nil, e
	}
	ids := map[string]bool{}
	local := map[int]bool{}
	for _, r := range resolved {
		p := r.Mapping
		if r.ID == "" || ids[r.ID] || local[p.LocalPort] || p.LocalPort < 1 || p.LocalPort > 65535 || p.PublicPort != p.LocalPort || (p.Protocol != "http" && p.Protocol != "https" && p.Protocol != "tcp") || p.Socket != "" {
			return nil, errors.New("invalid resolved ports")
		}
		ids[r.ID] = true
		local[p.LocalPort] = true
	}
	return resolved, ctx.Err()
}

// bindPorts gives IPv6 the trusted resolved ports the request selects.
func (m *Selection) bindPorts(ctx context.Context, s *Settings) (map[string]int, error) {
	resolved, e := m.resolvePorts(ctx)
	if e != nil {
		return nil, e
	}
	if len(s.Mappings) != 0 || len(resolved) == 0 {
		return nil, errors.New("IPv6 accepts trusted resolved ports only")
	}
	bindings := map[string]int{}
	selected := map[string]bool{}
	if s.ServiceIDs != nil {
		if len(s.ServiceIDs) == 0 || len(s.ServiceIDs) > 16 {
			return nil, errors.New("select a service")
		}
		for _, id := range s.ServiceIDs {
			if id == "" || selected[id] {
				return nil, errors.New("invalid service selection")
			}
			selected[id] = true
		}
	}
	for _, r := range resolved {
		if strings.HasPrefix(r.ID, "detected-") {
			continue
		}
		if s.ServiceIDs == nil || selected[r.ID] {
			s.Mappings = append(s.Mappings, r.Mapping)
			bindings[r.ID] = r.Mapping.PublicPort
			delete(selected, r.ID)
		}
	}
	if len(selected) != 0 {
		return nil, errors.New("service no longer available")
	}
	return bindings, nil
}
func resolvedMappings(id string, bindings map[string]int, resolved []ResolvedPort) []PortMapping {
	out := make([]PortMapping, 0, len(resolved))
	for _, r := range resolved {
		p := r.Mapping
		if strings.HasPrefix(r.ID, "detected-") {
			continue
		}
		if id == "ipv6" && bindings != nil {
			if _, ok := bindings[r.ID]; !ok {
				continue
			}
		}
		out = append(out, p)
	}
	return out
}

// ReplacePorts is the tracker owner callback. Selection, binary activation and
// runtime mutation share this lock. A stale plan never writes a new target.
func (m *Selection) ReplacePorts(ctx context.Context, expected []ResolvedPort) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	m.forgetDeletedServices(ctx)
	if m.saved.Provider != "ipv6" || !m.saved.Enabled || m.ports == nil {
		return ErrPortsInactive
	}
	resolved, e := m.resolvePorts(ctx)
	if len(expected) != 0 {
		if e != nil {
			return errors.New("resolved ports changed before application")
		}
		for _, want := range expected {
			found := false
			for _, current := range resolved {
				if current == want {
					found = true
					break
				}
			}
			if !found {
				return errors.New("resolved ports changed before application")
			}
		}
	}
	// Empty replacement is always allowed: failed detection must withdraw routes.
	if len(expected) == 0 {
		resolved = nil
	}
	return m.replacePorts(ctx, expected)
}

// forgetDeletedServices drops bindings to services that no longer exist, so a
// connection never lists them and a disabled one never offers them in its
// form. Disabled services keep their bindings. An unreadable list forgets nothing.
func (m *Selection) forgetDeletedServices(ctx context.Context) {
	if len(m.saved.PortBindings) == 0 {
		return
	}
	known, ok := m.knownPorts(ctx)
	if !ok {
		return
	}
	exists := map[string]bool{}
	for _, r := range known {
		exists[r.ID] = true
	}
	bindings := map[string]int{}
	for id, port := range m.saved.PortBindings {
		if exists[id] {
			bindings[id] = port
		}
	}
	if len(bindings) == len(m.saved.PortBindings) {
		return
	}
	next := m.saved
	next.PortBindings = bindings
	if !next.Enabled && next.Settings != nil {
		s := *next.Settings
		s.Mappings = resolvedMappings(next.Provider, bindings, known)
		next.Settings = &s
	}
	if e := m.persist(next); e != nil {
		connector.Logf("%s: deleted services not forgotten: %v", next.Provider, e)
	}
}

// keptSettings rebuilds the saved Tailscale or IPv6 services from their
// bindings, so a stop while the tracker has withdrawn the routes never saves
// that empty set. An unreadable service list keeps the settings as they are.
func (m *Selection) keptSettings(ctx context.Context) *Settings {
	if m.saved.Provider != "ipv6" || m.saved.Settings == nil || len(m.saved.PortBindings) == 0 {
		return m.saved.Settings
	}
	known, ok := m.knownPorts(ctx)
	if !ok {
		return m.saved.Settings
	}
	s := *m.saved.Settings
	s.Mappings = resolvedMappings(m.saved.Provider, m.saved.PortBindings, known)
	return &s
}

// knownPorts lists every existing service, enabled or not; false when the
// source cannot tell.
func (m *Selection) knownPorts(ctx context.Context) ([]ResolvedPort, bool) {
	source, ok := m.ports.(interface {
		KnownPorts(context.Context) ([]ResolvedPort, error)
	})
	if !ok {
		return nil, false
	}
	known, e := source.KnownPorts(ctx)
	return known, e == nil
}
func (m *Selection) WithdrawPorts(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	if m.saved.Provider != "ipv6" || !m.saved.Enabled || m.ports == nil {
		return nil
	}
	return m.replacePorts(ctx, nil)
}
func (m *Selection) replacePorts(ctx context.Context, resolved []ResolvedPort) error {
	id := m.saved.Provider
	p := m.registry[id]
	if p == nil || m.saved.Settings == nil {
		return ErrPortsInactive
	}
	s := *m.saved.Settings
	s.Mappings = resolvedMappings(id, m.saved.PortBindings, resolved)
	// Additions use the same journal/readback/rollback owner as explicit Apply.
	// A trusted detector withdrawing obsolete targets remains a revocation: it
	// must never resurrect an unsafe old upstream through rollback.
	retained := true
	for _, old := range m.saved.Settings.Mappings {
		found := false
		for _, next := range s.Mappings {
			if old == next {
				found = true
				break
			}
		}
		retained = retained && found
	}
	if retained && len(s.Mappings) > 0 && len(m.saved.Settings.Mappings) > 0 && m.saved.Enabled && !m.portBlocked {
		_, e := m.safeUpdate(ctx, p, s, m.saved.PortBindings)
		if e == nil && m.pending != nil {
			return connector.Preserved(errApplyBusy)
		}
		return e
	}
	m.cancelApply()
	// Persist the replacement before runtime effects. Recovery cannot replay the
	// old unsafe mappings even if cleanup or the runtime's own save fails.
	next := m.saved
	next.Settings = &s
	if e := m.persist(next); e != nil {
		m.failed = true
		m.portBlocked = true
		_ = p.Stop()
		return e
	}
	var e error
	switch p := p.(type) {
	case *IPv6Runtime:
		e = p.AcceptMappings(s.Mappings)
		if e == nil && len(s.Mappings) != 0 {
			e = p.Restore(ctx)
		}
	default:
		e = ErrPortsInactive
	}
	if e != nil {
		m.failed = true
		m.portBlocked = true
		if stopErr := p.Stop(); stopErr != nil {
			return errors.New("port mapping cleanup required")
		}
		return e
	}
	m.failed = false
	m.portBlocked = false
	return nil
}
func (m *Selection) restorePorts(ctx context.Context, p Runtime) error {
	resolved, readErr := m.resolvePorts(ctx)
	if readErr != nil {
		resolved = nil
	}
	if m.saved.Settings == nil {
		return errors.New("resolved provider settings unavailable")
	}
	s := *m.saved.Settings
	s.Mappings = resolvedMappings(m.saved.Provider, m.saved.PortBindings, resolved)
	next := m.saved
	next.Settings = &s
	if e := m.persist(next); e != nil {
		return e
	}
	var e error
	switch p := p.(type) {
	case *IPv6Runtime:
		e = p.Restore(ctx)
		if e == nil {
			e = p.AcceptMappings(s.Mappings)
		}
	default:
		e = ErrPortsInactive
	}
	if e != nil {
		return e
	}
	return readErr
}
