package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"dsmapp/internal/connector"
	"github.com/synopackageland/syno-pkg-land-sdk/supervision"
)

// recreation is private, durable recovery state. Keep the old identity until
// every DNS route is repaired; a restart resumes the same replacement tunnel.
type recreation struct {
	Previous    domain          `json:"previous"`
	Credentials json.RawMessage `json:"credentials"`
}

func (t *Tunnel) recreationPending() bool {
	_, err := os.Lstat(t.domainFile("recreate.json"))
	return !errors.Is(err, os.ErrNotExist)
}

func (a API) tunnelDeleted(ctx context.Context, signed cert, id string) (bool, error) {
	var remote struct {
		ID        string  `json:"id"`
		DeletedAt *string `json:"deleted_at"`
	}
	err := a.get(ctx, signed, "/accounts/"+signed.accountID+"/cfd_tunnel/"+id, nil, &remote)
	if errors.Is(err, errNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if remote.ID != id {
		return false, errAPI
	}
	return remote.DeletedAt != nil && *remote.DeletedAt != "", nil
}

// recoveryAuthorization only starts browser sign-in for missing/unusable or
// explicitly refused authorization. A network error never discards the cert.
func (t *Tunnel) recoveryAuthorization(ctx context.Context, gen uint64, bin string) (cert, string, error) {
	signed, err := readCert(t.certFile())
	if err == nil {
		zone, e := t.cfg.API.ZoneName(ctx, signed)
		if !errors.Is(e, ErrPermissionDenied) {
			return signed, zone, e
		}
	}
	t.set(gen, func(s *connector.Status) { s.State = "needs-login"; s.Code = "cloudflare_reconnect_required" })
	signed, zone, err := t.signedIn(ctx, gen, bin)
	if err == nil {
		t.set(gen, func(s *connector.Status) { s.State = "connecting"; s.Code = ""; s.LoginURL = "" })
	}
	return signed, zone, err
}

// recoveryNames follows current approved ingress, not obsolete TCP or withdrawn
// App names in the historical routed ledger.
func (t *Tunnel) recoveryNames(ctx context.Context, d domain) ([]string, error) {
	names := []string{d.Hostname}
	if t.cfg.Apps != nil {
		routes, err := t.cfg.Apps.ActivePublic(ctx)
		if err != nil {
			return nil, err
		}
		if !validAppRoutes(routes, d) {
			return nil, errAppRoutes
		}
		for _, r := range routes {
			if !slices.Contains(d.Routed, r.Scope.Authority) {
				return nil, errAppRoutes
			}
			names = append(names, r.Scope.Authority)
		}
	}
	if t.cfg.NetBirdServer != nil {
		origin, enabled := t.cfg.NetBirdServer()
		if enabled {
			_, zone, _ := strings.Cut(d.Hostname, ".")
			if !origin.valid(zone) || origin.Hostname == d.Hostname || !slices.Contains(d.Routed, origin.Hostname) {
				return nil, errServerOrigin
			}
			names = append(names, origin.Hostname)
		}
	}
	return names, nil
}

// Recovery uses the same retained identity and remote ownership markers as a
// regular reconnect. An unowned dangling CNAME never grants write authority.
func (t *Tunnel) recoveryClaim(ctx context.Context, signed cert, name, oldID, newID string) (nameClaim, error) {
	n, err := t.classifyName(ctx, signed, name, oldID, newID)
	if err != nil {
		return n, err
	}
	ledger, e := t.ownership(signed.accountID)
	if e != nil {
		return n, e
	}
	if expected, ok := ledger.DNS[name]; ok && n.found && expected != n.record && !n.owned {
		n.reason = "other"
		return n, t.cfg.API.conflict(ctx, signed, n)
	}
	if n.found && (n.record.ID == "" || n.record.Name != name || n.record.Type != "CNAME" || !n.record.Proxied || !n.owned && n.tunnel != oldID && (newID == "" || n.tunnel != newID)) {
		n.reason = "other"
		return n, t.cfg.API.conflict(ctx, signed, n)
	}
	return n, nil
}

func (t *Tunnel) recreate(ctx context.Context, gen uint64, bin string) (domain, error) {
	// Other operations can hold ops while stopping this watcher. Polling the
	// lock with cancellation prevents that lock order from deadlocking Stop.
	for !t.ops.TryLock() {
		if err := supervision.Wait(ctx, 10*time.Millisecond); err != nil {
			return domain{}, err
		}
	}
	defer t.ops.Unlock()
	if err := ctx.Err(); err != nil {
		return domain{}, err
	}
	d, err := t.savedDomain()
	if err != nil {
		return domain{}, err
	}
	signed, zone, err := t.recoveryAuthorization(ctx, gen, bin)
	if err != nil {
		return domain{}, err
	}
	if _, suffix, _ := strings.Cut(d.Hostname, "."); suffix != zone {
		return domain{}, errZone
	}
	rawCredentials, err := connector.ReadPrivate(t.domainFile("credentials.json"))
	if err != nil {
		return domain{}, storageError(err)
	}
	savedCredentials, err := parseCredentials(rawCredentials)
	if err != nil || savedCredentials.account != signed.accountID {
		return domain{}, errZone
	}
	raw, err := connector.ReadPrivate(t.domainFile("recreate.json"))
	var pending recreation
	var created credentials
	if err == nil {
		if json.Unmarshal(raw, &pending) != nil {
			return domain{}, errSave
		}
		created, err = parseCredentials(pending.Credentials)
		if err != nil || pending.Previous.Hostname != d.Hostname || !tunnelUUID.MatchString(pending.Previous.TunnelID) || created.account != signed.accountID || d.TunnelID != pending.Previous.TunnelID && d.TunnelID != created.tunnelID {
			return domain{}, errSave
		}
		d = pending.Previous
	} else if !errors.Is(err, os.ErrNotExist) {
		return domain{}, storageError(err)
	}
	names, err := t.recoveryNames(ctx, d)
	if err != nil {
		return domain{}, err
	}
	if created.tunnelID == "" {
		deleted, e := t.cfg.API.tunnelDeleted(ctx, signed, d.TunnelID)
		if e != nil {
			return domain{}, e
		}
		if !deleted {
			return domain{}, errAPI
		} // The child log alone is never deletion evidence.
		for _, name := range names {
			if _, e = t.recoveryClaim(ctx, signed, name, d.TunnelID, ""); e != nil {
				return domain{}, e
			}
		}
		label, _, _ := strings.Cut(d.Hostname, ".")
		created, err = t.create(ctx, bin, t.certFile(), label, signed.accountID)
		if err != nil {
			return domain{}, err
		}
		pending = recreation{Previous: d, Credentials: created.privateJSON()}
		raw, _ = json.Marshal(pending)
		if err = connector.WritePrivate(t.domainFile("recreate.json"), raw); err != nil {
			return domain{}, storageError(err)
		}
	}
	for _, name := range names {
		if t.cfg.BeforeRoute != nil && name != t.serverHostname() {
			if err = t.cfg.BeforeRoute(ctx, signed.accountID, name); err != nil {
				return domain{}, err
			}
		}
		observed, e := t.recoveryClaim(ctx, signed, name, d.TunnelID, created.tunnelID)
		if e != nil {
			return domain{}, e
		}
		if observed.found && observed.tunnel == created.tunnelID {
			continue
		}
		fresh, e := t.recoveryClaim(ctx, signed, name, d.TunnelID, created.tunnelID)
		if e != nil {
			return domain{}, e
		}
		if observed.found != fresh.found || observed.record != fresh.record {
			fresh.reason = "other"
			return domain{}, t.cfg.API.conflict(ctx, signed, fresh)
		}
		if fresh.found {
			if fresh.owned && fresh.tunnel != created.tunnelID {
				if e = t.retireTunnel(signed, fresh.tunnel); e != nil {
					return domain{}, e
				}
			}
			var result struct{ ID string }
			if e = t.cfg.API.request(ctx, signed, http.MethodDelete, "/zones/"+signed.zoneID+"/dns_records/"+fresh.record.ID, nil, &result); e != nil && !errors.Is(e, errNotFound) {
				return domain{}, e
			}
			if e == nil && result.ID != fresh.record.ID {
				return domain{}, errAPI
			}
		}
		if err = t.route(ctx, bin, created.tunnelID, name); err != nil {
			return domain{}, err
		}
		if _, err = t.cfg.API.RouteRecord(ctx, signed, name, created.tunnelID); err != nil {
			return domain{}, err
		}
	}
	d.TunnelID = created.tunnelID
	d.AddressCreatedAt = time.Now()
	if err = t.saveDomain(d, created); err != nil {
		return domain{}, err
	}
	if err = os.Remove(t.domainFile("recreate.json")); err != nil {
		return domain{}, storageError(err)
	}
	t.set(gen, func(s *connector.Status) {
		s.RecoveryNotice = "cloudflare_tunnel_recreated"
		s.State = "connecting"
		s.Code = ""
		s.LoginURL = ""
		s.Supervision = nil
	})
	return d, nil
}

// recoverRun uses tolerant supervision with no total time budget. Each retry
// resumes the private journal; transport failures retain existing credentials.
func (t *Tunnel) recoverRun(ctx context.Context, gen uint64, r run) (run, error) {
	cfg := supervision.Defaults()
	// API/network outages do not consume the child-crash burst budget.
	cfg.Burst = math.MaxInt
	if t.cfg.RestartDelay > 0 {
		cfg.Initial = t.cfg.RestartDelay
		cfg.Min = min(time.Second, cfg.Initial)
	}
	policy := supervision.New(cfg, nil)
	reauthorized := false
	for ctx.Err() == nil {
		bin, err := t.cfg.Cloudflared(ctx)
		var d domain
		if err == nil {
			d, err = t.recreate(ctx, gen, bin)
		}
		if err == nil {
			next, e := t.domainRun(d, r.port)
			if e != nil {
				t.fail(gen, e)
			}
			return next, e
		}
		if ctx.Err() != nil {
			return run{}, ctx.Err()
		}
		code := connector.ErrorCode(err)
		if errors.Is(err, ErrPermissionDenied) && !reauthorized {
			reauthorized = true
			if e := os.Remove(t.certFile()); e != nil && !errors.Is(e, os.ErrNotExist) {
				t.fail(gen, storageError(e))
				return run{}, e
			}
			continue
		}
		if errors.Is(err, ErrPermissionDenied) || code == "cloudflare_reconnect_required" {
			t.set(gen, func(s *connector.Status) { s.State = "needs-login"; s.Code = code; s.LoginURL = ""; s.Endpoints = nil })
			return run{}, err
		}
		if code != "cloudflare_api_failed" && code != "cloudflare_create_failed" && code != "cloudflare_route_failed" && code != "binary_required" {
			t.fail(gen, err)
			return run{}, err
		}
		snapshot := policy.Exited(time.Now(), supervision.Exit{Code: -1, Reason: code})
		t.set(gen, func(s *connector.Status) {
			s.State = string(snapshot.State)
			s.Phase = string(snapshot.State)
			s.Code = code
			s.Supervision = &snapshot
			s.Endpoints = nil
		})
		if supervision.Wait(ctx, time.Until(snapshot.NextRetry)) != nil {
			return run{}, ctx.Err()
		}
	}
	return run{}, ctx.Err()
}
