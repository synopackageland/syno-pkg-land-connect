package dsmexternal

import (
	"testing"

	"dsmapp/internal/connector"
)

func port(n int) *int { return &n }

func TestAdviceSuggestsOnlyACloudflareOwnAddress(t *testing.T) {
	domain := connector.Status{Provider: "cloudflare", State: "connected", Mode: "domain", Hostname: "NAS.example.com"}
	for _, tc := range []struct {
		name     string
		current  *External
		statuses []connector.Status
		state    string
		reason   string
		host     string
	}{
		{"unset", &External{}, []connector.Status{domain}, "not-set", "", "nas.example.com"},
		{"other host", &External{Hostname: "old.example.net", HTTPSPort: port(443)}, []connector.Status{domain}, "differs", "", "nas.example.com"},
		{"same host other port", &External{Hostname: "nas.example.com", HTTPSPort: port(5001)}, []connector.Status{domain}, "differs", "", "nas.example.com"},
		{"same host no port", &External{Hostname: "nas.example.com"}, []connector.Status{domain}, "differs", "", "nas.example.com"},
		{"matches", &External{Hostname: "nas.example.com.", HTTPSPort: port(443)}, []connector.Status{domain}, "matches", "", "nas.example.com"},
		{"unread", nil, []connector.Status{domain}, "unknown", "read-failed", "nas.example.com"},
		{"token endpoint", &External{}, []connector.Status{{Provider: "cloudflare", State: "connected", Mode: "token", Endpoints: []connector.Endpoint{{URL: "https://files.example.org", Public: true}}}}, "not-set", "", "files.example.org"},
		{"temporary", &External{}, []connector.Status{{Provider: "cloudflare", State: "connected", Mode: "quick", Hostname: "a-b.trycloudflare.com"}}, "no-stable-address", "temporary-address", ""},
		{"temporary host in domain mode", &External{}, []connector.Status{{Provider: "cloudflare", State: "connected", Mode: "domain", Hostname: "a-b.trycloudflare.com"}}, "no-stable-address", "temporary-address", ""},
		{"stopped domain", &External{}, []connector.Status{{Provider: "cloudflare", State: "disconnected", Mode: "domain", Hostname: "nas.example.com"}}, "no-stable-address", "none", ""},
		{"private only", &External{}, []connector.Status{{Provider: "tailscale", State: "connected"}, {Provider: "netbird", State: "connected"}}, "no-stable-address", "private-address", ""},
		{"nothing", &External{}, nil, "no-stable-address", "none", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := Advise(tc.current, tc.statuses)
			if a.State != tc.state || a.Reason != tc.reason {
				t.Fatalf("%+v", a)
			}
			if tc.host == "" && a.Suggested != nil {
				t.Fatalf("suggested %+v", a.Suggested)
			}
			if tc.host != "" && (a.Suggested == nil || a.Suggested.Hostname != tc.host || a.Suggested.HTTPSPort != 443 || a.Suggested.Source != "cloudflare") {
				t.Fatalf("suggested %+v", a.Suggested)
			}
		})
	}
}

func TestValidHostnameAcceptsNamesAddressesAndClearing(t *testing.T) {
	for _, ok := range []string{"", "nas.example.com", "NAS-1.example.co.uk", "203.0.113.7", "2001:db8::1", "fixture.invalid"} {
		if !ValidHostname(ok) {
			t.Errorf("%q refused", ok)
		}
	}
	for _, bad := range []string{"https://nas.example.com", "nas.example.com:443", "nas.example.com/x", "nas example.com", "-nas.example.com", "nas..example.com", "user@nas.example.com", string(make([]byte, 254))} {
		if ValidHostname(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}
