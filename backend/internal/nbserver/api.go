package nbserver

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// managementAPI reaches the server's HTTP API on loopback, verifying the
// server's own certificate for its public name. It never leaves the NAS.
type managementAPI struct {
	base   string
	client *http.Client
}

func newManagementAPI(p paths, hostname string, port int) (managementAPI, error) {
	raw, e := os.ReadFile(p.cert())
	if e != nil {
		return managementAPI{}, e
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(raw) {
		return managementAPI{}, errors.New("invalid server certificate")
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: hostname, MinVersion: tls.VersionTLS12},
		DialContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		},
		Proxy: nil,
	}
	return managementAPI{base: "https://" + hostname, client: &http.Client{Transport: transport, Timeout: time.Minute,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

type apiError struct{ status int }

func (e apiError) Error() string { return fmt.Sprintf("NetBird server answered %d", e.status) }

func (a managementAPI) call(ctx context.Context, method, path, token string, in, out any) error {
	var body io.Reader
	if in != nil {
		raw, _ := json.Marshal(in)
		body = bytes.NewReader(raw)
	}
	req, e := http.NewRequestWithContext(ctx, method, a.base+path, body)
	if e != nil {
		return e
	}
	req.Header.Set("Accept", "application/json")
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Token "+token)
	}
	res, e := a.client.Do(req)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return apiError{res.StatusCode}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(out)
}

// setupRequired reports whether the server still waits for its first owner.
func (a managementAPI) setupRequired(ctx context.Context) (bool, error) {
	var out struct {
		SetupRequired *bool `json:"setup_required"`
	}
	if e := a.call(ctx, "GET", "/api/instance", "", nil, &out); e != nil {
		return false, e
	}
	if out.SetupRequired == nil {
		return false, errors.New("invalid instance answer")
	}
	return *out.SetupRequired, nil
}

// tokenDays is the longest life NetBird gives an API token.
const tokenDays = 365

// setup makes the owner and returns the owner's ID and API token. NetBird
// then closes setup for good.
func (a managementAPI) setup(ctx context.Context, email, password string) (user, token string, e error) {
	in := map[string]any{"email": email, "password": password, "name": "Syno Connect Center", "create_pat": true, "pat_expire_in": tokenDays}
	var out struct {
		UserID string  `json:"user_id"`
		Token  *string `json:"personal_access_token"`
	}
	if e = a.call(ctx, "POST", "/api/setup", "", in, &out); e != nil {
		return "", "", e
	}
	if out.Token == nil || !plainToken(*out.Token) || out.UserID == "" {
		return "", "", errors.New("the server returned no API token")
	}
	return out.UserID, *out.Token, nil
}

func plainToken(t string) bool { return t != "" && len(t) <= 512 && !strings.ContainsAny(t, " \r\n") }

// accessToken is one of the owner's API tokens, without its secret.
type accessToken struct {
	ID      string    `json:"id"`
	Expires time.Time `json:"expiration_date"`
}

func tokensPath(user string) string { return "/api/users/" + url.PathEscape(user) + "/tokens" }

// tokens lists the owner's API tokens.
func (a managementAPI) tokens(ctx context.Context, token, user string) ([]accessToken, error) {
	var out []accessToken
	e := a.call(ctx, "GET", tokensPath(user), token, nil, &out)
	return out, e
}

// newToken makes another API token for the owner, valid as long as NetBird allows.
func (a managementAPI) newToken(ctx context.Context, token, user string) (string, accessToken, error) {
	var out struct {
		Plain string      `json:"plain_token"`
		Token accessToken `json:"personal_access_token"`
	}
	if e := a.call(ctx, "POST", tokensPath(user), token, map[string]any{"name": "Syno Connect Center", "expires_in": tokenDays}, &out); e != nil {
		return "", accessToken{}, e
	}
	if !plainToken(out.Plain) || out.Token.ID == "" || out.Token.Expires.IsZero() {
		return "", accessToken{}, errors.New("the server returned no API token")
	}
	return out.Plain, out.Token, nil
}

func (a managementAPI) deleteToken(ctx context.Context, token, user, id string) error {
	return a.call(ctx, "DELETE", tokensPath(user)+"/"+url.PathEscape(id), token, nil, nil)
}

// setupKey makes a key that joins one peer once, within the hour.
func (a managementAPI) setupKey(ctx context.Context, token string) (string, error) {
	in := map[string]any{"name": "Syno Connect Center NAS", "type": "one-off", "expires_in": 3600, "auto_groups": []string{}, "usage_limit": 1}
	var out struct {
		Key string `json:"key"`
	}
	if e := a.call(ctx, "POST", "/api/setup-keys", token, in, &out); e != nil {
		return "", e
	}
	if len(out.Key) < 16 || len(out.Key) > 256 || strings.ContainsAny(out.Key, " \r\n") {
		return "", errors.New("the server returned no setup key")
	}
	return out.Key, nil
}

// peers counts the peers the server knows and those connected now.
func (a managementAPI) peers(ctx context.Context, token string) (total, connected int, e error) {
	var out []struct {
		Connected bool `json:"connected"`
	}
	if e = a.call(ctx, "GET", "/api/peers", token, nil, &out); e != nil {
		return 0, 0, e
	}
	for _, p := range out {
		if p.Connected {
			connected++
		}
	}
	return len(out), connected, nil
}
