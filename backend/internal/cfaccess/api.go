// Package cfaccess owns the optional Cloudflare email gate. API credentials,
// desired emails and remote object identifiers stay in package-private state.
package cfaccess

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

	"dsmapp/internal/accounttoken"
	"dsmapp/internal/connector"
)

type Credential = accounttoken.Credential

type API struct {
	Base string
	HTTP *http.Client
}
type apiError struct {
	code   string
	status int
	retry  time.Duration
}

func (e *apiError) Error() string { return "Cloudflare Access request unavailable" }
func Code(err error) string {
	if err == nil {
		return ""
	}
	var e *apiError
	if errors.As(err, &e) {
		return e.code
	}
	if c := connector.ErrorCode(err); c != "" {
		return c
	}
	return "access_unavailable"
}
func failure(code string) error { return &apiError{code: code} }

type Policy struct {
	ID         string           `json:"id,omitempty"`
	Name       string           `json:"name"`
	Decision   string           `json:"decision"`
	Precedence int              `json:"precedence"`
	Include    []map[string]any `json:"include"`
	Exclude    []map[string]any `json:"exclude"`
	Require    []map[string]any `json:"require"`
}
type Application struct {
	ID           string           `json:"id,omitempty"`
	AUD          string           `json:"aud,omitempty"`
	Name         string           `json:"name"`
	Type         string           `json:"type"`
	Domain       string           `json:"domain"`
	AllowedIDPs  []string         `json:"allowed_idps"`
	AutoRedirect bool             `json:"auto_redirect_to_identity"`
	Launcher     bool             `json:"app_launcher_visible"`
	Session      string           `json:"session_duration"`
	Policies     []Policy         `json:"policies"`
	Destinations []map[string]any `json:"destinations,omitempty"`
	AllowWARP    bool             `json:"allow_authenticate_via_warp,omitempty"`
}

// An optional duration is not evidence for the security policy. Ignore an
// unreadable field while retaining strict decoding of the rest of the app.
func (a *Application) UnmarshalJSON(raw []byte) error {
	type plain Application
	var wire struct {
		*plain
		Session json.RawMessage `json:"session_duration"`
	}
	wire.plain = (*plain)(a)
	if err := json.Unmarshal(raw, &wire); err != nil {
		return err
	}
	a.Session = ""
	_ = json.Unmarshal(wire.Session, &a.Session)
	return nil
}

type IdentityProvider struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}
type Organization struct {
	Domain string `json:"auth_domain"`
}
type inventory struct {
	Organization Organization
	Apps         []Application
	IDPs         []IdentityProvider
}
type envelope struct {
	Success bool            `json:"success"`
	Result  json.RawMessage `json:"result"`
	Info    struct {
		Page       int `json:"page"`
		TotalPages int `json:"total_pages"`
	} `json:"result_info"`
}

