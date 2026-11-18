package cloudflare

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type dnsRecord struct {
	ID, Name, Type, Content string
	Proxied                 bool
	Comment                 string `json:"comment,omitempty"`
}

func (a API) FindRoute(ctx context.Context, c cert, name, id string) (dnsRecord, bool, error) {
	var rows []dnsRecord
	if e := a.get(ctx, c, "/zones/"+c.zoneID+"/dns_records", url.Values{"name": {name}}, &rows); e != nil {
		return dnsRecord{}, false, e
	}
	if len(rows) == 0 {
		return dnsRecord{}, false, nil
	}
	if len(rows) != 1 {
		return dnsRecord{}, false, errRoute
	}
	r := rows[0]
	if r.ID == "" || r.Name != name || r.Type != "CNAME" || strings.TrimSuffix(r.Content, ".") != id+".cfargotunnel.com" || !r.Proxied {
		return dnsRecord{}, false, errRoute
	}
	return r, true, nil
}
func (a API) RouteRecord(ctx context.Context, c cert, name, id string) (dnsRecord, error) {
	r, found, e := a.FindRoute(ctx, c, name, id)
	if e != nil {
		return r, e
	}
	if !found {
		return r, errRoute
	}
	return r, nil
}

func (a API) DeleteRoute(ctx context.Context, c cert, r dnsRecord, id string) error {
	fresh, e := a.RouteRecord(ctx, c, r.Name, id)
	if e != nil || fresh != r {
		return errUpdateRecovery
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodDelete, a.base()+"/zones/"+c.zoneID+"/dns_records/"+r.ID, nil)
	if e != nil {
		return errUpdateRecovery
	}
	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	client := http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errAPI }}
	res, e := client.Do(req)
	if e != nil {
		return errUpdateRecovery
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return errUpdateRecovery
	}
	_, found, e := a.FindRoute(ctx, c, r.Name, id)
	if e != nil || found {
		return errUpdateRecovery
	}
	return nil
}
