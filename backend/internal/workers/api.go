package workers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"time"

	"dsmapp/internal/connector"
)

// API is a fixed Cloudflare account API transport. Errors never embed response
// bodies, URLs, tokens, headers, transport errors or caller-provided strings.
type API struct {
	Base   string
	HTTP   *http.Client
	Script []byte
	// Bridge is the NetBird Signal bridge, deployed on the user's zone.
	Bridge []byte
}
type apiError struct {
	code   string
	status int
}

func (e *apiError) Error() string { return "Cloudflare workers request refused" }
func (a API) base() string {
	if a.Base != "" {
		return a.Base
	}
	return "https://api.cloudflare.com/client/v4"
}
func (a API) call(ctx context.Context, token, method, path, contentType string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, a.base()+path, bytes.NewReader(body))
	if err != nil {
		return failure("workers_unavailable")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	client := a.HTTP
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	res, err := client.Do(req)
	if err != nil {
		return failure("workers_unavailable")
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 2<<20+1))
	if err != nil || len(raw) > 2<<20 {
		return failure("workers_unavailable")
	}
	var envelope struct {
		Success bool            `json:"success"`
		Result  json.RawMessage `json:"result"`
	}
	if json.Unmarshal(raw, &envelope) != nil || res.StatusCode < 200 || res.StatusCode >= 300 || !envelope.Success {
		code := "workers_api_refused"
		// These fixed GETs contain no caller input except the credential.
		// Deployment bad requests must retain their general API refusal code.
		tokenCheck := method == http.MethodGet && (path == "/user/tokens/verify" || path == "/accounts?per_page=50")
		if res.StatusCode == http.StatusUnauthorized || (res.StatusCode == http.StatusBadRequest && tokenCheck) {
			code = "workers_token_invalid"
		}
		if res.StatusCode == 403 {
			code = "workers_permission_denied"
		}
		if res.StatusCode == 429 {
			code = "workers_rate_limited"
		}
		return &apiError{code: code, status: res.StatusCode}
	}
	if out != nil && json.Unmarshal(envelope.Result, out) != nil {
		return failure("workers_unavailable")
	}
	return nil
}
func failure(code string) error {
	return connector.Coded(code, "Cloudflare workers request unavailable")
}
func sanitize(err error) error {
	if strings.HasPrefix(connector.ErrorCode(err), "workers_") {
		return err
	}
	var e *apiError
	if errors.As(err, &e) {
		return failure(e.code)
	}
	if err != nil {
		return failure("workers_unavailable")
	}
	return nil
}
func missing(err error) bool { var e *apiError; return errors.As(err, &e) && e.status == 404 }
func (a API) json(ctx context.Context, v privateState, method, path string, body, out any) error {
	raw, _ := json.Marshal(body)
	if body == nil {
		raw = nil
	}
	return a.call(ctx, v.Token, method, "/accounts/"+v.AccountID+path, "application/json", raw, out)
}
func (a API) validate(ctx context.Context, token, account string) (string, error) {
	var verify struct {
		Status string `json:"status"`
	}
	if err := a.call(ctx, token, "GET", "/user/tokens/verify", "", nil, &verify); err != nil {
		return "", sanitize(err)
	}
	if verify.Status != "active" {
		return "", failure("workers_token_invalid")
	}
	return a.subdomain(ctx, privateState{Token: token, AccountID: account})
}

