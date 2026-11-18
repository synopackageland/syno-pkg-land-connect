package cfaccess

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"
)

type keyCache struct {
	mu              sync.Mutex
	issuer          string
	keys            map[string]*rsa.PublicKey
	checked         time.Time
	attemptedIssuer string
	attemptedAt     time.Time
	http            *http.Client
}

func (k *keyCache) key(ctx context.Context, issuer, kid string) (*rsa.PublicKey, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if !validIssuer(issuer) {
		return nil, failure("access_jwt_invalid")
	}
	if k.issuer == issuer && time.Since(k.checked) < 5*time.Minute {
		if key := k.keys[kid]; key != nil {
			return key, nil
		}
		if time.Since(k.checked) < 30*time.Second {
			return nil, failure("access_jwt_invalid")
		}
	}
	if k.attemptedIssuer == issuer && time.Since(k.attemptedAt) < 30*time.Second {
		return nil, failure("access_jwt_unavailable")
	}
	k.attemptedIssuer = issuer
	k.attemptedAt = time.Now()
	req, e := http.NewRequestWithContext(ctx, "GET", issuer+"/cdn-cgi/access/certs", nil)
	if e != nil {
		return nil, failure("access_jwt_invalid")
	}
	client := http.Client{Timeout: 5 * time.Second}
	if k.http != nil {
		client = *k.http
		if client.Timeout == 0 {
			client.Timeout = 5 * time.Second
		}
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, e := client.Do(req)
	if e != nil {
		return nil, failure("access_jwt_unavailable")
	}
	defer res.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(res.Body, 256<<10+1))
	if e != nil || len(raw) > 256<<10 || res.StatusCode != 200 {
		return nil, failure("access_jwt_unavailable")
	}
	var set struct {
		Keys []struct {
			KID string `json:"kid"`
			KTY string `json:"kty"`
			Alg string `json:"alg"`
			Use string `json:"use"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if json.Unmarshal(raw, &set) != nil || len(set.Keys) == 0 || len(set.Keys) > 16 {
		return nil, failure("access_jwt_unavailable")
	}
	keys := map[string]*rsa.PublicKey{}
	for _, j := range set.Keys {
		if j.KTY != "RSA" || j.Alg != "" && j.Alg != "RS256" || j.Use != "" && j.Use != "sig" {
			continue
		}
		n, e1 := base64.RawURLEncoding.DecodeString(j.N)
		exp, e2 := base64.RawURLEncoding.DecodeString(j.E)
		if e1 != nil || e2 != nil || len(n) < 256 || len(n) > 512 || len(exp) == 0 || len(exp) > 4 || j.KID == "" || keys[j.KID] != nil {
			return nil, failure("access_jwt_unavailable")
		}
		value := new(big.Int).SetBytes(exp).Int64()
		if value < 3 || value > 1<<31-1 || value%2 == 0 {
			return nil, failure("access_jwt_unavailable")
		}
		keys[j.KID] = &rsa.PublicKey{N: new(big.Int).SetBytes(n), E: int(value)}
	}
	k.issuer = issuer
	k.checked = time.Now()
	k.keys = keys
	if key := keys[kid]; key != nil {
		return key, nil
	}
	return nil, failure("access_jwt_invalid")
}

// Verify applies only on Cloudflare-specific origin bindings. Nothing from a
// request chooses an issuer, JWKS URL, audience, destination or DSM identity.
func (m *Manager) Verify(ctx context.Context, host, raw string) error {
	if m.invalid.Load() {
		return failure("access_jwt_invalid")
	}
	v := m.view.Load()
	if v == nil {
		return failure("access_jwt_invalid")
	}
	if len(v.Emails) == 0 && len(v.Records) == 0 {
		return nil
	}
	r, ok := v.Records[host]
	if !v.Synced || len(v.Emails) == 0 || !ok || r.AUD == "" || len(raw) > 16384 {
		return failure("access_jwt_invalid")
	}
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return failure("access_jwt_invalid")
	}
	header, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil {
		return failure("access_jwt_invalid")
	}
	var h struct {
		Alg string `json:"alg"`
		KID string `json:"kid"`
	}
	if json.Unmarshal(header, &h) != nil || h.Alg != "RS256" || h.KID == "" || len(h.KID) > 256 {
		return failure("access_jwt_invalid")
	}
	payload, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil {
		return failure("access_jwt_invalid")
	}
	var claims struct {
		Issuer   string   `json:"iss"`
		Audience []string `json:"aud"`
		Email    string   `json:"email"`
		Exp      int64    `json:"exp"`
		NBF      int64    `json:"nbf"`
		IAT      int64    `json:"iat"`
		Type     string   `json:"type"`
	}
	now := time.Now().Unix()
	if json.Unmarshal(payload, &claims) != nil || claims.Issuer != v.Issuer || !slices.Contains(claims.Audience, r.AUD) || !slices.Contains(v.Emails, claims.Email) || claims.Exp <= now || claims.NBF > now || claims.IAT > now+30 || claims.Type != "app" {
		return failure("access_jwt_invalid")
	}
	sig, e := base64.RawURLEncoding.DecodeString(parts[2])
	if e != nil {
		return failure("access_jwt_invalid")
	}
	key, e := m.keys.key(ctx, v.Issuer, h.KID)
	if e != nil {
		return e
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], sig) != nil || m.view.Load() != v || m.invalid.Load() {
		return failure("access_jwt_invalid")
	}
	return nil
}