func (a API) call(ctx context.Context, c Credential, method, path string, body, out any) (envelope, error) {
	var env envelope
	base := a.Base
	if base == "" {
		base = "https://api.cloudflare.com/client/v4"
	}
	var raw []byte
	var err error
	if body != nil {
		raw, err = json.Marshal(body)
		if err != nil {
			return env, failure("access_invalid_request")
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, base+path, bytes.NewReader(raw))
	if err != nil {
		return env, failure("access_unavailable")
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	client := http.Client{Timeout: 15 * time.Second}
	if a.HTTP != nil {
		client = *a.HTTP
		if client.Timeout == 0 {
			client.Timeout = 15 * time.Second
		}
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := client.Do(req)
	if err != nil {
		return env, failure("access_unavailable")
	}
	defer res.Body.Close()
	raw, err = io.ReadAll(io.LimitReader(res.Body, 2<<20+1))
	if err != nil || len(raw) > 2<<20 {
		return env, failure("access_unavailable")
	}
	if json.Unmarshal(raw, &env) != nil || !env.Success || res.StatusCode < 200 || res.StatusCode >= 300 {
		code := "access_api_refused"
		switch res.StatusCode {
		case 400:
			if path == "/user/tokens/verify" {
				code = "access_token_invalid"
			}
		case 401:
			code = "access_token_invalid"
		case 403:
			code = "access_permission_denied"
		case 404:
			code = "access_not_found"
		case 429:
			code = "access_rate_limited"
		default:
			if res.StatusCode >= 500 {
				code = "access_unavailable"
			}
		}
		retry := time.Duration(0)
		if seconds, e := time.ParseDuration(res.Header.Get("Retry-After") + "s"); e == nil {
			retry = seconds
		} else if at, e := http.ParseTime(res.Header.Get("Retry-After")); e == nil {
			retry = time.Until(at)
		}
		return env, &apiError{code: code, status: res.StatusCode, retry: retry}
	}
	if out != nil && (len(env.Result) == 0 || string(env.Result) == "null" || json.Unmarshal(env.Result, out) != nil) {
		return env, failure("access_api_refused")
	}
	return env, nil
}
func accountPath(c Credential) string { return "/accounts/" + url.PathEscape(c.Account) + "/access" }
func list[T any](ctx context.Context, a API, c Credential, path string) ([]T, error) {
	out := []T{}
	for page := 1; page <= 100; page++ {
		var rows []T
		env, err := a.call(ctx, c, "GET", path+"?per_page=100&page="+fmtInt(page), nil, &rows)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
		if len(out) > 10000 {
			return nil, failure("access_inventory_incomplete")
		}
		if env.Info.TotalPages > 0 {
			if page >= env.Info.TotalPages {
				return out, nil
			}
		} else if len(rows) < 100 {
			return out, nil
		}
	}
	return nil, failure("access_inventory_incomplete")
}
func (a API) Apps(ctx context.Context, c Credential) ([]Application, error) {
	rows, e := list[Application](ctx, a, c, accountPath(c)+"/apps")
	seen := map[string]bool{}
	for _, v := range rows {
		if v.ID == "" || seen[v.ID] {
			return nil, failure("access_inventory_incomplete")
		}
		seen[v.ID] = true
	}
	return rows, e
}
func (a API) IDPs(ctx context.Context, c Credential) ([]IdentityProvider, error) {
	rows, e := list[IdentityProvider](ctx, a, c, accountPath(c)+"/identity_providers")
	seen := map[string]bool{}
	for _, v := range rows {
		if v.ID == "" || v.Type == "" || seen[v.ID] {
			return nil, failure("access_inventory_incomplete")
		}
		seen[v.ID] = true
	}
	return rows, e
}
func (a API) GetApp(ctx context.Context, c Credential, id string) (Application, error) {
	var v Application
	_, e := a.call(ctx, c, "GET", accountPath(c)+"/apps/"+url.PathEscape(id), nil, &v)
	if e == nil && v.ID != id {
		return v, failure("access_readback_failed")
	}
	return v, e
}
func (a API) SaveApp(ctx context.Context, c Credential, v Application) (Application, error) {
	path := accountPath(c) + "/apps"
	method := "POST"
	if v.ID != "" {
		path += "/" + url.PathEscape(v.ID)
		method = "PUT"
	}
	v.ID = ""
	v.AUD = ""
	var out Application
	_, err := a.call(ctx, c, method, path, v, &out)
	return out, err
}
func (a API) DeleteApp(ctx context.Context, c Credential, id string) error {
	_, err := a.call(ctx, c, "DELETE", accountPath(c)+"/apps/"+url.PathEscape(id), nil, nil)
	return err
}
func (a API) Revoke(ctx context.Context, c Credential, id string) error {
	_, err := a.call(ctx, c, "POST", accountPath(c)+"/apps/"+url.PathEscape(id)+"/revoke_tokens", nil, nil)
	return err
}
func (a API) CreateOTP(ctx context.Context, c Credential, name string) (IdentityProvider, error) {
	var out IdentityProvider
	_, err := a.call(ctx, c, "POST", accountPath(c)+"/identity_providers", map[string]any{"name": name, "type": "onetimepin", "config": map[string]any{}}, &out)
	return out, err
}
func (a API) DeleteOTP(ctx context.Context, c Credential, id string) error {
	_, err := a.call(ctx, c, "DELETE", accountPath(c)+"/identity_providers/"+url.PathEscape(id), nil, nil)
	return err
}

// Permissions are optional self-introspection, never a request for additional
// API Tokens permissions. verify alone says active, not which scopes exist.
func (a API) permissions(ctx context.Context, c Credential) (bool, error) {
	var verified struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if _, e := a.call(ctx, c, "GET", "/user/tokens/verify", nil, &verified); e != nil {
		return false, e
	}
	if verified.Status != "active" || verified.ID == "" {
		return false, failure("access_token_invalid")
	}
	var details struct {
		Policies []struct {
			Effect    string         `json:"effect"`
			Resources map[string]any `json:"resources"`
			Groups    []struct {
				Name string `json:"name"`
			} `json:"permission_groups"`
		} `json:"policies"`
	}
	if _, e := a.call(ctx, c, "GET", "/user/tokens/"+url.PathEscape(verified.ID), nil, &details); e != nil {
		if Code(e) == "access_permission_denied" {
			return false, nil
		}
		return false, e
	}
	groups := map[string]bool{}
	for _, p := range details.Policies {
		if p.Effect != "allow" {
			continue
		}
		scope := p.Resources["com.cloudflare.api.account."+c.Account] == "*" || p.Resources["com.cloudflare.api.account.*"] == "*"
		if !scope {
			continue
		}
		for _, g := range p.Groups {
			groups[g.Name] = true
		}
	}
	if !groups["Access: Apps and Policies Write"] || !groups["Access: Organizations, Identity Providers, and Groups Write"] {
		return false, failure("access_permission_denied")
	}
	return true, nil
}
func (a API) Preflight(ctx context.Context, c Credential) (inventory, error) {
	var out inventory
	if c.Token == "" || c.Account == "" {
		return out, failure("access_token_required")
	}
	verified, e := a.permissions(ctx, c)
	if e != nil {
		return out, e
	}
	if _, e = a.call(ctx, c, "GET", accountPath(c)+"/organizations", nil, &out.Organization); e != nil {
		var ae *apiError
		if verified && errors.As(e, &ae) && ae.status == 403 {
			return out, failure("access_needs_zero_trust")
		}
		return out, e
	}
	if !validIssuer("https://" + out.Organization.Domain) {
		return out, failure("access_organization_unknown")
	}
	if out.Apps, e = a.Apps(ctx, c); e != nil {
		return out, e
	}
	out.IDPs, e = a.IDPs(ctx, c)
	return out, e
}
func validIssuer(issuer string) bool {
	u, e := url.Parse(issuer)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	label, ok := strings.CutSuffix(u.Host, ".cloudflareaccess.com")
	return ok && label != "" && !strings.ContainsAny(label, "./:% ")
}

// These provider refusals establish that a create was rejected. Transport,
// malformed replies and server failures keep the possibly delivered journal.
func definiteCreateRefusal(err error) bool {
	var e *apiError
	if !errors.As(err, &e) {
		return false
	}
	switch e.status {
	case 400, 401, 403, 404, 422, 429:
		return true
	}
	return false
}
