package ports

import (
	"context"
	"dsmapp/internal/provider"
	"errors"
)

// SelectingApplier delegates replacement/withdrawal to the selecting owner. It
// never calls an unprotected Runtime or restarts a connection without ports.
type SelectingApplier struct {
	Selection interface {
		PortEpoch() string
		ReplacePorts(context.Context, []provider.ResolvedPort) error
		WithdrawPorts(context.Context) error
	}
}

func (a SelectingApplier) OwnershipKey() string {
	if a.Selection == nil {
		return "unavailable"
	}
	return a.Selection.PortEpoch()
}
func (a SelectingApplier) Apply(ctx context.Context, p Plan) error {
	if a.Selection == nil {
		return ErrProviderPending
	}
	out := make([]provider.ResolvedPort, 0, len(p.Mappings))
	for _, m := range p.Mappings {
		if m.TargetHost != "127.0.0.1" {
			return ErrValidation
		}
		out = append(out, provider.ResolvedPort{ID: m.ID, Mapping: provider.PortMapping{LocalPort: m.Port, PublicPort: m.Port, Protocol: string(m.Protocol)}})
	}
	e := a.Selection.ReplacePorts(ctx, out)
	if errors.Is(e, provider.ErrPortsInactive) {
		return ErrProviderPending
	}
	return e
}
func (a SelectingApplier) Revoke(ctx context.Context) error {
	if a.Selection == nil {
		return nil
	}
	return a.Selection.WithdrawPorts(ctx)
}

func (s ResolvedSource) ResolvePorts(ctx context.Context) ([]provider.ResolvedPort, error) {
	v, e := s.Store.List(ctx, s.Detector)
	if e != nil {
		return nil, e
	}
	if v.Detection != "ready" {
		return nil, ErrDetection
	}
	out := make([]provider.ResolvedPort, 0, len(v.Plan.Mappings))
	for _, m := range v.Plan.Mappings {
		out = append(out, provider.ResolvedPort{ID: m.ID, Mapping: provider.PortMapping{LocalPort: m.Port, PublicPort: m.Port, Protocol: string(m.Protocol)}})
	}
	return out, nil
}

// KnownPorts lists every service that still exists, enabled or not, so the
// selector forgets only bindings to deleted services. It refuses while DSM
// ports are undetected: missing DSM entries must not read as deleted.
func (s ResolvedSource) KnownPorts(ctx context.Context) ([]provider.ResolvedPort, error) {
	v, e := s.Store.List(ctx, s.Detector)
	if e != nil {
		return nil, e
	}
	if v.Detection != "ready" {
		return nil, ErrDetection
	}
	out := make([]provider.ResolvedPort, 0, len(v.Entries))
	for _, entry := range v.Entries {
		if entry.Port != nil {
			out = append(out, provider.ResolvedPort{ID: entry.ID, Mapping: provider.PortMapping{LocalPort: *entry.Port, PublicPort: *entry.Port, Protocol: string(entry.Protocol)}})
		}
	}
	return out, nil
}
