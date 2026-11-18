package cloudflare

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"dsmapp/internal/connector"
)

const dsmAuth = `{"data":{"SYNO.API.Auth":{"maxVersion":7,"minVersion":1,"path":"entry.cgi"}},"success":true}`

func probeServer(t *testing.T, handle http.HandlerFunc) (string, *http.Client) {
	t.Helper()
	s := httptest.NewTLSServer(handle)
	t.Cleanup(s.Close)
	return s.URL, s.Client()
}

func TestProbeAcceptsDSMReachedThroughTheAddress(t *testing.T) {
	var nonces []string
	address, client := probeServer(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.URL.Path != "/webapi/query.cgi" || q.Get("api") != "SYNO.API.Info" || q.Get("method") != "query" || q.Get("query") != "SYNO.API.Auth" || q.Get("version") != "1" {
			t.Errorf("request %s", r.URL)
		}
		if r.Header.Get("Cache-Control") != "no-cache, no-store" || r.Header.Get("Pragma") != "no-cache" || r.Header.Get("Cookie") != "" {
			t.Errorf("headers %v", r.Header)
		}
		nonces = append(nonces, q.Get("nonce"))
		w.Write([]byte(dsmAuth))
	})
	p := Probe{Client: client}
	for range 2 {
		if e := p.Check(context.Background(), address); e != nil {
			t.Fatal(e)
		}
	}
	if len(nonces) != 2 || len(nonces[0]) != 32 || nonces[0] == nonces[1] {
		t.Fatalf("nonces %q", nonces)
	}
}

func TestProbeNamesWhyTheAddressDidNotReachDSM(t *testing.T) {
	cases := map[string]struct {
		handle http.HandlerFunc
		code   string
	}{
		"new tunnel 1033": {func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(530)
			w.Write([]byte("<html>Error 1033 Argo Tunnel error</html>"))
		}, "cloudflare_tunnel_pending"},
		"stopped tunnel 530": {func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(530) }, "cloudflare_tunnel_pending"},
		"origin down 502":    {func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(502) }, "cloudflare_origin_unreachable"},
		"generic 200":        {func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("<html>hello</html>")) }, "cloudflare_not_dsm"},
		"access login": {func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`<html><title>Sign in ・ Cloudflare Access</title></html>`))
		}, "cloudflare_not_dsm"},
		"dsm failure": {func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"success":false,"error":{"code":102}}`))
		}, "cloudflare_not_dsm"},
		"wrong path": {func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"data":{"SYNO.API.Auth":{"maxVersion":7,"minVersion":1,"path":"auth.cgi"}},"success":true}`))
		}, "cloudflare_not_dsm"},
		"bad versions": {func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(`{"data":{"SYNO.API.Auth":{"maxVersion":1,"minVersion":7,"path":"entry.cgi"}},"success":true}`))
		}, "cloudflare_not_dsm"},
		"redirect": {func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "https://elsewhere.example/webapi/query.cgi", http.StatusFound)
		}, "cloudflare_not_dsm"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			address, client := probeServer(t, c.handle)
			e := Probe{Client: client}.Check(context.Background(), address)
			if got := connector.ErrorCode(e); got != c.code {
				t.Fatalf("code %q (%v), want %q", got, e, c.code)
			}
		})
	}
}

func TestProbeNeverSkipsCertificateVerification(t *testing.T) {
	address, _ := probeServer(t, func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(dsmAuth)) })
	if code := connector.ErrorCode(Probe{}.Check(context.Background(), address)); code != "cloudflare_tls_failed" {
		t.Fatalf("code %q", code)
	}
}

func TestProbeReportsANameThatDoesNotResolveYet(t *testing.T) {
	client := &http.Client{Transport: &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) {
		return nil, &net.DNSError{Err: "no such host", Name: "nas.example.com", IsNotFound: true}
	}}}
	e := Probe{Client: client}.Check(context.Background(), "https://nas.example.com")
	if code := connector.ErrorCode(e); code != "cloudflare_dns_pending" {
		t.Fatalf("code %q (%v)", code, e)
	}
}

func TestProbeRefusesAnAddressThatIsNotAnHTTPSOrigin(t *testing.T) {
	for _, address := range []string{"", "http://nas.example.com", "https://nas.example.com/path", "https://u@nas.example.com"} {
		if e := (Probe{}).Check(context.Background(), address); connector.ErrorCode(e) != "cloudflare_not_dsm" {
			t.Errorf("%q: %v", address, e)
		}
	}
}

