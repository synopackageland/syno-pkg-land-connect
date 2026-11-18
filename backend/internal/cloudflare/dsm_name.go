package cloudflare

import (
	"context"
	"strings"
)

// DSM names use the same DNS preflight and exact ownership checks as App names.
// Only the first label may change; the current tunnel and zone stay pinned.
func (t *Tunnel) dsmName(ctx context.Context, hostname string, apply bool) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.safe != nil {
		return errAuthorizationBusy
	}
	if t.authCancel != nil {
		select {
		case <-t.authDone:
		default:
			return errAuthorizationBusy
		}
	}
	t.ops.Lock()
	defer t.ops.Unlock()
	d, err := t.savedDomain()
	if err != nil {
		return err
	}
	_, zone, _ := strings.Cut(d.Hostname, ".")
	label, suffix, ok := strings.Cut(hostname, ".")
	if !ok || !dnsLabel.MatchString(label) || suffix != zone {
		return errHostname
	}
	if t.cfg.Apps == nil {
		return errAppRoutes
	}
	routes, err := t.cfg.Apps.ActivePublic(ctx)
	if err != nil {
		return err
	}
	for _, r := range routes {
		if r.Scope.Authority == hostname {
			return errAppRoutes
		}
	}
	signed, err := readCert(t.certFile())
	if err != nil {
		return errReconnect
	}
	signedZone, zoneErr := t.cfg.API.ZoneName(ctx, signed)
	if zoneErr != nil || signedZone != zone {
		return errReconnect
	}
	if err = t.checkOwnedRoutes(ctx, signed, d); err != nil {
		return err
	}
	if hostname == d.Hostname {
		return nil
	}
	if err = t.namesFree(ctx, signed, []string{hostname}); err != nil {
		return err
	}
	if !apply {
		return nil
	}
	bin, _, err := t.prepare(ctx)
	if err != nil {
		return err
	}
	if !containsName(d.Routed, hostname) {
		if err = t.route(ctx, bin, d.TunnelID, hostname); err != nil {
			return err
		}
		d.Routed = append(d.Routed, hostname)
	}
	d.Hostname = hostname
	return t.saveRecord(d)
}
func (t *Tunnel) CheckDSMName(ctx context.Context, hostname string) error {
	return t.dsmName(ctx, hostname, false)
}
func (t *Tunnel) SetDSMName(ctx context.Context, hostname string) error {
	return t.dsmName(ctx, hostname, true)
}
