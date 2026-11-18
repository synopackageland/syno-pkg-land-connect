package cloudflare

import (
	"context"
	"os"
	"testing"
)

func TestRetainedDataRestoresOwnDomainWithoutSigningIn(t *testing.T) {
	f := newFixture(t)
	f.ownDomain()
	original, _ := f.tunnel.savedDomain()
	cert, _ := os.ReadFile(f.tunnel.certFile())
	f.tunnel.Stop()
	resume, err := Retain(f.dir)
	if err != nil || !resume {
		t.Fatalf("retain: %v %v", resume, err)
	}
	restarted := NewTunnel(f.tunnel.cfg)
	defer restarted.Stop()
	if err = restarted.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := restarted.savedDomain()
	if err != nil || got.TunnelID != original.TunnelID || got.Hostname != original.Hostname {
		t.Fatal("identity lost", got, err)
	}
	after, _ := os.ReadFile(restarted.certFile())
	if string(cert) != string(after) {
		t.Fatal("authorization lost")
	}
	if restarted.Status().SuggestedName != "nas" {
		t.Fatal("saved subdomain not preferred", restarted.Status().SuggestedName)
	}
	if f.logins() != 1 {
		t.Fatal("retention required another login")
	}
}

func TestRetainedDataDoesNotResumeAnExplicitDisconnect(t *testing.T) {
	f := newFixture(t)
	f.ownDomain()
	f.tunnel.Disable()
	if _, err := Retain(f.dir); err != nil {
		t.Fatal(err)
	}
	restarted := NewTunnel(f.tunnel.cfg)
	defer restarted.Stop()
	before := len(f.launcher.starts())
	restarted.Restore(context.Background())
	if len(f.launcher.starts()) != before || restarted.loadSettings().Enabled {
		t.Fatal("disconnect was undone")
	}
	if _, err := restarted.savedDomain(); err != nil {
		t.Fatal("saved identity removed", err)
	}
}