// subdomain is the account's workers.dev name now, empty when it has none.
func (a API) subdomain(ctx context.Context, v privateState) (string, error) {
	var sub struct {
		Subdomain string `json:"subdomain"`
	}
	err := a.json(ctx, v, "GET", "/workers/subdomain", nil, &sub)
	if err != nil && !missing(err) {
		return "", sanitize(err)
	}
	return sub.Subdomain, nil
}
func (a API) ensureSubdomain(ctx context.Context, v privateState) error {
	var out struct {
		Subdomain string `json:"subdomain"`
	}
	if err := a.json(ctx, v, "PUT", "/workers/subdomain", map[string]any{"subdomain": v.Subdomain}, &out); err != nil {
		// The name is a valid label, so a refused claim means another
		// account holds it or Cloudflare reserves it.
		var e *apiError
		if errors.As(err, &e) && (e.status == 400 || e.status == 409) {
			return failure("workers_subdomain_taken")
		}
		return sanitize(err)
	}
	if out.Subdomain != v.Subdomain {
		return failure("workers_subdomain_conflict")
	}
	return nil
}
func (a API) owned(ctx context.Context, v privateState, r Record) (bool, error) {
	var out struct {
		Tags []string `json:"tags"`
	}
	err := a.json(ctx, v, "GET", "/workers/scripts/"+r.Name+"/settings", nil, &out)
	if missing(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, tag := range out.Tags {
		if tag == "synopackageland-connect:"+v.Owner {
			return true, nil
		}
	}
	return false, failure("workers_name_conflict")
}
func (a API) deploy(ctx context.Context, v privateState, r Record) error {
	if len(a.Script) == 0 {
		return failure("workers_bundle_missing")
	}
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	metadata := map[string]any{"main_module": "worker.js", "compatibility_date": "2026-10-24", "compatibility_flags": []string{"enable_request_signal"}, "tags": []string{"synopackageland-connect:" + v.Owner}, "observability": map[string]bool{"enabled": false}, "bindings": []any{map[string]any{"type": "durable_object_namespace", "name": "TUNNEL", "class_name": "Tunnel"}, map[string]any{"type": "secret_text", "name": "RELAY_SECRET", "text": r.Secret}}, "migrations": map[string]any{"new_tag": "v1", "new_sqlite_classes": []string{"Tunnel"}}}
	// A retry updates the original script without reapplying an already-run migration.
	owned, err := a.owned(ctx, v, r)
	if err != nil {
		return sanitize(err)
	}
	if owned {
		delete(metadata, "migrations")
	}
	raw, _ := json.Marshal(metadata)
	p, err := w.CreateFormField("metadata")
	if err != nil {
		return failure("workers_unavailable")
	}
	p.Write(raw)
	p, err = w.CreatePart(textproto.MIMEHeader{"Content-Disposition": []string{`form-data; name="worker.js"; filename="worker.js"`}, "Content-Type": []string{"application/javascript+module"}})
	if err != nil {
		return failure("workers_unavailable")
	}
	p.Write(a.Script)
	w.Close()
	err = a.call(ctx, v.Token, "PUT", "/accounts/"+v.AccountID+"/workers/scripts/"+r.Name, w.FormDataContentType(), b.Bytes(), nil)
	if err != nil {
		return sanitize(err)
	}
	return sanitize(a.json(ctx, v, "POST", "/workers/scripts/"+r.Name+"/subdomain", map[string]bool{"enabled": true, "previews_enabled": false}, nil))
}
func (a API) delete(ctx context.Context, v privateState, r Record) error {
	owned, err := a.owned(ctx, v, r)
	if connector.ErrorCode(err) == "workers_name_conflict" {
		return nil
	}
	if err != nil {
		return sanitize(err)
	}
	if !owned {
		return nil
	}
	err = a.json(ctx, v, "DELETE", "/workers/scripts/"+r.Name+"?force=true", nil, nil)
	if missing(err) {
		return nil
	}
	if err != nil {
		return sanitize(err)
	}
	stillOwned, err := a.owned(ctx, v, r)
	if connector.ErrorCode(err) == "workers_name_conflict" {
		return nil
	}
	if err != nil {
		return sanitize(err)
	}
	if stillOwned {
		return failure("workers_cleanup_pending")
	}
	return nil
}
func validToken(token string) bool {
	return len(token) >= 8 && len(token) <= 4096 && !strings.ContainsAny(token, "\r\n\x00\t ")
}

func (a API) account(ctx context.Context, token string) (string, error) {
	var accounts []struct {
		ID string `json:"id"`
	}
	if err := a.call(ctx, token, "GET", "/accounts?per_page=50", "", nil, &accounts); err != nil {
		return "", sanitize(err)
	}
	if len(accounts) != 1 || !accountPattern.MatchString(accounts[0].ID) {
		return "", failure("workers_accounts_required")
	}
	return accounts[0].ID, nil
}
