package cloudflare

import (
	"context"
	"dsmapp/internal/connector"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Ownership survives domain-mode changes and Forget. The private journal
// complements remote Connect markers and queues only replaced tunnel IDs.
// An inactive tunnel without either kind of ownership evidence grants nothing.
type ownership struct {
	Account string               `json:"account"`
	Tunnels map[string]bool      `json:"tunnels"`
	DNS     map[string]dnsRecord `json:"dns"`
	Retired map[string]bool      `json:"retired,omitempty"`
}
type ownershipJournal struct {
	Accounts map[string]ownership `json:"accounts"`
}

func (t *Tunnel) journal() (ownershipJournal, error) {
	j := ownershipJournal{Accounts: map[string]ownership{}}
	raw, e := connector.ReadPrivate(filepath.Join(t.cfg.Dir, "ownership.json"))
	if errors.Is(e, os.ErrNotExist) {
		return j, nil
	}
	if e != nil || json.Unmarshal(raw, &j) != nil || j.Accounts == nil {
		return ownershipJournal{}, errSave
	}
	return j, nil
}
func (t *Tunnel) ownership(account string) (ownership, error) {
	j, e := t.journal()
	if e != nil {
		return ownership{}, e
	}
	if v, ok := j.Accounts[account]; ok {
		if v.Account != account || v.Tunnels == nil || v.DNS == nil {
			return ownership{}, errSave
		}
		return v, nil
	}
	return ownership{Account: account, Tunnels: map[string]bool{}, DNS: map[string]dnsRecord{}}, nil
}
func (t *Tunnel) saveOwnership(v ownership) error {
	j, e := t.journal()
	if e != nil {
		return e
	}
	j.Accounts[v.Account] = v
	raw, _ := json.Marshal(j)
	if e = connector.WritePrivate(filepath.Join(t.cfg.Dir, "ownership.json"), raw); e != nil {
		return storageError(e)
	}
	return nil
}

func (t *Tunnel) forgetOwnedRoute(c cert, name string) error {
	v, err := t.ownership(c.accountID)
	if err != nil {
		return err
	}
	delete(v.DNS, name)
	return t.saveOwnership(v)
}

// cleanupUnroutedOwnership reclaims records Connect can prove it created but
// which no current domain route ledger references. Changed or foreign records
// fail closed and remain visible for a later reconciliation.
func (t *Tunnel) cleanupUnroutedOwnership(ctx context.Context, d domain) error {
	c, err := readCert(t.certFile())
	if err != nil {
		return errReconnect
	}
	v, err := t.ownership(c.accountID)
	if err != nil {
		return err
	}
	changed := false
	for name, expected := range v.DNS {
		if name == d.Hostname || slices.Contains(d.Routed, name) {
			continue
		}
		tunnelID := strings.TrimSuffix(expected.Content, ".cfargotunnel.com")
		if !v.Tunnels[tunnelID] || !tunnelUUID.MatchString(tunnelID) || expected.Comment != dnsMarker(tunnelID) {
			continue
		}
		current, found, findErr := t.cfg.API.FindRoute(ctx, c, name, tunnelID)
		if findErr != nil {
			return findErr
		}
		if found {
			if current != expected {
				continue
			}
			if err = t.cfg.API.DeleteRoute(ctx, c, current, tunnelID); err != nil {
				return err
			}
		}
		delete(v.DNS, name)
		changed = true
	}
	if changed {
		return t.saveOwnership(v)
	}
	return nil
}
func (t *Tunnel) rememberTunnel(c credentials) error {
	v, e := t.ownership(c.account)
	if e != nil {
		return e
	}
	v.Tunnels[c.tunnelID] = true
	return t.saveOwnership(v)
}
func (t *Tunnel) rememberRoute(ctx context.Context, c cert, name, id string) error {
	v, e := t.ownership(c.accountID)
	if e != nil {
		return e
	}
	if !v.Tunnels[id] {
		raw, e := connector.ReadPrivate(t.domainFile("credentials.json"))
		if e != nil {
			return nil
		}
		credentials, e := parseCredentials(raw)
		if e != nil || credentials.account != c.accountID || credentials.tunnelID != id {
			return nil
		}
		v.Tunnels[id] = true
	}
	row, found, e := t.cfg.API.FindRoute(ctx, c, name, id)
	if e != nil {
		return e
	}
	if !found {
		return nil
	}
	if row.Comment != dnsMarker(id) {
		var marked dnsRecord
		if e = t.cfg.API.requestJSON(ctx, c, http.MethodPatch, "/zones/"+c.zoneID+"/dns_records/"+row.ID, nil, map[string]string{"comment": dnsMarker(id)}, &marked); e != nil {
			return e
		}
		row.Comment = dnsMarker(id)
		if marked != row {
			return errRoute
		}
		fresh, found, err := t.cfg.API.FindRoute(ctx, c, name, id)
		if err != nil {
			return err
		}
		if !found || fresh != row {
			return errRoute
		}
	}
	v.DNS[name] = row
	return t.saveOwnership(v)
}

// The remote marker also survives loss of the installation's private journal.
func dnsMarker(id string) string { return "synopkgconnect:tunnel=" + id }

func (t *Tunnel) retireTunnel(c cert, id string) error {
	if !tunnelUUID.MatchString(id) {
		return nil
	}
	v, err := t.ownership(c.accountID)
	if err != nil {
		return err
	}
	if v.Retired == nil {
		v.Retired = map[string]bool{}
	}
	v.Retired[id] = true
	return t.saveOwnership(v)
}
func (t *Tunnel) classifyName(ctx context.Context, c cert, name string, own ...string) (nameClaim, error) {
	n, e := t.cfg.API.classifyName(ctx, c, name, own...)
	if e != nil {
		return n, e
	}
	v, e := t.ownership(c.accountID)
	if e != nil {
		return n, e
	}
	n.owned = n.owned || n.found && v.Tunnels[n.tunnel]
	if n.owned {
		return n, nil
	}
	if n.found && n.reason == "" && !n.owned {
		// Migrate only names already present in the installation inventory and
		// bound to the retained private tunnel credentials, never any CNAME that
		// merely happens to point at that tunnel.
		if d, e := t.savedDomain(); e == nil && d.TunnelID == n.tunnel {
			raw, e := connector.ReadPrivate(t.domainFile("credentials.json"))
			if e == nil {
				credentials, e := parseCredentials(raw)
				if e == nil && credentials.account == c.accountID && credentials.tunnelID == n.tunnel && (d.Hostname == name || slices.Contains(d.Routed, name)) {
					n.owned = true
				}
			}
		}
		if !n.owned {
			n.reason = "other"
		}
	}
	return n, nil
}
