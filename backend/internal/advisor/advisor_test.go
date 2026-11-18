package advisor

import (
	"regexp"
	"strings"
	"testing"
)

var checkedMonth = regexp.MustCompile(`^20\d\d-(0[1-9]|1[0-2])$`)

func yes() *bool { v := true; return &v }
func no() *bool  { v := false; return &v }

func ids(r Result) []string {
	out := []string{}
	for _, o := range r.Options {
		out = append(out, o.ID+":"+o.Suitability)
	}
	return out
}

func TestCatalogLoadsEveryOptionOnce(t *testing.T) {
	c := Default()
	if c.Version == "" || len(c.Options) != 8 {
		t.Fatalf("catalog: %+v", c)
	}
	seen := map[string]bool{}
	for _, o := range c.Options {
		if seen[o.ID] || o.Difficulty < 1 || o.Difficulty > 4 || o.Reach == "" || o.Who == "" || o.Via == "" {
			t.Fatalf("invalid option: %+v", o)
		}
		seen[o.ID] = true
		for goal := range o.Goals {
			if !strings.Contains(strings.Join(c.Goals, ","), goal) {
				t.Fatalf("%s names unknown goal %s", o.ID, goal)
			}
		}
	}
	if !seen["quickconnect"] || !seen["ddns"] {
		t.Fatal("QuickConnect and DDNS are listed as DSM's own options")
	}
}

func TestParseRejectsUnknownFieldsAndGoals(t *testing.T) {
	if _, err := Parse([]byte(`{"version":"1","goals":["share"],"options":[{"id":"x","goals":{"share":1},"extra":1}]}`)); err == nil {
		t.Fatal("unknown field accepted")
	}
	if _, err := Parse([]byte(`{"version":"1","goals":["share"],"options":[{"id":"x","goals":{"other":1},"fit":"f","reach":"r","cost":"c","client":"b","difficulty":1,"who":"w","via":"v"}]}`)); err == nil {
		t.Fatal("unknown goal accepted")
	}
}

func TestRecommendRanksTheGoalFirstAndKeepsTheRest(t *testing.T) {
	got := ids(Recommend(Default(), "apps", Facts{IPv6Global: yes()}))
	want := []string{"cloudflare-quick:recommended", "cloudflare-workers:alternative", "cloudflare-domain:alternative", "quickconnect:alternative", "ipv6:alternative", "tailscale:other", "netbird:other", "ddns:other"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("got %v", got)
	}
	got = ids(Recommend(Default(), "all", Facts{}))
	if got[0] != "tailscale:recommended" || got[1] != "netbird:alternative" || got[2] != "cloudflare-quick:other" {
		t.Fatalf("got %v", got)
	}
}

func TestRecommendWithoutGoalKeepsCatalogOrder(t *testing.T) {
	r := Recommend(Default(), "", Facts{IPv6Global: yes()})
	for i, o := range r.Options {
		if o.ID != Default().Options[i].ID || o.Suitability != "other" {
			t.Fatalf("got %v", ids(r))
		}
	}
}

func TestIPv6WithoutAGlobalAddressIsUnavailableWithItsReason(t *testing.T) {
	r := Recommend(Default(), "apps", Facts{IPv6Global: no()})
	last := r.Options[len(r.Options)-1]
	if last.ID != "ipv6" || last.Suitability != "unavailable" || strings.Join(last.Reasons, ",") != "ipv6_no_global_address" {
		t.Fatalf("got %+v", last)
	}
	for _, o := range Recommend(Default(), "apps", Facts{}).Options {
		if o.ID == "ipv6" && (o.Suitability != "alternative" || strings.Join(o.Reasons, ",") != "ipv6_unknown,no_protection") {
			t.Fatalf("an unknown address must not hide the option: %+v", o)
		}
	}
}

// Direct IPv6 stays a choice, but without protection it is never the one
// recommended, even when it ranks first.
func TestAnOptionWithoutProtectionIsNeverRecommended(t *testing.T) {
	c := Default()
	c.Options = []Option{}
	for _, o := range Default().Options {
		if o.ID == "ipv6" || o.ID == "tailscale" {
			c.Options = append(c.Options, o)
		}
	}
	r := Recommend(c, "services-desktop", Facts{IPv6Global: yes()})
	if got := strings.Join(ids(r), " "); got != "tailscale:recommended ipv6:alternative" {
		t.Fatalf("got %v", got)
	}
	c.Options = c.Options[1:]
	if r := Recommend(c, "apps", Facts{IPv6Global: yes()}); r.Options[0].Suitability != "alternative" || !contains(r.Options[0].Reasons, "no_protection") {
		t.Fatalf("got %+v", r.Options[0])
	}
	for _, o := range Default().Options {
		if (o.Protection == "none") != (o.ID == "ipv6" || o.ID == "ddns") {
			t.Fatalf("%s protection %q", o.ID, o.Protection)
		}
	}
	if _, err := Parse([]byte(strings.Replace(string(catalogJSON), `"protection": "none"`, `"protection": "some"`, 1))); err == nil {
		t.Fatal("unknown protection accepted")
	}
}

