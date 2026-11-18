package ports

import (
	"context"

	"dsmapp/internal/ipv6"
	"dsmapp/internal/provider"
)

// ResolvedSource resolves enabled, non-conflicting local destinations on every
// read. IPv6 is direct access: PublicPort always equals LocalPort. It does not
// change firewall rules, listeners, DNS, provider selection or exposure scope.
type ResolvedSource struct {
	Store    Store
	Detector Detector
}

var _ ipv6.PortSource = ResolvedSource{}

func (s ResolvedSource) RuntimeMappings(ctx context.Context) ([]provider.PortMapping, error) {
	v, e := s.Store.List(ctx, s.Detector)
	if e != nil {
		return nil, e
	}
	if v.Detection != "ready" {
		return nil, ErrDetection
	}
	out := make([]provider.PortMapping, 0, len(v.Plan.Mappings))
	for _, m := range v.Plan.Mappings {
		if m.Source == "detected" {
			continue
		}
		out = append(out, provider.PortMapping{LocalPort: m.Port, PublicPort: m.Port, Protocol: string(m.Protocol)})
	}
	return out, nil
}
func (s ResolvedSource) Ports(ctx context.Context) ([]ipv6.Port, error) {
	mappings, e := s.RuntimeMappings(ctx)
	if e != nil {
		return nil, e
	}
	out := make([]ipv6.Port, 0, len(mappings))
	for _, m := range mappings {
		out = append(out, ipv6.Port{Number: m.LocalPort, Scheme: m.Protocol})
	}
	return out, nil
}
