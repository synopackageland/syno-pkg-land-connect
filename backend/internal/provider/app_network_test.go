package provider

import (
	"context"
	"testing"
)

func TestAppNetworkReadsWithoutConfiguringRoutes(t *testing.T) {
	session := &tsSession{snapshot: TailscaleSnapshot{BackendState: "Running", DNSName: "nas.example.ts.net.", CertDomains: []string{"nas.example.ts.net"}}}
	p := NewTailscale(t.TempDir(), nil)
	p.session = session
	observed, e := p.AppNetwork(context.Background())
	if e != nil || observed.DNSName != "nas.example.ts.net." {
		t.Fatal(observed, e)
	}
	observed.CertDomains[0] = "changed"
	if p.saved.Enabled || p.configured || session.snapshot.CertDomains[0] != "nas.example.ts.net" {
		t.Fatal("read mutated provider or shared evidence")
	}
}
