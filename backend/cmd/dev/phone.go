package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net/netip"
	"sync"
	"time"

	"dsmapp/internal/app"
	"dsmapp/internal/phonelink"
	"dsmapp/internal/ports"
)

// phoneFixture answers phone.addnas from a fixed LAN and a self-signed
// certificate made at startup, so the dialog never shows this machine's own
// addresses and no handshake leaves the process.
func phoneFixture(cfg *app.Config) {
	cert := sync.OnceValue(func() *x509.Certificate {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "synology"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour)}
		der, _ := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
		c, _ := x509.ParseCertificate(der)
		return c
	})
	cfg.PhoneLink = &phonelink.Source{
		Ports:    func(context.Context) (ports.DSMPorts, error) { return ports.DSMPorts{HTTP: 5000, HTTPS: 5001}, nil },
		Hostname: func() (string, error) { return "DiskStation", nil },
		Interfaces: func() ([]phonelink.Interface, error) {
			return []phonelink.Interface{{Name: "eth0", Addrs: []netip.Addr{netip.MustParseAddr("192.168.1.20")}}}, nil
		},
		Lookup: func(context.Context, string) ([]netip.Addr, error) { return nil, context.Canceled },
		Probe: func(context.Context, int, string) ([]*x509.Certificate, error) {
			return []*x509.Certificate{cert()}, nil
		},
	}
}
