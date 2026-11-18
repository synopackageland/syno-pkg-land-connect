package cloudflare

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"dsmapp/internal/connector"
)

var (
	errNotConnected     = connector.Coded("cloudflare_not_connected", "connect Cloudflare first")
	errOwnDomainNeeded  = connector.Coded("cloudflare_own_domain_required", "connect Cloudflare with your own domain first")
	errServerOrigin     = connector.Coded("netbird_server_not_ready", "the NetBird server is not set up for this name")
	errServerNameInApps = connector.Coded("cloudflare_name_taken", "this name is already used in the domain; choose another")
)

// ServerOrigin is the self-hosted NetBird server behind one own-domain name.
// cloudflared reaches it over TLS with HTTP/2, which gRPC needs, and verifies
// the server's own certificate instead of skipping verification.
type ServerOrigin struct {
	Hostname string
	Port     int
	CAFile   string
}

func (o ServerOrigin) valid(zone string) bool {
	label, suffix, ok := strings.Cut(o.Hostname, ".")
	return ok && dnsLabel.MatchString(label) && suffix == zone && o.Port > 0 && o.Port < 65536 &&
		filepath.IsAbs(o.CAFile) && filepath.Clean(o.CAFile) == o.CAFile
}

// serverIngress is the rule for an enabled server, or nothing. cloudflared
// carries gRPC only over its HTTP/2 transport, so the rule also selects it.
func (t *Tunnel) serverIngress(d domain) (rule, protocol string, e error) {
	if t.cfg.NetBirdServer == nil {
		return "", "", nil
	}
	o, ok := t.cfg.NetBirdServer()
	if !ok {
		return "", "", nil
	}
	_, zone, _ := strings.Cut(d.Hostname, ".")
	if !o.valid(zone) || o.Hostname == d.Hostname {
		return "", "", errServerOrigin
	}
	rule = fmt.Sprintf("  - hostname: %q\n    service: %q\n    originRequest:\n      http2Origin: true\n      originServerName: %q\n      caPool: %q\n",
		o.Hostname, fmt.Sprintf("https://127.0.0.1:%d", o.Port), o.Hostname, o.CAFile)
	return rule, "protocol: \"http2\"\n", nil
}

func (t *Tunnel) serverHostname() string {
	if t.cfg.NetBirdServer == nil {
		return ""
	}
	o, _ := t.cfg.NetBirdServer()
	return o.Hostname
}

// ownDomain is the running own domain with retained authorization. t.ops is held.
func (t *Tunnel) ownDomain() (domain, string, error) {
	s := t.loadSettings()
	if !s.Enabled {
		return domain{}, "", errNotConnected
	}
	if s.Mode != "domain" {
		return domain{}, "", errOwnDomainNeeded
	}
	d, e := t.savedDomain()
	if e != nil {
		return domain{}, "", errOwnDomainNeeded
	}
	if _, e = readCert(t.certFile()); e != nil {
		return domain{}, "", errReconnect
	}
	_, zone, _ := strings.Cut(d.Hostname, ".")
	return d, zone, nil
}

// ServerZone is the domain a self-hosted server can use, without network calls.
// Private files are atomically replaced. This is an observation only; each
// mutation still revalidates authorization while holding ops.
func (t *Tunnel) ServerZone() (string, error) {
	_, zone, e := t.ownDomain()
	return zone, e
}

// ServerHostname proposes a free name for the server; RouteServer checks it again.
func (t *Tunnel) ServerHostname(ctx context.Context) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.safe != nil {
		return "", errAuthorizationBusy
	}
	t.ops.Lock()
	defer t.ops.Unlock()
	d, zone, e := t.ownDomain()
	if e != nil {
		return "", e
	}
	signed, e := readCert(t.certFile())
	if e != nil {
		return "", errReconnect
	}
	occupied := slices.Clone(d.Routed)
	if t.cfg.Apps != nil {
		routes, e := t.cfg.Apps.ActivePublic(ctx)
		if e != nil {
			return "", e
		}
		for _, r := range routes {
			occupied = append(occupied, r.Scope.Authority)
		}
	}
	return t.chooseHostname(ctx, signed, domain{TunnelID: d.TunnelID}, zone, "netbird", append(occupied, d.Hostname))
}

// RouteServer points host at this tunnel, then serves the server's ingress
// with one restart. The server's origin must already name host.
func (t *Tunnel) RouteServer(ctx context.Context, host string) error {
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
	d, zone, e := t.ownDomain()
	if e != nil {
		return e
	}
	label, suffix, ok := strings.Cut(host, ".")
	if !ok || !dnsLabel.MatchString(label) || suffix != zone || host == d.Hostname {
		return errHostname
	}
	if t.serverHostname() != host {
		return errServerOrigin
	}
	if t.cfg.Apps != nil {
		routes, e := t.cfg.Apps.ActivePublic(ctx)
		if e != nil {
			return e
		}
		for _, r := range routes {
			if r.Scope.Authority == host {
				return errServerNameInApps
			}
		}
	}
	bin, port, e := t.prepare(ctx)
	if e != nil {
		return e
	}
	signed, e := readCert(t.certFile())
	if e != nil {
		return errReconnect
	}
	if apiZone, e := t.cfg.API.ZoneName(ctx, signed); e != nil || apiZone != zone {
		return errZone
	}
	if !slices.Contains(d.Routed, host) {
		if e = t.namesFree(ctx, signed, []string{host}); e != nil {
			return e
		}
		if e = t.route(ctx, bin, d.TunnelID, host); e != nil {
			return e
		}
		d.Routed = append(d.Routed, host)
		if e = t.saveRecord(d); e != nil {
			return e
		}
	}
	r, e := t.domainRun(d, port)
	if e != nil {
		return e
	}
	t.stop()
	return t.serve(ctx, bin, r)
}

// UnrouteServer withdraws the server's ingress first, then deletes the DNS
// record it created. The caller ends the server's origin before calling. It
// never deletes the DSM name or a changed or foreign record.
func (t *Tunnel) UnrouteServer(ctx context.Context, host string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.safe != nil {
		return errAuthorizationBusy
	}
	t.ops.Lock()
	defer t.ops.Unlock()
	d, e := t.savedDomain()
	if e != nil {
		return e
	}
	if host == d.Hostname || t.serverHostname() == host {
		return errHostname
	}
	if !slices.Contains(d.Routed, host) {
		return nil
	}
	if s := t.loadSettings(); s.Enabled && s.Mode == "domain" {
		bin, port, e := t.prepare(ctx)
		if e != nil {
			return e
		}
		r, e := t.domainRun(d, port)
		if e != nil {
			return e
		}
		t.stop()
		if e = t.serve(ctx, bin, r); e != nil {
			return e
		}
	}
	return t.deleteRouted(ctx, d, host)
}
