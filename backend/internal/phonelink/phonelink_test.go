package phonelink

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"errors"
	"math/big"
	"net"
	"net/netip"
	"reflect"
	"testing"
	"time"

	"dsmapp/internal/ports"
)

var now = time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

type pki struct {
	roots *x509.CertPool
	ca    *x509.Certificate
	key   *ecdsa.PrivateKey
}

func newPKI(t *testing.T) pki {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Test Root"}, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour * 24), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	return pki{roots: pool, ca: ca, key: key}
}

// leaf issues a certificate for names; issuer nil makes it self-signed.
func leaf(t *testing.T, issuer *pki, names []string, ips []net.IP) *x509.Certificate {
	t.Helper()
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "synology"}, DNSNames: names, IPAddresses: ips, NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	parent, signer := tpl, any(key)
	if issuer != nil {
		parent, signer = issuer.ca, issuer.key
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, parent, &key.PublicKey, signer)
	if err != nil {
		t.Fatal(err)
	}
	c, _ := x509.ParseCertificate(der)
	return c
}

func fingerprint(c *x509.Certificate) string {
	s := sha256.Sum256(c.Raw)
	return hex.EncodeToString(s[:])
}

type probeLog struct{ calls []string }

func source(t *testing.T, p pki, chains map[string][]*x509.Certificate, log *probeLog) Source {
	return Source{
		Ports:    func(context.Context) (ports.DSMPorts, error) { return ports.DSMPorts{HTTP: 5000, HTTPS: 5001}, nil },
		Hostname: func() (string, error) { return "DiskStation", nil },
		Interfaces: func() ([]Interface, error) {
			return []Interface{
				{Name: "eth0", Addrs: []netip.Addr{netip.MustParseAddr("192.168.1.20"), netip.MustParseAddr("fe80::1")}},
				{Name: "ovs_eth1", Addrs: []netip.Addr{netip.MustParseAddr("10.0.0.5")}},
				{Name: "docker0", Addrs: []netip.Addr{netip.MustParseAddr("172.17.0.1")}},
				{Name: "wt0", Addrs: []netip.Addr{netip.MustParseAddr("100.90.1.2")}},
				{Name: "tailscale0", Addrs: []netip.Addr{netip.MustParseAddr("100.101.1.2")}},
				{Name: "eth2", Addrs: []netip.Addr{netip.MustParseAddr("203.0.113.9")}},
			}, nil
		},
		Lookup: func(_ context.Context, host string) ([]netip.Addr, error) {
			switch host {
			case "diskstation.lan":
				return []netip.Addr{netip.MustParseAddr("192.168.1.20")}, nil
			case "nas.example.com":
				return []netip.Addr{netip.MustParseAddr("198.51.100.7")}, nil
			}
			return nil, errors.New("no such host")
		},
		Probe: func(_ context.Context, port int, sni string) ([]*x509.Certificate, error) {
			if port != 5001 {
				t.Fatalf("probe port %d", port)
			}
			log.calls = append(log.calls, sni)
			if c, ok := chains[sni]; ok {
				return c, nil
			}
			return nil, errors.New("refused")
		},
		Roots: func() *x509.CertPool { return p.roots },
		Now:   func() time.Time { return now },
	}
}

