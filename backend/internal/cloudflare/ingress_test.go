package cloudflare

import (
	"context"
	"strings"
	"testing"
	"time"
)

type machineTargets struct{ target AppRoute }

func (m *machineTargets) ActivePublic(context.Context) ([]AppRoute, error) {
	return []AppRoute{m.target}, nil
}
func (m *machineTargets) AppExists(_ context.Context, pkg string) bool {
	return m.target.Package == pkg
}
func (*machineTargets) ActivatePublic(context.Context, []AppRoute) error      { return nil }
func (*machineTargets) PausePublic(context.Context, []AppRoute, string) error { return nil }

func TestMachineIngressKeepsExactPathAcrossConnectorRestart(t *testing.T) {
	f := newFixture(t)
	f.ownDomain()
	m := &machineTargets{target: appTarget("custom.ingress-fixture", "reverse.example.com", "reverse")}
	m.target.Path = "/reverse/ws"
	f.tunnel.cfg.Apps = m
	f.tunnel.cfg.IngressOnly = func() bool { return true }
	// No DSM target is available or authorized in the machine-only tunnel.
	f.tunnel.cfg.DSMTarget = nil
	if err := f.tunnel.ReconcileAppRoutes(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertRules := func() {
		t.Helper()
		config := f.config()
		if !strings.Contains(config, `path: "^/reverse/ws$"`) || strings.Count(config, `hostname: "reverse.example.com"`) != 2 || !strings.Contains(config, "service: http_status:404") {
			t.Fatal(config)
		}
		if strings.Contains(config, `hostname: "nas.example.com"`) || strings.Contains(config, "5000") || strings.Contains(config, "5001") {
			t.Fatal("DSM exposed", config)
		}
	}
	assertRules()
	before := len(f.launcher.starts())
	f.serving().exit()
	deadline := time.Now().Add(3 * time.Second)
	for len(f.launcher.starts()) == before && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(f.launcher.starts()) != before+1 {
		t.Fatal("connector did not restart")
	}
	f.waitState("connected")
	assertRules()
	if err := f.tunnel.Disable(); err != nil {
		t.Fatal(err)
	}
	before = len(f.launcher.starts())
	time.Sleep(100 * time.Millisecond)
	if len(f.launcher.starts()) != before {
		t.Fatal("disabled connector restarted")
	}
	// Invalid path metadata fails closed at the provider boundary too.
	m.target.Path = "/reverse/.*"
	if _, err := f.tunnel.appIngress(domain{Hostname: "nas.example.com"}); err == nil {
		t.Fatal("unbounded edge path accepted")
	}
}
