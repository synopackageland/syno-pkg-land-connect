package cloudflare

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"

	"dsmapp/internal/connector"
	"dsmapp/internal/publicname"
)

func serverOrigin(f *fixture, host string) *ServerOrigin {
	o := &ServerOrigin{Hostname: host, Port: 33073, CAFile: filepath.Join(filepath.Dir(f.dir), "netbird-server", "tls", "cert.pem")}
	var mu sync.Mutex
	f.tunnel.cfg.NetBirdServer = func() (ServerOrigin, bool) {
		mu.Lock()
		defer mu.Unlock()
		if o.Hostname == "" {
			return ServerOrigin{}, false
		}
		return *o, true
	}
	return o
}

func TestServerZoneNeedsAConnectedOwnDomain(t *testing.T) {
	f := newFixture(t)
	if _, e := f.tunnel.ServerZone(); connector.ErrorCode(e) != "cloudflare_not_connected" {
		t.Fatal("zone without Cloudflare", e)
	}
	f.metrics.hostname = "a.trycloudflare.com"
	f.tunnel.Quick(context.Background())
	f.waitState("connected")
	if _, e := f.tunnel.ServerZone(); connector.ErrorCode(e) != "cloudflare_own_domain_required" {
		t.Fatal("zone on a temporary address", e)
	}
	f.tunnel.Disable()
	f.ownDomain()
	if zone, e := f.tunnel.ServerZone(); e != nil || zone != "example.com" {
		t.Fatal(zone, e)
	}
	f.tunnel.DisconnectAuthorization()
	if _, e := f.tunnel.ServerZone(); connector.ErrorCode(e) != "cloudflare_reconnect_required" {
		t.Fatal("zone without authorization", e)
	}
}

func TestServerHostnameAvoidsDSMAndUsedNames(t *testing.T) {
	f := newFixture(t)
	f.ownDomain()
	nas := publicname.NASName()
	f.taken = publicname.Candidate("netbird", nas, 0) + ".example.com"
	got, e := f.tunnel.ServerHostname(context.Background())
	if e != nil || got != publicname.Candidate("netbird", nas, 1)+".example.com" {
		t.Fatal(got, e)
	}
}

func TestServerRouteWritesVerifiedHTTP2IngressAndRestartsOnce(t *testing.T) {
	f := newFixture(t)
	f.ownDomain()
	if strings.Contains(f.config(), "protocol") || strings.Contains(f.config(), "33073") {
		t.Fatal("server ingress while off", f.config())
	}
	host := "netbird.example.com"
	o := serverOrigin(f, host)
	old := f.serving()
	before := len(f.launcher.starts())
	if e := f.tunnel.RouteServer(context.Background(), host); e != nil {
		t.Fatal(e)
	}
	f.waitState("connected")
	if !slices.Contains(f.routed(), host) || len(f.launcher.starts()) != before+1 || old.Alive() {
		t.Fatal("route or single restart missing", f.routed())
	}
	config := f.config()
	for _, want := range []string{
		"protocol: \"http2\"\n",
		"  - hostname: \"" + host + "\"\n    service: \"https://127.0.0.1:33073\"\n    originRequest:\n      http2Origin: true\n      originServerName: \"" + host + "\"\n      caPool: \"" + o.CAFile + "\"\n",
	} {
		if !strings.Contains(config, want) {
			t.Fatal(config)
		}
	}
	if strings.Contains(config, "noTLSVerify") || strings.Index(config, host) > strings.Index(config, "http_status:404") {
		t.Fatal(config)
	}
	d, _ := f.tunnel.savedDomain()
	if !slices.Contains(d.Routed, host) {
		t.Fatal("route not recorded", d.Routed)
	}
	// Routing again neither routes DNS twice nor fails.
	runs := len(f.routed())
	if e := f.tunnel.RouteServer(context.Background(), host); e != nil || len(f.routed()) != runs {
		t.Fatal("second route", e)
	}
}

func TestServerRouteRefusesForeignNames(t *testing.T) {
	f := newFixture(t)
	f.ownDomain()
	serverOrigin(f, "x")
	for _, host := range []string{"nas.example.com", "netbird.example.org", "a.netbird.example.com", "netbird.example.com.evil", ""} {
		if e := f.tunnel.RouteServer(context.Background(), host); connector.ErrorCode(e) != "cloudflare_invalid_hostname" {
			t.Fatal(host, e)
		}
	}
	f.taken = "netbird.example.com"
	if e := f.tunnel.RouteServer(context.Background(), "netbird.example.com"); e == nil {
		t.Fatal("used name routed")
	}
	if slices.Contains(f.routed(), "netbird.example.com") {
		t.Fatal("DNS written for a used name")
	}
}

func TestServerRouteNeedsAnOrigin(t *testing.T) {
	f := newFixture(t)
	f.ownDomain()
	if e := f.tunnel.RouteServer(context.Background(), "netbird.example.com"); e == nil {
		t.Fatal("routed without a running server intent")
	}
}

func TestServerUnrouteWithdrawsIngressThenDeletesOnlyItsRecord(t *testing.T) {
	f := newFixture(t)
	f.ownDomain()
	host := "netbird.example.com"
	o := serverOrigin(f, host)
	if e := f.tunnel.RouteServer(context.Background(), host); e != nil {
		t.Fatal(e)
	}
	f.waitState("connected")
	deletes := 0
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			deletes++
			if strings.Contains(f.config(), host) {
				t.Error("DNS deleted while ingress still served the name")
			}
		}
		rows := []dnsRecord{}
		if deletes == 0 && strings.HasSuffix(r.URL.Path, "/dns_records") {
			rows = append(rows, dnsRecord{ID: "owned", Name: host, Type: "CNAME", Content: tunnelID + ".cfargotunnel.com", Proxied: true})
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": rows})
	}))
	defer api.Close()
	f.tunnel.cfg.API = API{Base: api.URL}
	o.Hostname = ""
	if e := f.tunnel.UnrouteServer(context.Background(), host); e != nil || deletes != 1 {
		t.Fatal(e, deletes)
	}
	config := f.config()
	if strings.Contains(config, host) || strings.Contains(config, "protocol") || !strings.Contains(config, "nas.example.com") {
		t.Fatal(config)
	}
	d, _ := f.tunnel.savedDomain()
	if slices.Contains(d.Routed, host) || d.Hostname != "nas.example.com" {
		t.Fatal(d.Routed)
	}
	if e := f.tunnel.UnrouteServer(context.Background(), "nas.example.com"); e == nil {
		t.Fatal("DSM name removed")
	}
	if e := f.tunnel.UnrouteServer(context.Background(), host); e != nil || deletes != 1 {
		t.Fatal("unroute not idempotent", e)
	}
}

func TestServerIngressSurvivesRestore(t *testing.T) {
	f := newFixture(t)
	f.ownDomain()
	host := "netbird.example.com"
	serverOrigin(f, host)
	if e := f.tunnel.RouteServer(context.Background(), host); e != nil {
		t.Fatal(e)
	}
	f.waitState("connected")
	f.tunnel.Stop()
	if e := f.tunnel.Restore(context.Background()); e != nil {
		t.Fatal(e)
	}
	f.waitState("connected")
	if !strings.Contains(f.config(), host) {
		t.Fatal("restore dropped the server route", f.config())
	}
}
