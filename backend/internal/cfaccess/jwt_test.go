package cfaccess

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestOriginJWTRefusesMissingForgedWrongAudienceRemovedAndUnknownKey(t *testing.T) {
	m, _ := managerFixture(t)
	ctx := context.Background()
	if _, e := m.Apply(ctx, Request{Emails: []string{"reader@example.test"}}); e != nil {
		t.Fatal(e)
	}
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	fetched := 0
	keys := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetched++
		json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kid": "test-key", "kty": "RSA", "alg": "RS256", "use": "sig", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
	}))
	defer keys.Close()
	// Test transport maps the fixed trusted issuer URL onto a local fixture.
	m.keys.http = &http.Client{Transport: rewriteTransport{base: keys.URL}}
	claims := map[string]any{"iss": "https://fixture.cloudflareaccess.com", "aud": []string{"aud-1"}, "email": "reader@example.test", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "type": "app"}
	token := func(kid string) string {
		h, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": kid})
		b, _ := json.Marshal(claims)
		raw := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(b)
		sum := sha256.Sum256([]byte(raw))
		sig, _ := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
		return raw + "." + base64.RawURLEncoding.EncodeToString(sig)
	}
	good := token("test-key")
	if e = m.Verify(ctx, "web.example.test", good); e != nil {
		t.Fatal(e)
	}
	if fetched != 1 {
		t.Fatal(fetched)
	}
	for _, bad := range []string{"", good + "a", strings.Replace(good, ".", ".x", 1), token("unknown")} {
		if m.Verify(ctx, "web.example.test", bad) == nil {
			t.Fatal("accepted bad JWT")
		}
	}
	if m.Verify(ctx, "other.example.test", good) == nil {
		t.Fatal("accepted unknown hostname")
	}
	for name, value := range map[string]any{"iss": "https://attacker.invalid", "aud": []string{"other"}, "email": "removed@example.test", "exp": time.Now().Add(-time.Second).Unix(), "nbf": time.Now().Add(time.Hour).Unix(), "type": "service_token"} {
		old, exists := claims[name]
		claims[name] = value
		if m.Verify(ctx, "web.example.test", token("test-key")) == nil {
			t.Fatal("accepted wrong " + name)
		}
		if exists {
			claims[name] = old
		} else {
			delete(claims, name)
		}
	}
	m.state.Synced = false
	m.publish()
	if m.Verify(ctx, "web.example.test", good) == nil {
		t.Fatal("pending revision accepted")
	}
}

type rewriteTransport struct{ base string }

func (r rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	u := *req.URL
	clone.URL = &u
	parts := strings.SplitN(r.base, "://", 2)
	u.Scheme = parts[0]
	u.Host = parts[1]
	return http.DefaultTransport.RoundTrip(clone)
}
func TestUnusedOriginDoesNotFetchOrRequireJWT(t *testing.T) {
	m, _ := managerFixture(t)
	if e := m.Verify(context.Background(), "web.example.test", ""); e != nil {
		t.Fatal(e)
	}
}
