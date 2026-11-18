package publicnet

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func dohServer(t *testing.T, status int, ip string) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("name") != "example.com" || r.Header.Get("Accept") != "application/dns-json" {
			t.Errorf("query %s", r.URL)
		}
		answer := []map[string]any{}
		if r.URL.Query().Get("type") == "A" && ip != "" {
			answer = append(answer, map[string]any{"name": "example.com.", "type": 1, "data": ip})
		}
		json.NewEncoder(w).Encode(map[string]any{"Status": status, "Answer": answer})
	}))
	t.Cleanup(s.Close)
	return s
}

func TestDialBypassesCachedSystemNXDOMAIN(t *testing.T) {
	s := dohServer(t, 0, "192.0.2.1")
	var calls atomic.Int32
	d := Dialer{Lookup: noPublicDNS, Client: s.Client(), Endpoints: []string{s.URL}, Dial: func(_ context.Context, network, address string) (net.Conn, error) {
		calls.Add(1)
		if address != "192.0.2.1:443" {
			return nil, &net.DNSError{Name: "example.com", Err: "cached NXDOMAIN", IsNotFound: true}
		}
		a, b := net.Pipe()
		b.Close()
		return a, nil
	}}
	c, err := d.DialContext(context.Background(), "tcp", "example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if calls.Load() != 1 {
		t.Fatalf("dials %d", calls.Load())
	}
}

func TestFallbackOnlyWhenDoHUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     int
		wantSystem bool
	}{{"unreachable", -1, true}, {"NXDOMAIN", 3, false}, {"no addresses", 0, false}, {"SERVFAIL", 2, false}} {
		t.Run(tc.name, func(t *testing.T) {
			s := dohServer(t, tc.status, "")
			if tc.status < 0 {
				s.Close()
			}
			calls := 0
			d := Dialer{Lookup: noPublicDNS, Client: s.Client(), Endpoints: []string{s.URL}, Dial: func(context.Context, string, string) (net.Conn, error) {
				calls++
				return nil, errors.New("system fallback")
			}}
			_, err := d.DialContext(context.Background(), "tcp", "example.com:443")
			if err == nil || (calls > 0) != tc.wantSystem {
				t.Fatalf("system %d error %v", calls, err)
			}
		})
	}
}

func TestDoHUsesSecondProvider(t *testing.T) {
	bad := dohServer(t, 0, "")
	bad.Close()
	good := dohServer(t, 0, "192.0.2.2")
	d := Dialer{Lookup: noPublicDNS, Client: good.Client(), Endpoints: []string{bad.URL, good.URL}, Dial: func(_ context.Context, _, address string) (net.Conn, error) {
		if address != "192.0.2.2:443" {
			t.Fatalf("dial %s", address)
		}
		a, b := net.Pipe()
		b.Close()
		return a, nil
	}}
	c, err := d.DialContext(context.Background(), "tcp", "example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}

func TestTransportPreservesHostSNIAndCertificateVerification(t *testing.T) {
	var host, sni string
	origin := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host = r.Host
		sni = r.TLS.ServerName
		w.Write([]byte("ok"))
	}))
	defer origin.Close()
	dns := dohServer(t, 0, "192.0.2.1")
	d := Dialer{Lookup: noPublicDNS, Client: dns.Client(), Endpoints: []string{dns.URL}, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		if !strings.HasPrefix(address, "192.0.2.1:") {
			t.Errorf("system DNS %s", address)
			return nil, errors.New("unexpected system lookup")
		}
		return (&net.Dialer{}).DialContext(ctx, network, origin.Listener.Addr().String())
	}}
	tr := d.Transport()
	defer tr.CloseIdleConnections()
	tr.TLSClientConfig = origin.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	// A pooled trusted connection would let the second request skip verification.
	tr.DisableKeepAlives = true
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}
	res, err := client.Get("https://example.com/")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if host != "example.com" || sni != "example.com" {
		t.Fatalf("Host %s SNI %s", host, sni)
	}
	tr.CloseIdleConnections()
	tr.TLSClientConfig = nil
	if res, err = client.Get("https://example.com/"); err == nil {
		res.Body.Close()
		t.Fatal("untrusted certificate accepted")
	}
}

func TestCanceledLookupNeverFallsBack(t *testing.T) {
	s := dohServer(t, 0, "192.0.2.1")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	d := Dialer{Lookup: noPublicDNS, Client: s.Client(), Endpoints: []string{s.URL}, Dial: func(context.Context, string, string) (net.Conn, error) { t.Fatal("dial after cancel"); return nil, nil }}
	if _, err := d.DialContext(ctx, "tcp", "example.com:443"); !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
}

func noPublicDNS(context.Context, string) ([]netip.Addr, error) {
	return nil, errors.New("public DNS unavailable")
}

func TestPublicDNSFallbackBypassesSystemCacheWhenDoHIsBlocked(t *testing.T) {
	s := dohServer(t, 0, "")
	s.Close()
	d := Dialer{Client: s.Client(), Endpoints: []string{s.URL}, Lookup: func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("192.0.2.1")}, nil
	}, Dial: func(_ context.Context, _, address string) (net.Conn, error) {
		if address != "192.0.2.1:443" {
			return nil, &net.DNSError{Name: "example.com", Err: "cached NXDOMAIN", IsNotFound: true}
		}
		a, b := net.Pipe()
		b.Close()
		return a, nil
	}}
	c, err := d.DialContext(context.Background(), "tcp", "example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	d.Lookup = func(context.Context, string) ([]netip.Addr, error) {
		return nil, &net.DNSError{Name: "example.com", Err: "NXDOMAIN", IsNotFound: true}
	}
	if _, err = d.DialContext(context.Background(), "tcp", "example.com:443"); err == nil {
		t.Fatal("public NXDOMAIN fell through to system DNS")
	}
}

func TestUnreachableAddressTriesTheOtherFamily(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		typ, data := 1, "192.0.2.1"
		if r.URL.Query().Get("type") == "AAAA" {
			typ, data = 28, "2001:db8::1"
		}
		json.NewEncoder(w).Encode(map[string]any{"Status": 0, "Answer": []any{map[string]any{"type": typ, "data": data}}})
	}))
	defer s.Close()
	var addresses []string
	d := Dialer{Lookup: noPublicDNS, Client: s.Client(), Endpoints: []string{s.URL}, Dial: func(_ context.Context, _, address string) (net.Conn, error) {
		addresses = append(addresses, address)
		if address == "192.0.2.1:443" {
			return nil, errors.New("unreachable")
		}
		a, b := net.Pipe()
		b.Close()
		return a, nil
	}}
	c, err := d.DialContext(context.Background(), "tcp", "example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	if len(addresses) != 2 || addresses[1] != "[2001:db8::1]:443" {
		t.Fatalf("dials %v", addresses)
	}
}