func TestARunningMethodIsMarkedAndNotSuggestedAgain(t *testing.T) {
	r := Recommend(Default(), "all", Facts{States: map[string]string{"tailscale": "connected"}})
	if r.Options[0].ID != "tailscale" || r.Options[0].State != "connected" || !contains(r.Options[0].Reasons, "already_running") || len(r.Options[0].Next) != 0 {
		t.Fatalf("got %+v", r.Options[0])
	}
}

func TestNextStepsKeepConsentWithTheUser(t *testing.T) {
	r := Recommend(Default(), "apps", Facts{DSMHTTPPort: 5000})
	for _, o := range r.Options {
		if o.Public && o.Provider != "" {
			step := o.Next[0]
			if step.Input["confirmExposure"] != true || !contains(step.Needs, "exposure-consent") {
				t.Fatalf("%s: public start without consent note: %+v", o.ID, step)
			}
		}
	}
	byID := map[string]Recommendation{}
	for _, o := range r.Options {
		byID[o.ID] = o
	}
	if !contains(byID["cloudflare-domain"].Next[0].Needs, "browser-login") || !contains(byID["cloudflare-domain"].Next[0].Needs, "domain-on-cloudflare") {
		t.Fatal(byID["cloudflare-domain"].Next)
	}
	ts := byID["tailscale"].Next[0]
	if _, mapped := ts.Input["mappings"]; mapped || !contains(ts.Needs, "browser-login") {
		t.Fatalf("tailscale start: %+v", ts)
	}
	if byID["quickconnect"].Next[0].Kind != "user" || byID["quickconnect"].Next[0].Action != "open-dsm-quickconnect" {
		t.Fatal(byID["quickconnect"].Next)
	}
	// DSM's DDNS and the Free link are set up by the user, never by a ctl command.
	if byID["ddns"].Next[0].Kind != "user" || byID["ddns"].Next[0].Action != "open-dsm-ddns" || !contains(byID["ddns"].Next[0].Needs, "router-setup") {
		t.Fatal(byID["ddns"].Next)
	}
	if byID["cloudflare-workers"].Next[0].Kind != "user" || byID["cloudflare-workers"].Next[0].Action != "open-free-link" {
		t.Fatal(byID["cloudflare-workers"].Next)
	}
}

// HTTPS is described as each method actually serves it: Cloudflare's own
// certificate, Tailscale's ts.net certificate once HTTPS is on (else HTTP in
// the tailnet), NetBird's HTTP inside WireGuard, DSM's own certificate on IPv6.
func TestCatalogSaysWhoProvidesHTTPS(t *testing.T) {
	want := map[string]string{"cloudflare-quick": "cloudflare-certificate", "cloudflare-workers": "cloudflare-certificate", "cloudflare-domain": "cloudflare-certificate",
		"tailscale": "tailscale-certificate", "netbird": "wireguard-http", "quickconnect": "dsm-managed", "ddns": "own-certificate", "ipv6": "own-certificate"}
	for _, o := range Default().Options {
		if o.HTTPS != want[o.ID] {
			t.Errorf("%s https %q, want %q", o.ID, o.HTTPS, want[o.ID])
		}
	}
	if _, err := Parse([]byte(`{"version":"x","goals":[],"options":[{"id":"a","fit":"f","reach":"r","cost":"c","client":"b","who":"w","via":"v","difficulty":1,"goals":{},"accounts":[],"limits":[],"pairWith":[]}]}`)); err == nil {
		t.Fatal("an option without https parsed")
	}
}

func TestCloudflareComesInTwoTiersInOrder(t *testing.T) {
	tiers := []string{}
	for _, o := range Default().Options {
		if o.Provider == "cloudflare" {
			tiers = append(tiers, o.ID+":"+string(rune('0'+o.Tier))+":"+o.Scope)
		}
		if o.Scope == "" {
			t.Fatalf("%s has no scope", o.ID)
		}
	}
	if strings.Join(tiers, " ") != "cloudflare-quick:1:one-web-address cloudflare-domain:2:address-per-app" {
		t.Fatalf("tiers %v", tiers)
	}
	for _, o := range Default().Options {
		if (o.Provider == "tailscale" || o.Provider == "netbird") && o.Scope != "private-network" {
			t.Fatalf("%s scope %s", o.ID, o.Scope)
		}
	}
}

