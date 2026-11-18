package apps

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"testing"
)

func TestIngressExactWebSocketRequest(t *testing.T) {
	g := IngressGrant{Path: "/reverse/ws"}
	for _, target := range []string{"/reverse/ws", "/", "/reverse/ws/", "/reverse/ws/other", "/reverse/ws?target=admin", "/reverse/ws?", "/reverse/%77s", "/reverse/../reverse/ws", "http://other.invalid/reverse/ws", "/webapi/entry.cgi"} {
		r := httptest.NewRequest("GET", target, nil)
		r.Header.Set("Connection", "keep-alive, Upgrade")
		r.Header.Set("Upgrade", "websocket")
		if got := g.Accepts(r); got != (target == "/reverse/ws") {
			t.Errorf("%q accepted=%v", target, got)
		}
	}
	r := httptest.NewRequest("GET", "/reverse/ws", nil)
	if g.Accepts(r) {
		t.Fatal("ordinary HTTP admitted")
	}
	r.Header.Set("Connection", "Upgrade")
	r.Header.Set("Upgrade", "websocket")
	r.Method = "POST"
	if g.Accepts(r) {
		t.Fatal("POST admitted")
	}
	for _, path := range []string{"/", "/admin.cgi", "/a//b", "/a/..", "/a%2fb", "/a?x", "/a\\b", "//host"} {
		if ValidIngressPath(path) {
			t.Fatal("invalid path", path)
		}
	}
}

func TestIngressDefinitionAndLegacyDigest(t *testing.T) {
	c := CustomApp{Name: "Reverse", URL: "http://127.0.0.1:18080", AppSignIn: true}
	sum := sha256.Sum256([]byte(`{"URL":"http://127.0.0.1:18080","AppSignIn":true,"InsecureTLS":false}`))
	if c.Digest() != hex.EncodeToString(sum[:]) {
		t.Fatal("legacy approvals changed")
	}
	c.Ingress = &IngressGrant{Package: "syngle", UID: 1001, Path: "/reverse/ws", Hostname: "reverse.example.com"}
	if !c.valid() {
		t.Fatal("valid definition refused")
	}
	original := c.Digest()
	for _, change := range []func(*CustomApp){
		func(c *CustomApp) { c.Ingress.UID = 1002 }, func(c *CustomApp) { c.Ingress.Path = "/another" },
		func(c *CustomApp) { c.Ingress.Hostname = "another.example.com" }, func(c *CustomApp) { c.Ingress.Package = "OtherApp" },
		func(c *CustomApp) { c.Ingress.Revoked = true },
	} {
		copy := c
		grant := *c.Ingress
		copy.Ingress = &grant
		change(&copy)
		if copy.Digest() == original {
			t.Fatal("grant not bound to approval")
		}
	}
	for _, target := range []string{"http://localhost:18080", "http://0.0.0.0:18080", "http://[::1]:18080", "https://127.0.0.1:18080", "http://127.0.0.1:80"} {
		copy := c
		copy.URL = target
		if copy.valid() {
			t.Fatal("target admitted", target)
		}
	}
}
