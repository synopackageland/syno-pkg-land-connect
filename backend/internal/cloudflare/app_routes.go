package cloudflare

import (
	"context"
	"dsmapp/internal/localipc"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"dsmapp/internal/apps"
	"dsmapp/internal/connector"
)

var errProxyRequired = connector.Coded("pin_proxy_required", "approve the DSM HTTPS proxy target first")
var errAppRoutes = connector.Coded("cloudflare_invalid_app_routes", "approved App routes are unavailable")
var socketName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}\.sock$`)

// AppRoute comes from approved App state, never a browser socket path.
type AppRoute = apps.Target

// AppTargets is the state/proxy owner's boundary. ActivatePublic must durably
// write active state before starting listeners; PausePublic must durably pause
// and close listeners before returning. ActivePublic returns only valid active
// bindings, so provider leftovers never become authorization.
type AppTargets interface {
	ActivePublic(context.Context) ([]AppRoute, error)
	ActivatePublic(context.Context, []AppRoute) error
	PausePublic(context.Context, []AppRoute, string) error
	AppExists(context.Context, string) bool
}

type RouteResult struct {
	OK      bool   `json:"ok"`
	Package string `json:"package"`
	Code    string `json:"code,omitempty"`
}

func privateSocket(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && filepath.Dir(path) == localipc.RunDir && socketName.MatchString(filepath.Base(path))
}
func validAppRoutes(routes []AppRoute, d domain) bool {
	_, zone, _ := strings.Cut(d.Hostname, ".")
	seen := map[string]bool{d.Hostname: true}
	packages := map[string]bool{}
	for _, r := range routes {
		label, suffix, ok := strings.Cut(r.Scope.Authority, ".")
		if r.Package == "" || packages[r.Package] || !ok || !dnsLabel.MatchString(label) || suffix != zone || seen[r.Scope.Authority] || !socketName.MatchString(r.Scope.Socket+".sock") || r.Kind != apps.Public || r.Scope.State != apps.Active || r.Scope.Origin != "https://"+r.Scope.Authority {
			return false
		}
		seen[r.Scope.Authority], packages[r.Package] = true, true
	}
	return true
}

// PublishApps performs one administrator-confirmed multi-selection. It checks
// all DNS collisions before routing anything, records only successful names,
// persists App intent through the owner, then writes ingress and restarts once.
func (t *Tunnel) PublishApps(ctx context.Context, routes []AppRoute) ([]RouteResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.safe != nil || t.cfg.Apps == nil {
		return nil, errAuthorizationBusy
	}
	// An authorization worker must finish before a route batch can own ops.
	if t.authCancel != nil {
		select {
		case <-t.authDone:
		default:
			return nil, errAuthorizationBusy
		}
	}
	t.ops.Lock()
	defer t.ops.Unlock()
	bin, port, e := t.prepare(ctx)
	if e != nil {
		return nil, e
	}
	signed, e := readCert(t.certFile())
	if e != nil {
		return nil, errReconnect
	}
	zone, e := t.cfg.API.ZoneName(ctx, signed)
	if e != nil {
		return nil, errReconnect
	}
	d, loadErr := t.savedDomain()
	fresh := errors.Is(loadErr, errNoDomain)
	if loadErr != nil && !fresh {
		return nil, loadErr
	}
	if fresh {
		occupied := []string{}
		for _, r := range routes {
			occupied = append(occupied, r.Scope.Authority)
		}
		host, err := t.defaultDomainName(ctx, signed, zone, occupied)
		if err != nil {
			return nil, err
		}
		d = domain{Hostname: host}
	}
	if err := t.checkOwnedRoutes(ctx, signed, d); err != nil {
		return nil, err
	}
	if !strings.HasSuffix(d.Hostname, "."+zone) {
		return nil, errZone
	}
	if !validAppRoutes(routes, d) || len(routes) == 0 {
		return nil, errAppRoutes
	}
	for _, r := range routes {
		if server := t.serverHostname(); server != "" && r.Scope.Authority == server {
			return nil, errServerNameInApps
		}
	}
	active, e := t.cfg.Apps.ActivePublic(ctx)
	if e != nil {
		return nil, e
	}
	combined := append([]AppRoute(nil), routes...)
	for _, old := range active {
		replaced := false
		for _, next := range routes {
			if next.Package == old.Package {
				replaced = true
				break
			}
		}
		if !replaced {
			combined = append(combined, old)
		}
	}
	if !validAppRoutes(combined, d) {
		return nil, errAppRoutes
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
	if e = t.namesFree(ctx, signed, names); e != nil {
		return nil, e
	}
	if fresh {
		created, e := t.create(ctx, bin, t.certFile(), "nas", signed.accountID)
		if e != nil {
			return nil, e
		}
		d.TunnelID = created.tunnelID
		if e = t.route(ctx, bin, d.TunnelID, d.Hostname); e != nil {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
			defer cancel()
			_ = t.launcher().Run(cleanup, Command{Path: bin, Args: []string{"tunnel", "--origincert", t.certFile(), "delete", d.TunnelID}, Home: t.home()})
			return nil, errRoute
		}
		d.Routed = append(d.Routed, d.Hostname)
		if e = t.saveDomain(d, created); e != nil {
			return nil, e
		}
	}
	results := make([]RouteResult, 0, len(routes))
	successful := []AppRoute{}
	for _, r := range routes {
		result := RouteResult{Package: r.Package}
		if !containsName(d.Routed, r.Scope.Authority) {
			if e = t.route(ctx, bin, d.TunnelID, r.Scope.Authority); e != nil {
				if _, found, readErr := t.cfg.API.FindRoute(ctx, signed, r.Scope.Authority, d.TunnelID); readErr != nil || !found {
					result.Code = failCode(errRoute)
					results = append(results, result)
					continue
				}
			}
			// Persist DNS progress before any App route can start. Retrying never
			// overwrites an unrelated name, even after a partial batch.
			d.Routed = append(d.Routed, r.Scope.Authority)
		}
		if d.AppRoutes == nil {
			d.AppRoutes = map[string]string{}
		}
		d.AppRoutes[r.Package] = r.Scope.Authority
		if e = t.saveRecord(d); e != nil {
			return results, e
		}
		successful = append(successful, r)
		results = append(results, result)
	}
	if len(successful) == 0 {
		return results, nil
	}
	if e = t.cfg.Apps.ActivatePublic(ctx, successful); e != nil {
		if pause := t.pauseApps(ctx, successful); pause != nil {
			return results, pause
		}
		return results, e
	}
	// Generate the complete ingress from active state; never from provider DNS.
	r, e := t.domainRun(d, port)
	if e == nil {
		// Write config before ending the previous process. A batch replaces it once.
		t.stop()
		e = t.saveSettings(settings{Enabled: true, Mode: "domain"})
		if e == nil {
			e = t.serve(ctx, bin, r)
		}
	}
	if e != nil {
		if pause := t.pauseApps(ctx, successful); pause != nil {
			return results, pause
		}
		return results, e
	}
	for i := range results {
		results[i].OK = results[i].Code == ""
	}
	return results, nil
}

// PublicResult matches apps.setPublic's UI response.
type PublicResult struct {
	Results  []RouteResult `json:"results"`
	LoginURL string        `json:"loginUrl,omitempty"`
}

func (t *Tunnel) SetPublicApps(ctx context.Context, routes []AppRoute) (PublicResult, error) {
	results, e := t.PublishApps(ctx, routes)
	return PublicResult{Results: results}, e
}
func containsName(names []string, name string) bool {
	for _, n := range names {
		if n == name {
			return true
		}
	}
	return false
}

// ReconcileAppRoutes applies an explicit local binding change without DNS or
// login. E1 pauses close proxy connections themselves and need no restart.
func (t *Tunnel) ReconcileAppRoutes(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.safe != nil || t.cfg.Apps == nil {
		return errAuthorizationBusy
	}
	if !t.loadSettings().Enabled {
		return nil
	}
	t.ops.Lock()
	defer t.ops.Unlock()
	d, e := t.savedDomain()
	if e != nil {
		return e
	}
	if e = t.reconcileAppRouteOwnership(ctx, &d); e != nil {
		return e
	}
	bin, port, e := t.prepare(ctx)
	if e != nil {
		return e
	}
	r, e := t.domainRun(d, port)
	if connector.ErrorCode(e) == "pin_proxy_required" {
		// A revoked DSM target ends the running tunnel as a restart would:
		// its ingress still names the revoked socket, and the failure asks
		// for the administrator's review now rather than after a restart.
		t.stop()
		t.refused(d, e)
	}
	if e != nil {
		return e
	}
	t.stop()
	return t.serve(ctx, bin, r)
}

func (t *Tunnel) appSocket(id string) string {
	return apps.PinSocket(id)
}
func (t *Tunnel) appIngress(d domain) (string, error) {
	if t.cfg.Apps == nil {
		return "", nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	routes, e := t.cfg.Apps.ActivePublic(ctx)
	if e != nil || !validAppRoutes(routes, d) {
		return "", errAppRoutes
	}
	yaml := ""
	for _, r := range routes {
		if !privateSocket(t.appSocket(r.Scope.Socket)) {
			return "", errAppRoutes
		}
		if r.Path != "" {
			if !validPathSet(r.Path, r.AdditionalPaths) {
				return "", errAppRoutes
			}
			yaml += fmt.Sprintf("  - hostname: %q\n    path: %q\n    service: %q\n", r.Scope.Authority, pathPattern(r.Path, r.AdditionalPaths), "unix:"+t.appSocket(r.Scope.Socket))
			yaml += fmt.Sprintf("  - hostname: %q\n    service: http_status:404\n", r.Scope.Authority)
		} else {
			yaml += fmt.Sprintf("  - hostname: %q\n    service: %q\n", r.Scope.Authority, "unix:"+t.appSocket(r.Scope.Socket))
		}
	}
	return yaml, nil
}

func (t *Tunnel) pauseApps(ctx context.Context, routes []AppRoute) error {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	return t.cfg.Apps.PausePublic(cleanup, routes, "route-failed")
}

func validPathSet(path string, extra []string) bool {
	if !apps.ValidIngressPath(path) || len(extra) > 3 {
		return false
	}
	seen := map[string]bool{path: true}
	for _, p := range extra {
		if !apps.ValidIngressPath(p) || seen[p] {
			return false
		}
		seen[p] = true
	}
	return true
}
func pathPattern(path string, extra []string) string {
	if len(extra) == 0 {
		return "^" + regexp.QuoteMeta(path) + "$"
	}
	paths := []string{regexp.QuoteMeta(path)}
	for _, p := range extra {
		paths = append(paths, regexp.QuoteMeta(p))
	}
	return "^(?:" + strings.Join(paths, "|") + ")$"
}
