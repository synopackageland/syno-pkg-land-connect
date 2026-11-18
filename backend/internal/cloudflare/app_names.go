package cloudflare

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"dsmapp/internal/publicname"
)

// DefaultAppHostname considers both local reservations and fresh DNS ownership.
// It proposes a name only; approval and publication still preflight it again.
func (t *Tunnel) DefaultAppHostname(ctx context.Context, pkg string, occupied []string) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.safe != nil {
		return "", errAuthorizationBusy
	}
	t.ops.Lock()
	defer t.ops.Unlock()
	signed, err := readCert(t.certFile())
	if err != nil {
		return "", errReconnect
	}
	d, err := t.savedDomain()
	if err != nil {
		return "", err
	}
	_, zone, ok := strings.Cut(d.Hostname, ".")
	if !ok {
		return "", errZone
	}
	occupied = append(slices.Clone(occupied), d.Hostname)
	return t.chooseHostname(ctx, signed, d, zone, publicname.ShortName(pkg), occupied)
}

func (t *Tunnel) chooseHostname(ctx context.Context, signed cert, d domain, zone, base string, occupied []string) (string, error) {
	label, err := publicname.Choose(ctx, base, publicname.NASName(), func(label string) (bool, error) {
		host := label + "." + zone
		if slices.Contains(occupied, host) {
			return false, nil
		}
		if d.TunnelID != "" {
			n, e := t.classifyName(ctx, signed, host, d.TunnelID)
			if e != nil {
				return false, e
			}
			return allowedClaim(n, nil), nil
		}
		n, err := t.classifyName(ctx, signed, host)
		return allowedClaim(n, nil), err
	})
	if errors.Is(err, publicname.ErrTaken) {
		return "", errNameTaken
	}
	if err != nil {
		return "", err
	}
	return label + "." + zone, nil
}

func (t *Tunnel) defaultDomainName(ctx context.Context, signed cert, zone string, occupied []string) (string, error) {
	base, _ := t.defaultName()
	return t.chooseHostname(ctx, signed, domain{}, zone, base, occupied)
}

// DeleteAppRoute follows durable removal, after ingress has been withdrawn.
// It never deletes the DSM name or a changed/foreign record.
func (t *Tunnel) DeleteAppRoute(ctx context.Context, hostname string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.safe != nil {
		return errAuthorizationBusy
	}
	t.ops.Lock()
	defer t.ops.Unlock()
	d, err := t.savedDomain()
	if err != nil {
		return err
	}
	if hostname == d.Hostname {
		return errAppRoutes
	}
	return t.deleteRouted(ctx, d, hostname)
}

func (t *Tunnel) orphanedAppRoutes(d domain) []string {
	claimed := map[string]bool{d.Hostname: true}
	if server := t.serverHostname(); server != "" {
		claimed[server] = true
	}
	for _, hostname := range d.AppRoutes {
		claimed[hostname] = true
	}
	orphans := []string{}
	for _, hostname := range d.Routed {
		if !claimed[hostname] {
			orphans = append(orphans, hostname)
		}
	}
	slices.Sort(orphans)
	return slices.Compact(orphans)
}

// OrphanedAppRoutes reports legacy Connect-created routes which predate the
// package ledger. They are never removed automatically because an older App
// may still expect its withdrawn name; the administrator can clean them once.
func (t *Tunnel) OrphanedAppRoutes() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	d, err := t.savedDomain()
	if err != nil {
		return 0
	}
	return len(t.orphanedAppRoutes(d))
}

// CleanupOrphanedAppRoutes removes only locally inventoried routes still bound
// to the retained tunnel. An ownership receipt, when present, must match too.
func (t *Tunnel) CleanupOrphanedAppRoutes(ctx context.Context) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.safe != nil {
		return 0, errAuthorizationBusy
	}
	t.ops.Lock()
	defer t.ops.Unlock()
	d, err := t.savedDomain()
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, hostname := range t.orphanedAppRoutes(d) {
		if err = t.deleteRouted(ctx, d, hostname); err != nil {
			return removed, err
		}
		d.Routed = slices.DeleteFunc(d.Routed, func(name string) bool { return name == hostname })
		removed++
	}
	return removed, nil
}