func TestProbeForChecksCloudflareFromOutsideAndLeavesOthersAlone(t *testing.T) {
	address, client := probeServer(t, func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(dsmAuth)) })
	statuses := map[string]connector.Status{
		"cloudflare": {State: "connected", Endpoints: []connector.Endpoint{{URL: address, Public: true}}},
	}
	others := 0
	probe := Probe{Client: client}.For(func(id string) connector.Status { return statuses[id] }, func(context.Context, string) (bool, error) {
		others++
		return false, nil
	})
	if external, e := probe(context.Background(), "cloudflare"); !external || e != nil {
		t.Fatalf("external %v, %v", external, e)
	}
	if _, e := probe(context.Background(), "tailscale"); e != nil || others != 1 {
		t.Fatalf("other provider: %v, %d", e, others)
	}
	statuses["cloudflare"] = connector.Status{State: "connecting"}
	if _, e := probe(context.Background(), "cloudflare"); connector.ErrorCode(e) != "not_connected" {
		t.Fatalf("not connected: %v", e)
	}
}

func TestFreshHostnameDNSGrace(t *testing.T) {
	now := time.Date(2026, 10, 26, 8, 0, 0, 0, time.UTC)
	for _, mode := range []string{"quick", "domain", "token"} {
		for _, tc := range []struct {
			name    string
			created time.Time
			failure error
			want    string
		}{
			{"fresh DNS", now.Add(-time.Second), &net.DNSError{Err: "no such host", Name: "nas.example.com", IsNotFound: true}, "check_starting"},
			{"end of grace", now.Add(-dnsGraceWindow), &net.DNSError{Err: "no such host", Name: "nas.example.com", IsNotFound: true}, "cloudflare_dns_pending"},
			{"old DNS", now.Add(-dnsGraceWindow - time.Second), &net.DNSError{Err: "no such host", Name: "nas.example.com", IsNotFound: true}, "cloudflare_dns_pending"},
			{"unknown age", time.Time{}, &net.DNSError{Err: "no such host", Name: "nas.example.com", IsNotFound: true}, "cloudflare_dns_pending"},
			{"future timestamp", now.Add(time.Second), &net.DNSError{Err: "no such host", Name: "nas.example.com", IsNotFound: true}, "cloudflare_dns_pending"},
			{"proxy DNS", now, &net.DNSError{Err: "no such host", Name: "proxy.example.com", IsNotFound: true}, "cloudflare_unreachable"},
			{"connection refused", now, errors.New("connection refused"), "cloudflare_unreachable"},
			{"TLS", now, x509.UnknownAuthorityError{}, "cloudflare_tls_failed"},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				client := &http.Client{Transport: &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) { return nil, tc.failure }}}
				p := Probe{Client: client, Now: func() time.Time { return now }}
				probe := p.For(func(string) connector.Status {
					return connector.Status{State: "connected", Mode: mode, AddressCreatedAt: tc.created, Endpoints: []connector.Endpoint{{URL: "https://nas.example.com"}}}
				}, nil)
				external, err := probe(context.Background(), "cloudflare")
				if external || connector.ErrorCode(err) != tc.want {
					t.Fatalf("external %v, error %v; want %s", external, err, tc.want)
				}
			})
		}
	}
}

func TestFreshHostnameHTTPFailureIsNotDNSGrace(t *testing.T) {
	address, client := probeServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusBadGateway) })
	probe := (Probe{Client: client}).For(func(string) connector.Status {
		return connector.Status{State: "connected", AddressCreatedAt: time.Now(), Endpoints: []connector.Endpoint{{URL: address}}}
	}, nil)
	if external, err := probe(context.Background(), "cloudflare"); external || connector.ErrorCode(err) != "cloudflare_origin_unreachable" {
		t.Fatalf("%v %v", external, err)
	}
}

func TestProbeWorkersChecksPublicDSMOnTheFreeLink(t *testing.T) {
	address, client := probeServer(t, func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(dsmAuth)) })
	url := ""
	probe := Probe{Client: client}.Workers(func() string { return url })
	if _, e := probe(context.Background(), "workers"); !errors.Is(e, connector.ErrNotConnected) {
		t.Fatalf("without DSM on the Free link: %v", e)
	}
	url = address
	if external, e := probe(context.Background(), "workers"); e != nil || !external {
		t.Fatalf("external %v, error %v", external, e)
	}
	notDSM, other := probeServer(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNotFound) })
	url = notDSM
	if _, e := (Probe{Client: other}).Workers(func() string { return url })(context.Background(), "workers"); connector.ErrorCode(e) != "cloudflare_not_dsm" {
		t.Fatalf("error %v", e)
	}
}
