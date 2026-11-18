package relay

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestGateHeadersSurviveRelay(t *testing.T) {
	cookie := "__Host-spl-connect=" + strings.Repeat("a", 64) + "; Path=/; Max-Age=28800; HttpOnly; Secure; SameSite=Lax"
	c, _, _ := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != testAuthority || r.Header.Get("Origin") != "https://"+testAuthority || r.Header.Get("Sec-Fetch-Site") != "same-origin" {
			t.Error("relay changed approved Host or browser origin metadata")
		}
		if r.Header.Get("X-Forwarded-Host") != "" {
			t.Error("visitor selected forwarded host")
		}
		w.Header().Set("Set-Cookie", cookie)
		w.Header().Set("Location", "/library?id=1")
		w.WriteHeader(http.StatusSeeOther)
	}))
	start(t, c, 1, openMessage{Method: "POST", Path: "/_spl/connect/signin", Headers: [][2]string{
		{"Host", "attacker.example.com"}, {"X-Forwarded-Host", "attacker.example.com"},
		{"Origin", "https://" + testAuthority}, {"Sec-Fetch-Site", "same-origin"},
	}})
	kind, _, raw := receive(t, c)
	var head headMessage
	if err := json.Unmarshal(raw, &head); err != nil || kind != headFrame || head.Status != http.StatusSeeOther {
		t.Fatalf("bad gate response: kind=%d head=%+v err=%v", kind, head, err)
	}
	h := http.Header{}
	for _, pair := range head.Headers {
		h.Add(pair[0], pair[1])
	}
	if h.Get("Set-Cookie") != cookie || h.Get("Location") != "/library?id=1" {
		t.Fatal("relay changed gate cookie or redirect", h)
	}
}