func TestCollectLANFirstWithSelfSignedPin(t *testing.T) {
	p := newPKI(t)
	self := leaf(t, nil, []string{"synology"}, nil)
	log := &probeLog{}
	got := source(t, p, map[string][]*x509.Certificate{"": {self}}, log).Collect(context.Background(), Current{Protocol: "http:", Host: "10.0.0.5", Port: 5000})
	pin := fingerprint(self)
	want := Result{Name: "DiskStation", Ports: true, Addresses: []Address{
		{URL: "https://10.0.0.5:5001", Kind: "lan", SHA256: pin},
		{URL: "https://192.168.1.20:5001", Kind: "lan", SHA256: pin},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	if !reflect.DeepEqual(log.calls, []string{""}) {
		t.Fatalf("one no-SNI probe expected, got %q", log.calls)
	}
}

func TestCollectTrustedHostNameHasNoPin(t *testing.T) {
	p := newPKI(t)
	trusted := leaf(t, &p, []string{"diskstation.lan"}, nil)
	self := leaf(t, nil, nil, nil)
	log := &probeLog{}
	got := source(t, p, map[string][]*x509.Certificate{"diskstation.lan": {trusted}, "": {self}}, log).Collect(context.Background(), Current{Protocol: "https:", Host: "DiskStation.lan", Port: 5001})
	if got.Addresses[0] != (Address{URL: "https://diskstation.lan:5001", Kind: "lan"}) {
		t.Fatalf("trusted name: %+v", got.Addresses[0])
	}
	if got.Addresses[1].SHA256 != fingerprint(self) || got.Addresses[2].SHA256 != fingerprint(self) {
		t.Fatalf("IP literals pin the default certificate: %+v", got.Addresses)
	}
}

func TestCollectUntrustedIPAndExpiredCertificate(t *testing.T) {
	p := newPKI(t)
	// A CA-signed certificate that does not name the IP is still untrusted for it.
	named := leaf(t, &p, []string{"diskstation.lan"}, nil)
	got := source(t, p, map[string][]*x509.Certificate{"": {named}}, &probeLog{}).Collect(context.Background(), Current{})
	if len(got.Addresses) != 2 || got.Addresses[0].SHA256 != fingerprint(named) {
		t.Fatalf("got %+v", got)
	}
	ip := leaf(t, &p, nil, []net.IP{net.ParseIP("192.168.1.20"), net.ParseIP("10.0.0.5")})
	s := source(t, p, map[string][]*x509.Certificate{"": {ip}}, &probeLog{})
	if got := s.Collect(context.Background(), Current{}); got.Addresses[0].SHA256 != "" {
		t.Fatalf("trusted IP certificate pinned: %+v", got)
	}
	s.Now = func() time.Time { return now.Add(48 * time.Hour) }
	if got := s.Collect(context.Background(), Current{}); got.Addresses[0].SHA256 != fingerprint(ip) {
		t.Fatalf("expired certificate not pinned: %+v", got)
	}
}

func TestCollectPublicBrowserAddressIsOther(t *testing.T) {
	p := newPKI(t)
	self := leaf(t, nil, nil, nil)
	cases := []struct {
		cur  Current
		want Address
	}{
		{Current{Protocol: "https:", Host: "nas.example.com", Port: 5001}, Address{URL: "https://nas.example.com:5001", Kind: "other", SHA256: fingerprint(self)}},
		{Current{Protocol: "https:", Host: "nas.example.com"}, Address{URL: "https://nas.example.com", Kind: "other"}},
		{Current{Protocol: "http:", Host: "198.51.100.7", Port: 8080}, Address{URL: "http://198.51.100.7:8080", Kind: "other"}},
	}
	for _, c := range cases {
		got := source(t, p, map[string][]*x509.Certificate{"": {self}, "nas.example.com": {self}}, &probeLog{}).Collect(context.Background(), c.cur)
		if n := len(got.Addresses); n != 3 || got.Addresses[2] != c.want || got.Addresses[0].Kind != "lan" {
			t.Fatalf("%+v: got %+v", c.cur, got.Addresses)
		}
	}
}

func TestCollectSkipsUnusableBrowserAddresses(t *testing.T) {
	p := newPKI(t)
	for _, host := range []string{"127.0.0.1", "localhost", "100.90.1.2", "fe80::1", "[fe80::1%eth0]", "bad host"} {
		got := source(t, p, nil, &probeLog{}).Collect(context.Background(), Current{Protocol: "https:", Host: host, Port: 5001})
		for _, a := range got.Addresses {
			if a.Kind != "lan" || (a.URL != "https://192.168.1.20:5001" && a.URL != "https://10.0.0.5:5001") {
				t.Fatalf("%q: unexpected %+v", host, got.Addresses)
			}
		}
	}
}

func TestCollectLocalNamesAreLAN(t *testing.T) {
	for _, host := range []string{"diskstation", "diskstation.local"} {
		got := source(t, newPKI(t), nil, &probeLog{}).Collect(context.Background(), Current{Protocol: "http:", Host: host, Port: 5000})
		if got.Addresses[0] != (Address{URL: "https://" + host + ":5001", Kind: "lan"}) {
			t.Fatalf("%s: got %+v", host, got.Addresses)
		}
	}
	got := source(t, newPKI(t), nil, &probeLog{}).Collect(context.Background(), Current{Protocol: "https:", Host: "unknown.example", Port: 5001})
	if last := got.Addresses[len(got.Addresses)-1]; last.Kind != "other" || last.URL != "https://unknown.example:5001" {
		t.Fatalf("unresolved public name: %+v", got.Addresses)
	}
}

func TestCollectIPv6LANAndLimits(t *testing.T) {
	p := newPKI(t)
	s := source(t, p, nil, &probeLog{})
	got := s.Collect(context.Background(), Current{Protocol: "https:", Host: "[fd00::20]", Port: 5001})
	if got.Addresses[0].URL != "https://[fd00::20]:5001" {
		t.Fatalf("got %+v", got.Addresses)
	}
	s.Interfaces = func() ([]Interface, error) {
		var many []netip.Addr
		for i := 1; i <= 10; i++ {
			many = append(many, netip.AddrFrom4([4]byte{192, 168, 9, byte(i)}))
		}
		return []Interface{{Name: "bond0", Addrs: many}}, nil
	}
	got = s.Collect(context.Background(), Current{Protocol: "https:", Host: "nas.example.com", Port: 5001})
	lan := 0
	for _, a := range got.Addresses {
		if a.Kind == "lan" {
			lan++
		}
	}
	if lan != MaxLAN || got.Addresses[len(got.Addresses)-1].Kind != "other" {
		t.Fatalf("got %+v", got.Addresses)
	}
}

func TestCollectWithoutPortsOffersNothing(t *testing.T) {
	s := source(t, newPKI(t), nil, &probeLog{})
	s.Ports = func(context.Context) (ports.DSMPorts, error) { return ports.DSMPorts{}, ports.ErrDetection }
	got := s.Collect(context.Background(), Current{Host: "192.168.1.20"})
	if got.Ports || len(got.Addresses) != 0 || got.Name != "DiskStation" {
		t.Fatalf("got %+v", got)
	}
	// Only HTTP known: http origins, no probe.
	s.Ports = func(context.Context) (ports.DSMPorts, error) { return ports.DSMPorts{HTTP: 5000}, nil }
	s.Probe = func(context.Context, int, string) ([]*x509.Certificate, error) { t.Fatal("probed"); return nil, nil }
	got = s.Collect(context.Background(), Current{Host: "192.168.1.20"})
	if got.Addresses[0] != (Address{URL: "http://192.168.1.20:5000", Kind: "lan"}) {
		t.Fatalf("got %+v", got)
	}
}
