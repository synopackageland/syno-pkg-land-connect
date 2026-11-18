package cloudflare

import (
	"context"
	"dsmapp/internal/connector"
	"testing"
)

func TestDSMNameUsesAppConflictPreflightWithoutChangingDomain(t *testing.T) {
	f := newFixture(t)
	f.ownDomain()
	installTargets(f)
	f.taken = "taken.example.com"
	ctx := context.Background()
	err := f.tunnel.CheckDSMName(ctx, "taken.example.com")
	if connector.ErrorCode(err) != "cloudflare_name_taken" {
		t.Fatal("not the App name conflict", err)
	}
	err = f.tunnel.SetDSMName(ctx, "taken.example.com")
	if connector.ErrorCode(err) != "cloudflare_name_taken" {
		t.Fatal("publication skipped preflight", err)
	}
	domain, _ := f.tunnel.AppDomain()
	if domain.Hostname != "nas.example.com" {
		t.Fatal("conflict changed DSM hostname")
	}
	if err = f.tunnel.CheckDSMName(ctx, "different.other.example"); err == nil {
		t.Fatal("foreign suffix accepted")
	}
}
