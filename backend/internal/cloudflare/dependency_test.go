package cloudflare

import (
	"dsmapp/internal/connector"
	"dsmapp/internal/dependency"
	"os"
	"path/filepath"
	"testing"
)

func TestDependentServerPreventsAuthorizationDeletion(t *testing.T) {
	dir := t.TempDir()
	m := dependency.Model{Enabled: func() bool { return true }}
	tunnel := NewTunnel(Config{Dir: dir, CheckRemoval: m.Guard})
	if e := os.MkdirAll(filepath.Dir(tunnel.certFile()), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(tunnel.certFile(), goodCert(), 0600); e != nil {
		t.Fatal(e)
	}
	before, e := os.ReadFile(tunnel.certFile())
	if e != nil {
		t.Fatal(e)
	}
	if e := tunnel.DisconnectAuthorization(); connector.ErrorCode(e) != "netbird_cloudflare_in_use" {
		t.Fatal(e)
	}
	after, e := os.ReadFile(tunnel.certFile())
	if e != nil || string(before) != string(after) {
		t.Fatal("guard removed certificate", e)
	}
}