// deleteRouted deletes one recorded route of this tunnel. t.ops is held.
func (t *Tunnel) deleteRouted(ctx context.Context, d domain, hostname string) error {
	recorded := slices.Contains(d.Routed, hostname)
	for _, name := range d.AppRoutes {
		recorded = recorded || name == hostname
	}
	if !recorded {
		return nil
	}
	signed, err := readCert(t.certFile())
	if err != nil {
		return errReconnect
	}
	ledger, err := t.ownership(signed.accountID)
	if err != nil {
		return err
	}
	tunnelID := d.TunnelID
	// A tunnel recreation re-routes only active names, so a withdrawn name can
	// still point at the replaced tunnel. Its exact, marked receipt for a tunnel
	// this installation owned identifies that tunnel; anything else fails closed.
	if expected, ok := ledger.DNS[hostname]; ok {
		owner := strings.TrimSuffix(strings.TrimSuffix(expected.Content, "."), ".cfargotunnel.com")
		if owner != d.TunnelID && ledger.Tunnels[owner] && tunnelUUID.MatchString(owner) && expected.Comment == dnsMarker(owner) {
			tunnelID = owner
		}
	}
	row, found, err := t.cfg.API.FindRoute(ctx, signed, hostname, tunnelID)
	if err != nil {
		return err
	}
	if found {
		if expected, ok := ledger.DNS[hostname]; ok && expected != row {
			return t.cfg.API.conflict(ctx, signed, nameClaim{name: hostname, record: row, reason: "other", found: true})
		}
	}
	if found {
		if err = t.cfg.API.DeleteRoute(ctx, signed, row, tunnelID); err != nil {
			return err
		}
	}
	d.Routed = slices.DeleteFunc(d.Routed, func(name string) bool { return name == hostname })
	for pkg, name := range d.AppRoutes {
		if name == hostname {
			delete(d.AppRoutes, pkg)
		}
	}
	if err = t.forgetOwnedRoute(signed, hostname); err != nil {
		return err
	}
	return t.saveRecord(d)
}

// reconcileAppRouteOwnership migrates active routes into the package ledger,
// removes ledger entries whose App no longer exists and reclaims DNS left in
// the ownership journal but absent from the route ledger. Every remote delete
// still passes deleteRouted's exact record and tunnel checks.
func (t *Tunnel) reconcileAppRouteOwnership(ctx context.Context, d *domain) error {
	active, err := t.cfg.Apps.ActivePublic(ctx)
	if err != nil {
		return err
	}
	changed := false
	if d.AppRoutes == nil {
		d.AppRoutes = map[string]string{}
	}
	for _, route := range active {
		if d.AppRoutes[route.Package] != route.Scope.Authority {
			d.AppRoutes[route.Package] = route.Scope.Authority
			changed = true
		}
		if !slices.Contains(d.Routed, route.Scope.Authority) {
			d.Routed = append(d.Routed, route.Scope.Authority)
			changed = true
		}
	}
	for pkg, hostname := range d.AppRoutes {
		if t.cfg.Apps.AppExists(ctx, pkg) {
			continue
		}
		if err = t.deleteRouted(ctx, *d, hostname); err != nil {
			return fmt.Errorf("cleanup %s: %w", pkg, err)
		}
		delete(d.AppRoutes, pkg)
		d.Routed = slices.DeleteFunc(d.Routed, func(name string) bool { return name == hostname })
		changed = true
	}
	if err = t.cleanupUnroutedOwnership(ctx, *d); err != nil {
		return err
	}
	if changed {
		return t.saveRecord(*d)
	}
	return nil
}
