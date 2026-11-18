package app

import (
	"context"
	"crypto/x509"
	"net/netip"
	"testing"

	"dsmapp/internal/phonelink"
	"dsmapp/internal/ports"
)

func TestPhoneAddNASListsLANAddressesReadOnly(t *testing.T) {
	cfg := testConfig(t)
	cfg.PhoneLink = &phonelink.Source{
		Ports:    func(context.Context) (ports.DSMPorts, error) { return ports.DSMPorts{HTTP: 5000, HTTPS: 5001}, nil },
		Hostname: func() (string, error) { return "DiskStation", nil },
		Interfaces: func() ([]phonelink.Interface, error) {
			return []phonelink.Interface{{Name: "eth0", Addrs: []netip.Addr{netip.MustParseAddr("192.168.1.20")}}}, nil
		},
		Probe: func(context.Context, int, string) ([]*x509.Certificate, error) { return nil, context.Canceled },
	}
	code, out := call(t, cfg, alice, "phone.addnas", `{"protocol":"https:","host":"192.168.1.20","port":5001}`)
	data, _ := out["data"].(map[string]any)
	addresses, _ := data["addresses"].([]any)
	if code != "" || data["name"] != "DiskStation" || data["ports"] != true || len(addresses) != 1 {
		t.Fatalf("got %q %v", code, out)
	}
	if a := addresses[0].(map[string]any); a["url"] != "https://192.168.1.20:5001" || a["kind"] != "lan" {
		t.Fatalf("got %v", a)
	}
	assertDataDirEmpty(t, cfg)
	for _, bad := range []string{`{"token":"x"}`, `{"host":1}`, `{"port":70000}`} {
		if code, _ := call(t, cfg, alice, "phone.addnas", bad); code == "" {
			t.Errorf("%s accepted", bad)
		}
	}
}
