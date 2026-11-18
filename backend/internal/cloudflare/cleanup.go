package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
)

// Cleanup is restricted to replaced identities. Never enumerate and delete
// unrelated Connect installations, trust a DNS marker as deletion authority,
// or force a tunnel with live connectors down.
func (a API) deleteRetiredTunnel(ctx context.Context, c cert, id string) error {
	path := "/accounts/" + c.accountID + "/cfd_tunnel/" + id
	var remote struct {
		ID, Name  string
		DeletedAt *string `json:"deleted_at"`
	}
	if err := a.get(ctx, c, path, nil, &remote); errors.Is(err, errNotFound) {
		return nil
	} else if err != nil {
		return err
	}
	if remote.ID != id || !strings.HasPrefix(remote.Name, "synopkgconnect-") {
		return errRoute
	}
	if remote.DeletedAt != nil && *remote.DeletedAt != "" {
		return nil
	}
	var connections *[]json.RawMessage
	if err := a.get(ctx, c, path+"/connections", nil, &connections); err != nil {
		return err
	}
	if connections == nil || len(*connections) > 0 {
		return errRoute
	}
	var result json.RawMessage
	err := a.request(ctx, c, http.MethodDelete, path, nil, &result)
	if errors.Is(err, errNotFound) {
		return nil
	}
	return err
}

// A durable queue retries cleanup on reconnect after DNS and the new private
// identity are committed. Cleanup failures remain visible without losing service.
func (t *Tunnel) cleanupRetired(ctx context.Context, c cert, current string) {
	v, err := t.ownership(c.accountID)
	if err != nil {
		t.cleanupResult(c, true)
		return
	}
	if len(v.Retired) == 0 {
		return
	}
	kept := false
	for id := range v.Retired {
		if id == current {
			continue
		}
		if err = t.cfg.API.deleteRetiredTunnel(ctx, c, id); err != nil {
			kept = true
			continue
		}
		delete(v.Retired, id)
	}
	if err = t.saveOwnership(v); err != nil {
		kept = true
	}
	t.cleanupResult(c, kept)
}

func (t *Tunnel) cleanupResult(c cert, kept bool) {
	t.smu.Lock()
	defer t.smu.Unlock()
	t.cleanupNote, t.cleanupURL = "", ""
	if kept {
		t.cleanupNote = "cloudflare_old_tunnel_kept"
		t.cleanupURL = "https://one.dash.cloudflare.com/" + c.accountID + "/networks/connectors"
	}
}
