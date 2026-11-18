package workers

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/textproto"
	"net/url"
	"slices"
	"strconv"
	"time"

	"dsmapp/internal/connector"
)

// SignalBridge is the Worker that carries the self-hosted NetBird server's
// Signal stream, and its stream and static landing routes on the user's zone.
type SignalBridge struct {
	RouteIDs map[string]string `json:"route_ids"`
	Zone     string            `json:"zone"`
	Host     string            `json:"host"`
	ZoneID   string            `json:"zone_id,omitempty"`
	Script   string            `json:"script"`
}

const bridgeTag = "synopackageland-connect-signal:"

// Signal's two-way stream and the exact root page use the Worker. API, OIDC,
// gRPC and the WebSocket opened back to the same name stay on the tunnel.
func bridgePattern(host string) string {
	return host + "/signalexchange.SignalExchange/ConnectStream*"
}

func bridgePatterns(host string) []string {
	return []string{bridgePattern(host), host + "/"}
}

func bridgeScript(host string) string {
	sum := sha256.Sum256([]byte(host))
	return "connect-nbsignal-" + hex.EncodeToString(sum[:6])
}

// bridgeVersion names the bundled script, so a Connect update replaces it.
func (a API) bridgeVersion() string {
	sum := sha256.Sum256(a.Bridge)
	return hex.EncodeToString(sum[:6])
}

// EnsureSignalBridge deploys the bridge for host on zone with the saved
// Workers token, or brings it up to date. It only uploads when the script is
// missing or older than the bundled one, and adds the route when missing.
func (m *Manager) EnsureSignalBridge(ctx context.Context, zone, host string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, err := m.Store.load()
	if err != nil {
		return failure("workers_state_corrupt")
	}
	if v.Token == "" || v.Disconnecting {
		return failure("workers_not_connected")
	}
	if len(m.API.Bridge) == 0 {
		return failure("workers_bundle_missing")
	}
	if b := v.SignalBridge; b != nil && (b.Host != host || b.Zone != zone) {
		if err = m.removeBridge(ctx, &v); err != nil {
			return err
		}
	}
	zoneID, err := m.API.zone(ctx, v, zone)
	if err != nil {
		return err
	}
	// A token that cannot add the route uploads nothing.
	routes, err := m.API.routes(ctx, v, zoneID)
	if err != nil {
		return err
	}
	name := bridgeScript(host)
	if v.SignalBridge != nil {
		name = v.SignalBridge.Script
	}
	tags, found, err := m.API.scriptTags(ctx, v, name)
	if err != nil {
		return err
	}
	if found && !slices.Contains(tags, "synopackageland-connect:"+v.Owner) {
		alternative := ""
		for i := 2; i <= 100; i++ {
			candidate := bridgeScript(host) + "-" + strconv.Itoa(i)
			_, exists, e := m.API.scriptTags(ctx, v, candidate)
			if e != nil {
				return e
			}
			if !exists {
				alternative = candidate
				break
			}
		}
		if !connector.AnotherName(ctx) || alternative == "" {
			e := bridgeConflict("workers_name_conflict", "worker", name, v.AccountID, zone)
			detail := *connector.Conflict(e)
			detail.SuggestedName = alternative
			return connector.WithNameConflict(e, detail)
		}
		name = alternative
	}
	for _, r := range routes {
		if slices.Contains(bridgePatterns(host), r.Pattern) && r.Script != name {
			return bridgeConflict("workers_route_conflict", "route", r.Pattern, v.AccountID, zone)
		}
	}
	b := SignalBridge{Zone: zone, Host: host, ZoneID: zoneID, Script: name, RouteIDs: map[string]string{}}
	if old := v.SignalBridge; old != nil && old.Script == name {
		b.RouteIDs = old.RouteIDs
	}
	v.SignalBridge = &b
	if m.Store.save(v) != nil {
		return failure("workers_state_corrupt")
	}
	if err = m.API.deployBridge(ctx, v, b.Script); err != nil {
		return err
	}
	return m.ensureBridgeRoutes(ctx, &v, &b)
}

