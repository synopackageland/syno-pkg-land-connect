package apps

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestCustomGateOriginUsesApprovedAuthority(t *testing.T) {
	for _, tc := range []struct {
		name, origin, site string
		allowed            bool
	}{
		{"public form", "https://" + customAuthority, "same-origin", true},
		{"legacy same-origin metadata", "", "same-origin", true},
		{"cross-site", "https://attacker.example.com", "cross-site", false},
		{"same-site sibling", "https://other.example.com", "same-site", false},
		{"opaque origin", "null", "same-origin", false},
		{"insecure origin", "http://" + customAuthority, "same-origin", false},
		{"wrong port", "https://" + customAuthority + ":8443", "same-origin", false},
		{"no origin cross-site", "", "cross-site", false},
		{"no origin same-site", "", "same-site", false},
		{"no evidence", "", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			g := &Gate{Login: func(context.Context, string, string, string) (string, string) {
				calls++
				return "alice", ""
			}}
			r := httptest.NewRequest("POST", "https://attacker.example.com"+gateSignIn, strings.NewReader("account=alice&password=fixture"))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.Header.Set("Origin", tc.origin)
			r.Header.Set("Sec-Fetch-Site", tc.site)
			// None of these request-controlled values may select the expected origin.
			r.Header.Set("Forwarded", "host=attacker.example.com;proto=https")
			r.Header.Set("X-Forwarded-Host", "attacker.example.com")
			r.Header.Set("X-Forwarded-Proto", "https")
			w := httptest.NewRecorder()
			g.handle(w, r, customAuthority, "Media")
			want, wantCalls := http.StatusForbidden, 0
			if tc.allowed {
				want, wantCalls = http.StatusSeeOther, 1
			}
			if w.Code != want || calls != wantCalls {
				t.Fatalf("status=%d login calls=%d; want %d/%d", w.Code, calls, want, wantCalls)
			}
		})
	}
}

func TestCustomGatePreservesBrowserOrigin(t *testing.T) {
	w := httptest.NewRecorder()
	renderGate(w, http.StatusOK, gateView{Locale: "en", App: "Media", Next: "/"})
	// no-referrer makes a browser's navigation POST send Origin: null, even
	// when the form targets this same public origin. Do not weaken sameOrigin
	// to accept opaque origins; retain the origin on same-origin requests.
	if got := w.Header().Get("Referrer-Policy"); got != "same-origin" {
		t.Fatalf("gate must preserve the form's public origin, policy=%q", got)
	}
}

// Run with CONNECT_GATE_BROWSER=1 after npm ci and installing Playwright's
// Chromium. All public hosts map to loopback fixtures through a local proxy.
func TestCustomGatePublicBrowser(t *testing.T) {
	if os.Getenv("CONNECT_GATE_BROWSER") != "1" {
		t.Skip("set CONNECT_GATE_BROWSER=1 to run the Chromium/Worker header regression")
	}
	for _, provider := range []string{Workers, "cloudflared"} {
		t.Run(provider, func(t *testing.T) {
			l := newCustomLab(t, false, false, func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.Header.Get("Cookie"), gateCookie) {
					t.Error("gate cookie reached the App")
				}
				fmt.Fprint(w, "fixture App")
			})
			cfg := l.proxy.cfg
			cfg.Binding.Provider = provider
			cfg.Binding.Authority = "media.example.com"
			p, err := NewCustomProxy(cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(p.Stop)
			front := httptest.NewServer(p)
			t.Cleanup(front.Close)
			// Supply only httptest's synthetic certificate to the local HTTPS
			// bridge, so Chromium can follow real redirects and store cookies.
			tlsFixture := httptest.NewTLSServer(http.NotFoundHandler())
			t.Cleanup(tlsFixture.Close)
			cert := tlsFixture.TLS.Certificates[0]
			key, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
			if err != nil {
				t.Fatal(err)
			}
			input, err := json.Marshal(map[string]string{
				"cert": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Certificate[0]})),
				"key":  string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key})),
			})
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "node", "../../../tests/gate-browser.mjs", front.URL, cfg.Binding.Authority, provider)
			cmd.Stdin = strings.NewReader(string(input))
			out, err := cmd.CombinedOutput()
			t.Log(string(out))
			if err != nil {
				t.Fatal(err)
			}
			if l.hits.Load() != 1 {
				t.Fatalf("App hits=%d; want exactly one authenticated redirect", l.hits.Load())
			}
		})
	}
}

func TestCustomGateRedirectStaysOnPublicHost(t *testing.T) {
	for _, next := range []string{"/library?id=1", "https://attacker.example.com", "//attacker.example.com", "/\\attacker.example.com", GatePrefix + "signin"} {
		t.Run(next, func(t *testing.T) {
			g := &Gate{Login: func(context.Context, string, string, string) (string, string) { return "alice", "" }}
			r := httptest.NewRequest("POST", gateSignIn, strings.NewReader("account=alice&password=fixture&next="+url.QueryEscape(next)))
			r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			r.Header.Set("Origin", "https://"+customAuthority)
			w := httptest.NewRecorder()
			g.handle(w, r, customAuthority, "Media")
			want := "/"
			if next == "/library?id=1" {
				want = next
			}
			res := w.Result()
			defer res.Body.Close()
			if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != want {
				t.Fatal(res.StatusCode, res.Header)
			}
		})
	}
}
