package cloudflare

import (
	"context"
	"errors"
	"strings"
)

// SetAppTargets installs the daemon's batch state/proxy adapter before request
// handling. It must not be called from a callback already owned by this tunnel.
func (t *Tunnel) SetAppTargets(targets AppTargets) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.safe != nil {
		return errAuthorizationBusy
	}
	t.ops.Lock()
	defer t.ops.Unlock()
	t.cfg.Apps = targets
	return nil
}

// CheckPublicApps is the approval owner's whole-batch DNS preflight. It never
// creates records, changes state, starts listeners or restarts cloudflared.
func (t *Tunnel) CheckPublicApps(ctx context.Context, routes []AppRoute) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.safe != nil {
		return errAuthorizationBusy
	}
	t.ops.Lock()
	defer t.ops.Unlock()
	signed, e := readCert(t.certFile())
	if e != nil {
		return errReconnect
	}
	zone, e := t.cfg.API.ZoneName(ctx, signed)
	if e != nil {
		return errReconnect
	}
	d, e := t.savedDomain()
	fresh := errors.Is(e, errNoDomain)
	if e != nil && !fresh {
		return e
	}
	if fresh {
		occupied := []string{}
		for _, r := range routes {
			occupied = append(occupied, r.Scope.Authority)
		}
		host, err := t.defaultDomainName(ctx, signed, zone, occupied)
		if err != nil {
			return err
		}
		d = domain{Hostname: host}
	}
	if err := t.checkOwnedRoutes(ctx, signed, d); err != nil {
		return err
	}
	if !strings.HasSuffix(d.Hostname, "."+zone) {
		return errZone
	}
	if len(routes) == 0 || !validAppRoutes(routes, d) {
		return errAppRoutes
	}
	names := []string{}
	if fresh {
		names = append(names, d.Hostname)
	}
	for _, r := range routes {
		if !containsName(d.Routed, r.Scope.Authority) {
			names = append(names, r.Scope.Authority)
		}
	}
	return t.namesFree(ctx, signed, names)
}

// A local progress record is not proof that DNS still belongs to this tunnel.
func (t *Tunnel) checkOwnedRoutes(ctx context.Context, c cert, d domain) error {
	for _, name := range d.Routed {
		if _, _, err := t.cfg.API.FindRoute(ctx, c, name, d.TunnelID); err != nil {
			if errors.Is(err, errRoute) {
				claim, e := t.cfg.API.classifyName(ctx, c, name, d.TunnelID)
				if e != nil {
					return e
				}
				return t.cfg.API.conflict(ctx, c, claim)
			}
			return err
		}
	}
	return nil
}