// A refusal is asked again soon, so a permission added to the same token in
// Cloudflare's dashboard shows up quickly; a pass holds longer.
const (
	checkRefusal = 15 * time.Second
	checkPass    = 10 * time.Minute
)

type bridgeCheck struct {
	key   [32]byte
	until time.Time
	err   error
}

// CheckSignalBridge tells, without deploying anything, whether the saved
// token can put the bridge on zone: it finds the zone and reads its Worker
// routes. The NetBird page asks on every status poll, so the answer is kept
// for the token and zone; a Cloudflare failure is not kept.
func (m *Manager) CheckSignalBridge(ctx context.Context, zone string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, err := m.Store.load()
	if err != nil {
		return failure("workers_state_corrupt")
	}
	if v.Token == "" || v.Disconnecting {
		return failure("workers_not_connected")
	}
	now := time.Now()
	if m.clock != nil {
		now = m.clock()
	}
	key := sha256.Sum256([]byte(v.Token + "\x00" + zone))
	if c := m.checked; c.key == key && now.Before(c.until) {
		return c.err
	}
	zoneID, err := m.API.zone(ctx, v, zone)
	if err == nil {
		_, err = m.API.routes(ctx, v, zoneID)
	}
	switch {
	case err == nil:
		m.checked = bridgeCheck{key, now.Add(checkPass), nil}
	case Refusal(err):
		m.checked = bridgeCheck{key, now.Add(checkRefusal), err}
	}
	return err
}

// Refusal reports a check failure that only a token change fixes.
func Refusal(err error) bool {
	switch connector.ErrorCode(err) {
	case "workers_not_connected", "workers_routes_permission", "workers_zone_account", "workers_token_invalid":
		return true
	}
	return false
}

