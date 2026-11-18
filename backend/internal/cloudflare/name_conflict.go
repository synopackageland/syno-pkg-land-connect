package cloudflare

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"dsmapp/internal/connector"
)

type nameClaim struct {
	owned                bool
	name, reason, tunnel string
	record               dnsRecord
	found, deleted       bool
}

func (n nameClaim) fingerprint() string {
	raw, _ := json.Marshal(n.record)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
func (a API) classifyName(ctx context.Context, c cert, name string, own ...string) (nameClaim, error) {
	n := nameClaim{name: name}
	var rows []dnsRecord
	if err := a.get(ctx, c, "/zones/"+c.zoneID+"/dns_records", url.Values{"name": {name}}, &rows); err != nil {
		return n, err
	}
	if len(rows) == 0 {
		return n, nil
	}
	n.found = true
	n.reason = "other"
	if len(rows) != 1 {
		return n, nil
	}
	n.record = rows[0]
	if n.record.ID == "" || n.record.Name != name || n.record.Type != "CNAME" {
		return n, nil
	}
	id, target := strings.CutSuffix(strings.ToLower(strings.TrimSuffix(n.record.Content, ".")), ".cfargotunnel.com")
	if !target || !tunnelUUID.MatchString(id) {
		return n, nil
	}
	n.tunnel = id
	n.owned = n.record.Comment == dnsMarker(id)
	if slices.Contains(own, id) {
		n.reason = ""
		return n, nil
	}
	var tunnel struct {
		ID, Name  string
		DeletedAt *string `json:"deleted_at"`
	}
	err := a.get(ctx, c, "/accounts/"+c.accountID+"/cfd_tunnel/"+id, nil, &tunnel)
	if errors.Is(err, errNotFound) {
		n.reason = "leftover"
		n.deleted = true
		return n, nil
	}
	// Permission failures and unknown replies never prove that a tunnel is deleted or inactive.
	if err != nil || tunnel.ID != id {
		return n, nil
	}
	if tunnel.DeletedAt != nil && *tunnel.DeletedAt != "" {
		n.reason = "leftover"
		n.deleted = true
		return n, nil
	}
	if !strings.HasPrefix(tunnel.Name, "synopkgconnect-") {
		return n, nil
	}
	n.owned = true
	// Tunnel GET no longer includes connections. Use the dedicated endpoint.
	var connections *[]json.RawMessage
	if err = a.get(ctx, c, "/accounts/"+c.accountID+"/cfd_tunnel/"+id+"/connections", nil, &connections); err != nil || connections == nil {
		return n, nil
	}
	n.reason = "leftover"
	if len(*connections) > 0 {
		n.reason = "other_connect"
	}
	return n, nil
}
func (a API) conflict(ctx context.Context, c cert, n nameClaim) error {
	zone, err := a.ZoneName(ctx, c)
	if err != nil {
		return err
	}
	d := connector.NameConflict{ObjectType: "dns", DashboardURL: "https://dash.cloudflare.com/" + c.accountID + "/" + zone + "/dns/records", Reason: n.reason, ConflictName: n.name, DNSURL: "https://dash.cloudflare.com/" + c.accountID + "/" + zone + "/dns/records", TunnelURL: "https://one.dash.cloudflare.com/" + c.accountID + "/networks/connectors"}
	if d.Reason == "" {
		d.Reason = "other"
	}
	if n.reason == "leftover" && n.owned {
		d.ReuseRecord = n.fingerprint()
	}
	base := strings.TrimSuffix(n.name, "."+zone)
	for i := 2; i <= 100; i++ {
		suffix := "-" + strconv.Itoa(i)
		label := strings.TrimRight(base[:min(len(base), 63-len(suffix))], "-") + suffix
		used, e := a.NameInUse(ctx, c, label+"."+zone)
		if e != nil {
			break
		}
		if !used {
			d.SuggestedName = label + "." + zone
			break
		}
	}
	return connector.WithNameConflict(errNameTaken, d)
}
func allowedClaim(n nameClaim, r *connector.NameReuse) bool {
	return !n.found || n.owned
}

// claimName re-reads classification immediately before deleting exactly one DNS
// record. cloudflared's non-overwrite route refuses a competing new record.
func (a API) claimName(ctx context.Context, c cert, observed nameClaim, id string, reuse *connector.NameReuse, route func() error) (string, error) {
	fresh, err := a.classifyName(ctx, c, observed.name, id)
	if err != nil {
		return "", err
	}
	fresh.owned = observed.owned && observed.record == fresh.record && observed.tunnel == fresh.tunnel
	if fresh.found && fresh.reason == "" && !observed.owned {
		fresh.reason = "other"
	}
	if !allowedClaim(fresh, reuse) || observed.found != fresh.found || observed.record != fresh.record {
		return "", a.conflict(ctx, c, fresh)
	}
	if fresh.found && fresh.tunnel == id {
		return "", nil
	}
	if fresh.found {
		var result struct{ ID string }
		if err = a.request(ctx, c, http.MethodDelete, "/zones/"+c.zoneID+"/dns_records/"+fresh.record.ID, nil, &result); err != nil {
			fresh.reason = "other"
			return "", a.conflict(ctx, c, fresh)
		}
		if result.ID != fresh.record.ID {
			return "", errRoute
		}
	}
	if err = route(); err != nil {
		return "", err
	}
	return "", nil
}
func (t *Tunnel) nameClaims(ctx context.Context, c cert, names []string) ([]nameClaim, error) {
	own := ""
	if d, e := t.savedDomain(); e == nil {
		own = d.TunnelID
	}
	claims := []nameClaim{}
	for _, name := range names {
		n, e := t.classifyName(ctx, c, name, own)
		if e != nil {
			return nil, e
		}
		if !allowedClaim(n, connector.ReuseFor(ctx, name)) {
			return nil, t.cfg.API.conflict(ctx, c, n)
		}
		claims = append(claims, n)
	}
	return claims, nil
}
