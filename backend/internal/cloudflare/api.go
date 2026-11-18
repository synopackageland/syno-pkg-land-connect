package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"dsmapp/internal/connector"
)

// ErrPermissionDenied names a sign-in that may not read the zone or its DNS.
var ErrPermissionDenied = connector.Coded("cloudflare_permission_denied", "the Cloudflare sign-in may not manage this domain")
var errNotFound = errors.New("Cloudflare resource not found")
var errAPI = connector.Coded("cloudflare_api_failed", "Cloudflare did not answer as expected")

// API reads the signed-in zone through Cloudflare's REST API with the
// certificate's token. Confirmed recovery can delete one verified record and
// an inactive Connect tunnel; cloudflared itself creates resources.
type API struct{ Base string }

func (a API) base() string {
	if a.Base == "" {
		return "https://api.cloudflare.com/client/v4"
	}
	return a.Base
}

// validDomain reports whether host is a DNS name of at least two labels.
func validDomain(host string) bool {
	labels := strings.Split(host, ".")
	if len(host) > 253 || len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if !dnsLabel.MatchString(label) {
			return false
		}
	}
	return true
}

// get decodes a successful reply's result. The reply body is never returned
// or logged: it may describe the account.
func (a API) get(ctx context.Context, c cert, path string, query url.Values, result any) error {
	return a.request(ctx, c, http.MethodGet, path, query, result)
}

func (a API) request(ctx context.Context, c cert, method, path string, query url.Values, result any) error {
	return a.requestJSON(ctx, c, method, path, query, nil, result)
}

func (a API) requestJSON(ctx context.Context, c cert, method, path string, query url.Values, body any, result any) error {
	u := a.base() + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var data []byte
	if body != nil {
		var err error
		data, err = json.Marshal(body)
		if err != nil {
			return errAPI
		}
	}
	req, e := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(data))
	if e != nil {
		return errAPI
	}
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errAPI }}
	res, e := client.Do(req)
	if e != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return ctx.Err()
		}
		return errAPI
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		return ErrPermissionDenied
	}
	if res.StatusCode == http.StatusNotFound {
		return errNotFound
	}
	var reply struct {
		Success bool            `json:"success"`
		Result  json.RawMessage `json:"result"`
	}
	if res.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&reply) != nil || !reply.Success || json.Unmarshal(reply.Result, result) != nil {
		return errAPI
	}
	return nil
}

// ZoneName returns the domain the user chose while signing in. The zone must
// belong to the certificate's own account.
func (a API) ZoneName(ctx context.Context, c cert) (string, error) {
	var zone struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Account struct {
			ID string `json:"id"`
		} `json:"account"`
	}
	if e := a.get(ctx, c, "/zones/"+c.zoneID, nil, &zone); e != nil {
		return "", e
	}
	if zone.ID != c.zoneID || zone.Account.ID != c.accountID || !validDomain(zone.Name) {
		return "", errAPI
	}
	return zone.Name, nil
}

// NameInUse reports whether the zone already has any DNS record named name.
// cloudflared's own route also refuses an existing name; this check lets the
// user choose another name before a tunnel is created.
func (a API) NameInUse(ctx context.Context, c cert, name string) (bool, error) {
	var records []json.RawMessage
	if e := a.get(ctx, c, "/zones/"+c.zoneID+"/dns_records", url.Values{"name": {name}}, &records); e != nil {
		return false, e
	}
	return len(records) > 0, nil
}