func TestUsesRankAndPublishTheirOwnService(t *testing.T) {
	for goal, want := range map[string]string{
		"services-web":     "cloudflare-domain:recommended quickconnect:alternative ipv6:alternative",
		"services-desktop": "tailscale:recommended quickconnect:alternative ipv6:alternative",
		"all":              "tailscale:recommended netbird:alternative cloudflare-quick:other",
	} {
		if got := ids(Recommend(Default(), goal, Facts{})); strings.Join(got[:3], " ") != want {
			t.Fatalf("%s: %v", goal, got)
		}
	}
	for _, goal := range []string{"services-desktop", "all"} {
		for _, o := range Recommend(Default(), goal, Facts{DSMHTTPPort: 5000, States: map[string]string{"cloudflare": "connected"}, Modes: map[string]string{"cloudflare": "domain"}}).Options {
			switch o.ID {
			case "cloudflare-domain", "cloudflare-quick":
				if len(o.Next) != 0 {
					t.Fatalf("%s offers Cloudflare for a TCP service", goal)
				}
			case "tailscale":
				// Tailscale reaches the use's service at the NAS's own address: the step maps nothing.
				if _, mapped := o.Next[0].Input["mappings"]; mapped || len(o.Next[0].Needs) != 1 || len(o.Reasons) != 0 {
					t.Fatalf("%s tailscale: %+v", goal, o)
				}
			}
		}
	}
	if o := Recommend(Default(), "", Facts{DSMHTTPPort: 5000}).Options[3]; o.ID != "tailscale" || o.Next[0].Input["mappings"] != nil {
		t.Fatalf("no use: %+v", o)
	}
}

func TestEveryOptionSaysRiskInstallPriceAndSteps(t *testing.T) {
	c := Default()
	for _, o := range c.Options {
		if o.Risk == "" || o.Install.App == "" || !checkedMonth.MatchString(o.Pricing.Checked) || (!o.Native && len(o.Guide) == 0) {
			t.Fatalf("%s: %+v", o.ID, o)
		}
		for _, url := range o.Pricing.Links {
			if !strings.HasPrefix(url, "https://") {
				t.Fatalf("%s pricing link %q", o.ID, url)
			}
		}
	}
	if _, err := Parse([]byte(strings.Replace(string(catalogJSON), `"risk": "login-page"`, `"risk": "some"`, 1))); err == nil {
		t.Fatal("unknown risk accepted")
	}
}

func TestSMBIsPrivateOnly(t *testing.T) {
	for _, o := range Recommend(Default(), "all", Facts{}).Options {
		if o.Provider == "cloudflare" && (o.Suitability != "other" || len(o.Next) != 0) {
			t.Fatalf("SMB offers Cloudflare: %+v", o)
		}
	}
}

// TCP presets are carried by private networks only; Cloudflare publishes
// web pages and no longer takes a TCP service.
func TestPresetsArePrivateNetworkOnly(t *testing.T) {
	for _, p := range Default().TCPServices {
		if strings.Join(p.Providers, " ") != "tailscale netbird" {
			t.Fatalf("%s: %v", p.ID, p.Providers)
		}
	}
	for _, replacement := range []string{`[]`, `["unknown"]`, `["cloudflare"]`, `["tailscale","tailscale"]`} {
		raw := strings.Replace(string(catalogJSON), `[
        "tailscale",
        "netbird"
      ]`, replacement, 1)
		if _, err := Parse([]byte(raw)); err == nil {
			t.Fatalf("accepted provider list %s", replacement)
		}
	}
}

// Drive's phone app and web page use DSM's HTTPS address, so a Cloudflare web
// address needs nothing on the device; Drive Client sync is TCP, which only a
// private network carries, so Tailscale and its app come first.
func TestDriveDependsOnTheConnectingDevice(t *testing.T) {
	byID := map[string]Option{}
	for _, o := range Default().Options {
		byID[o.ID] = o
	}
	web := Recommend(Default(), "services-web", Facts{}).Options[0]
	if web.ID != "cloudflare-domain" || byID[web.ID].Install.App != "none" || byID[web.ID].Install.Phone != "yes" || web.Next[0].Input["tcpPorts"] != nil {
		t.Fatalf("web: %+v", web)
	}
	desktop := Recommend(Default(), "services-desktop", Facts{}).Options
	if desktop[0].ID != "tailscale" || desktop[0].Suitability != "recommended" || byID["tailscale"].Install.App != "tailscale" {
		t.Fatalf("desktop: %v", ids(Recommend(Default(), "services-desktop", Facts{})))
	}
}

// The sign-in in front of the NAS's own: only Cloudflare's own domain offers
// an email gate (Cloudflare Access); QuickConnect, DDNS and IPv6 have none,
// and QuickConnect says every NAS shares its domain.
func TestCatalogSaysWhatSignInStandsInFront(t *testing.T) {
	want := map[string]string{"cloudflare-quick": "none", "cloudflare-workers": "dsm-sign-in", "cloudflare-domain": "cloudflare-access",
		"tailscale": "network-sign-in", "netbird": "network-sign-in", "quickconnect": "none", "ddns": "none", "ipv6": "none"}
	for _, o := range Default().Options {
		if o.Gate != want[o.ID] {
			t.Errorf("%s gate %q, want %q", o.ID, o.Gate, want[o.ID])
		}
		if o.ID == "quickconnect" && !contains(o.Limits, "shared-domain") {
			t.Error("QuickConnect does not say its domain is shared")
		}
		if o.ID == "ddns" && (o.Protection != "none" || o.Via != "direct" || !contains(o.Limits, "port-forwarding")) {
			t.Errorf("ddns: %+v", o)
		}
	}
}
