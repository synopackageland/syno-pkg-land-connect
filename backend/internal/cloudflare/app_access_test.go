package cloudflare

import (
	"context"
	"testing"
)

func TestWholeBatchPreflightCreatesNothing(t *testing.T) {
	f := newFixture(t)
	f.ownDomain()
	beforeRoutes, beforeStarts := len(f.routed()), len(f.launcher.starts())
	routes := []AppRoute{appTarget("One", "one.example.com", "one"), appTarget("Two", "two.example.com", "two")}
	if e := f.tunnel.CheckPublicApps(context.Background(), routes); e != nil {
		t.Fatal(e)
	}
	f.taken = "two.example.com"
	if e := f.tunnel.CheckPublicApps(context.Background(), routes); e == nil {
		t.Fatal("preflight missed collision")
	}
	if len(f.routed()) != beforeRoutes || len(f.launcher.starts()) != beforeStarts {
		t.Fatal("preflight created a route or process")
	}
	a := &appTargetsFixture{}
	if e := f.tunnel.SetAppTargets(a); e != nil || f.tunnel.cfg.Apps != a {
		t.Fatal(e)
	}
}
