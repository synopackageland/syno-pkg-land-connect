package ports

import "context"

func (s Store) followSnapshot(ctx context.Context, p settings, v *Snapshot) {
	v.FollowServices = p.FollowServices
	v.FollowState = "off"
	v.Services = []Service{}
	if !p.FollowServices {
		return
	}
	d := s.Services
	if d == nil {
		d = &defaultServices
	}
	scan, e := d.DetectServices(ctx)
	if e != nil {
		v.FollowState = "unavailable"
		return
	}
	v.Services = scan.Services
	v.UDPSkipped = scan.UDPSkipped
	if scan.Overflow {
		v.FollowState = "limit"
		return
	}
	used := map[int]bool{}
	for _, entry := range v.Entries {
		if entry.Port != nil && (entry.Source == "dsm" || entry.Enabled) {
			used[*entry.Port] = true
		}
	}
	additions := []Mapping{}
	for _, service := range scan.Services {
		// Keep existing manual/DSM consent and protocol for a duplicate local port.
		if used[service.Port] {
			continue
		}
		additions = append(additions, Mapping{service.ID, TCP, service.Port, "detected", "127.0.0.1"})
	}
	if len(v.Plan.Mappings)+len(additions) > MaxFollowed {
		v.FollowState = "limit"
		return
	}
	v.FollowState = "ready"
	if v.Detection == "ready" {
		v.Plan.Mappings = append(v.Plan.Mappings, additions...)
	}
}

// SetFollow records explicit consent to future installed TCP declarations. It
// grants no Cloudflare/IPv6/NetBird exposure; the provider enforces scope.
func (s Store) SetFollow(ctx context.Context, d Detector, enabled, confirm bool) (Snapshot, error) {
	if enabled && !confirm {
		return Snapshot{}, ErrConfirmation
	}
	if e := s.locked(ctx, func(p *settings) (bool, error) {
		changed := p.FollowServices != enabled
		p.FollowServices = enabled
		return changed, nil
	}); e != nil {
		return Snapshot{}, e
	}
	return s.List(ctx, d)
}