// RemoveSignalBridge deletes the bridge's route and script, when this
// installation made them.
func (m *Manager) RemoveSignalBridge(ctx context.Context) error {
	if m.CheckBridgeRemoval != nil {
		if e := m.CheckBridgeRemoval(); e != nil {
			return e
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	v, err := m.Store.load()
	if err != nil {
		return failure("workers_state_corrupt")
	}
	return m.removeBridge(ctx, &v)
}

func (m *Manager) removeBridge(ctx context.Context, v *privateState) error {
	b := v.SignalBridge
	if b == nil || v.Token == "" {
		return nil
	}
	tags, found, err := m.API.scriptTags(ctx, *v, b.Script)
	if err != nil {
		return err
	}
	if found && !slices.Contains(tags, "synopackageland-connect:"+v.Owner) {
		// A failed name claim or externally replaced script grants no authority
		// over that script or the routes pointing at it. Forget only our claim.
		v.SignalBridge = nil
		if m.Store.save(*v) != nil {
			return failure("workers_state_corrupt")
		}
		return nil
	}
	// Deleting an owned script also removes its routes. If it is already
	// missing, route cleanup still uses the retained installation inventory.
	if b.ZoneID == "" {
		id, e := m.API.zone(ctx, *v, b.Zone)
		if e != nil {
			return e
		}
		b.ZoneID = id
	}
	routes, err := m.API.routes(ctx, *v, b.ZoneID)
	if err != nil {
		return err
	}
	for _, r := range routes {
		if r.Script == b.Script && (!slices.Contains(bridgePatterns(b.Host), r.Pattern) || b.RouteIDs != nil && b.RouteIDs[r.Pattern] != r.ID) {
			return bridgeConflict("workers_route_conflict", "route", r.Pattern, v.AccountID, b.Zone)
		}
	}
	if err = m.removeRoute(ctx, v, b); err != nil {
		return err
	}
	if found && slices.Contains(tags, "synopackageland-connect:"+v.Owner) {
		err = m.API.json(ctx, *v, "DELETE", "/workers/scripts/"+b.Script, nil, nil)
		if err != nil && !missing(err) {
			return sanitize(err)
		}
	}
	v.SignalBridge = nil
	if m.Store.save(*v) != nil {
		return failure("workers_state_corrupt")
	}
	return nil
}

func (m *Manager) removeRoute(ctx context.Context, v *privateState, b *SignalBridge) error {
	if b.ZoneID == "" {
		id, err := m.API.zone(ctx, *v, b.Zone)
		if err != nil {
			return err
		}
		b.ZoneID = id
	}
	routes, err := m.API.routes(ctx, *v, b.ZoneID)
	if err != nil {
		return err
	}
	for _, r := range routes {
		if slices.Contains(bridgePatterns(b.Host), r.Pattern) && r.Script == b.Script && (b.RouteIDs == nil || b.RouteIDs[r.Pattern] == r.ID) {
			err = m.API.call(ctx, v.Token, "DELETE", "/zones/"+b.ZoneID+"/workers/routes/"+r.ID, "", nil, nil)
			if err != nil && !missing(err) {
				return routeFailure(err)
			}
		}
	}
	return nil
}

// routeFailure names the zone permission the Workers token also needs.
func routeFailure(err error) error {
	var e *apiError
	if errors.As(err, &e) && e.status == 403 {
		return failure("workers_routes_permission")
	}
	return sanitize(err)
}

// zone finds the zone's ID. A token without a permission on the zone does
// not see it.
func (a API) zone(ctx context.Context, v privateState, name string) (string, error) {
	var zones []struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
	}
	if err := a.call(ctx, v.Token, "GET", "/zones?name="+url.QueryEscape(name), "", nil, &zones); err != nil {
		return "", routeFailure(err)
	}
	for _, z := range zones {
		if z.Name != name {
			continue
		}
		if z.Account.ID != v.AccountID {
			return "", failure("workers_zone_account")
		}
		return z.ID, nil
	}
	return "", failure("workers_routes_permission")
}

type route struct {
	ID      string `json:"id"`
	Pattern string `json:"pattern"`
	Script  string `json:"script"`
}

func (a API) routes(ctx context.Context, v privateState, zoneID string) ([]route, error) {
	var out []route
	if err := a.call(ctx, v.Token, "GET", "/zones/"+zoneID+"/workers/routes", "", nil, &out); err != nil {
		return nil, routeFailure(err)
	}
	return out, nil
}

func (m *Manager) ensureBridgeRoutes(ctx context.Context, state *privateState, b *SignalBridge) error {
	a, v := m.API, *state
	routes, err := a.routes(ctx, v, b.ZoneID)
	if err != nil {
		return err
	}
	legacy := b.RouteIDs == nil
	if b.RouteIDs == nil {
		b.RouteIDs = map[string]string{}
	}
	for _, pattern := range bridgePatterns(b.Host) {
		found := false
		for _, r := range routes {
			if r.Pattern == pattern {
				if r.Script != b.Script {
					return bridgeConflict("workers_route_conflict", "route", r.Pattern, v.AccountID, b.Zone)
				}
				if id := b.RouteIDs[pattern]; id != r.ID && (id != "" || !legacy) {
					return bridgeConflict("workers_route_conflict", "route", pattern, v.AccountID, b.Zone)
				}
				b.RouteIDs[pattern] = r.ID
				found = true
			}
		}
		if found {
			state.SignalBridge = b
			if m.Store.save(*state) != nil {
				return failure("workers_state_corrupt")
			}
			continue
		}
		raw, _ := json.Marshal(map[string]string{"pattern": pattern, "script": b.Script})
		var created route
		err = a.call(ctx, v.Token, "POST", "/zones/"+b.ZoneID+"/workers/routes", "application/json", raw, &created)
		var e *apiError
		if errors.As(err, &e) && e.status == 409 {
			return bridgeConflict("workers_route_conflict", "route", pattern, v.AccountID, b.Zone)
		}
		if err != nil {
			return routeFailure(err)
		}
		if created.ID == "" {
			return failure("workers_api_refused")
		}
		b.RouteIDs[pattern] = created.ID
		state.SignalBridge = b
		if m.Store.save(*state) != nil {
			return failure("workers_state_corrupt")
		}
	}
	return nil
}

// scriptTags reads a script's tags; found is false when there is none.
func (a API) scriptTags(ctx context.Context, v privateState, name string) (tags []string, found bool, err error) {
	var out struct {
		Tags []string `json:"tags"`
	}
	err = a.json(ctx, v, "GET", "/workers/scripts/"+name+"/settings", nil, &out)
	if missing(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, sanitize(err)
	}
	return out.Tags, true, nil
}

func (a API) deployBridge(ctx context.Context, v privateState, name string) error {
	tags, found, err := a.scriptTags(ctx, v, name)
	if err != nil {
		return err
	}
	if found && !slices.Contains(tags, "synopackageland-connect:"+v.Owner) {
		return bridgeConflict("workers_name_conflict", "worker", name, v.AccountID, "")
	}
	version := bridgeTag + a.bridgeVersion()
	if found && slices.Contains(tags, version) {
		return nil
	}
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	metadata := map[string]any{"main_module": "bridge.js", "compatibility_date": "2026-10-24", "compatibility_flags": []string{"enable_request_signal"}, "tags": []string{"synopackageland-connect:" + v.Owner, version}, "observability": map[string]bool{"enabled": false}, "bindings": []any{}}
	raw, _ := json.Marshal(metadata)
	p, err := w.CreateFormField("metadata")
	if err != nil {
		return failure("workers_unavailable")
	}
	p.Write(raw)
	p, err = w.CreatePart(textproto.MIMEHeader{"Content-Disposition": []string{`form-data; name="bridge.js"; filename="bridge.js"`}, "Content-Type": []string{"application/javascript+module"}})
	if err != nil {
		return failure("workers_unavailable")
	}
	p.Write(a.Bridge)
	w.Close()
	if err = a.call(ctx, v.Token, "PUT", "/accounts/"+v.AccountID+"/workers/scripts/"+name, w.FormDataContentType(), b.Bytes(), nil); err != nil {
		return sanitize(err)
	}
	// The bridge answers only on the user's own name.
	return sanitize(a.json(ctx, v, "POST", "/workers/scripts/"+name+"/subdomain", map[string]bool{"enabled": false, "previews_enabled": false}, nil))
}

// ObserveSignalBridge is read-only: both expected routes and the owned Worker
// must still exist. A private inventory entry alone is not evidence of health.
func (m *Manager) ObserveSignalBridge(ctx context.Context, zone, host string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, e := m.Store.load()
	if e != nil {
		return failure("workers_state_corrupt")
	}
	if v.Token == "" {
		return failure("workers_not_connected")
	}
	b := v.SignalBridge
	missingBridge := func() error { return failure("netbird_server_bridge_missing") }
	if b == nil || b.Host != host || b.Zone != zone {
		return missingBridge()
	}
	tags, found, e := m.API.scriptTags(ctx, v, b.Script)
	if e != nil {
		return e
	}
	if !found || !slices.Contains(tags, "synopackageland-connect:"+v.Owner) {
		return missingBridge()
	}
	zoneID, e := m.API.zone(ctx, v, zone)
	if e != nil {
		return e
	}
	routes, e := m.API.routes(ctx, v, zoneID)
	if e != nil {
		return e
	}
	for _, pattern := range bridgePatterns(host) {
		found := false
		for _, r := range routes {
			if r.Pattern == pattern && r.Script == b.Script {
				found = true
				break
			}
		}
		if !found {
			return missingBridge()
		}
	}
	return nil
}
