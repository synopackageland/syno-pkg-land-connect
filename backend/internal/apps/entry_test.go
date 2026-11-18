package apps

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestIngressDiscoveryAfterPortUpgrade(t *testing.T) {
	d := Discovery{PackagesDir: t.TempDir(), DetectPorts: fixturePorts, Ping: func(context.Context, string, int) string { return Ready }}
	g := gatewayFixture("ExampleMusic", SDKPing, 28443)
	g.External = &ExternalPolicy{Connect: "optional", OriginChange: "none"}
	g.Entry = "app-privilege"
	g.Access = "dsm-users"
	writeDiscoveryGateway(t, d.root(), g.Package, g)
	before, err := d.Scan(context.Background())
	if err != nil || len(before) != 1 {
		t.Fatalf("before: %+v %v", before, err)
	}
	g.Listen.HTTPS = 0
	g.IngressPath = "/synopkgland/ExampleMusic/"
	writeDiscoveryGateway(t, d.root(), g.Package, g)
	os.WriteFile(filepath.Join(d.root(), g.Package, "INFO"), []byte("displayname=\"Music\"\n"), 0644)
	after, err := d.Scan(context.Background())
	if err != nil || len(after) != 1 {
		t.Fatalf("after: %+v %v", after, err)
	}
	a := after[0]
	if a.Name != "Music" || a.Port != 5001 || a.Entry.Path != g.IngressPath || a.Entry.Package != g.Package || a.Summary != before[0].Summary || a.NeedsAppUpdate {
		t.Fatalf("entry was lost: %+v", a)
	}
	candidates := d.CustomCandidates(context.Background(), nil, nil)
	if len(candidates) < 2 || candidates[1].Package != g.Package || candidates[1].Name != "Music" {
		t.Fatalf("picker: %+v", candidates)
	}
}

func TestIngressDeclarationIsolation(t *testing.T) {
	for _, p := range []string{"/", "/webman/", "/synopkgland/Other/", "/music/../webman/", "/music/%2f/", "//music/", "/music"} {
		d := Discovery{PackagesDir: t.TempDir()}
		g := gatewayFixture("ExampleMusic", SDKPing, 0)
		g.IngressPath = p
		writeDiscoveryGateway(t, d.root(), g.Package, g)
		if _, err := d.ReadGateway(g.Package); err == nil {
			t.Errorf("accepted %q", p)
		}
	}
}

func TestIngressRequestScope(t *testing.T) {
	e := PackageEntry{Package: "ExampleMusic", Path: "/music/", Port: 5001}
	for _, p := range []string{"/music/", "/music/app/", "/music/_sso/return", "/webman/3rdparty/ExampleMusic/sso/authorize.cgi", "/webman/3rdparty/ExampleMusic/sso/open.cgi"} {
		if !e.AllowsPath(p) {
			t.Errorf("rejected %s", p)
		}
	}
	for _, p := range []string{"/webapi/entry.cgi", "/webman/3rdparty/Other/sso/open.cgi", "/synopkgland/Other/", "/music/../webman/", "/music/%2e%2e/webman/", "/music//../webman/", "/musicish/"} {
		if e.AllowsPath(p) {
			t.Errorf("exposed %s", p)
		}
	}
}

func TestProtectionIncludesAppGate(t *testing.T) {
	g := gatewayFixture("ExampleMusic", SDKPing, 28443)
	g.Entry = "app-privilege"
	g.Access = "dsm-users"
	_, original := ProtectionOf(g)
	for _, edit := range []func(*Gateway){func(g *Gateway) { g.Entry = "admin-only" }, func(g *Gateway) { g.Access = "public" }} {
		changed := g
		edit(&changed)
		_, sum := ProtectionOf(changed)
		if sum == original {
			t.Fatal("gate change escaped approval")
		}
	}
}

func TestFollowPackageEntryAutoManualAndNoChurn(t *testing.T) {
	for _, scenario := range []string{"auto", "legacy-review", "protection-review", "certificate-review", "paused"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			l := fixtureLifecycle(t.TempDir())
			d := Discovery{PackagesDir: t.TempDir(), DetectPorts: fixturePorts}
			g := gatewayFixture("ExampleApp", SDKPing, 28443)
			g.Entry = "app-privilege"
			g.Access = "dsm-users"
			g.External = &ExternalPolicy{Connect: "optional", OriginChange: "none"}
			_, sum := ProtectionOf(g)
			scope := fixtureScope(Public)
			scope.Binding.Port = 28443
			scope.Binding.Summary = sum
			scope.Binding.Entry = &PackageEntry{Package: g.Package, Port: 28443}
			if scenario == "legacy-review" {
				scope.Binding.Entry = nil
			}
			if err := l.Open(ctx, Target{Package: g.Package, Kind: Public, Scope: *scope}, false); err != nil {
				t.Fatal(err)
			}
			if scenario == "paused" {
				if err := l.Close(ctx, g.Package, Public, "admin_paused"); err != nil {
					t.Fatal(err)
				}
			}
			g.Listen.HTTPS = 0
			g.IngressPath = "/synopkgland/ExampleApp/"
			if scenario == "protection-review" {
				g.Entry = "admin-only"
			}
			if scenario == "certificate-review" {
				l.Runtime.(*fixtureRuntime).invalid = true
			}
			writeDiscoveryGateway(t, d.root(), g.Package, g)
			before, _ := l.Store.Load()
			if err := FollowEntries(ctx, l, d); err != nil {
				t.Fatal(err)
			}
			after, err := l.Store.Load()
			if err != nil {
				t.Fatal(err)
			}
			got := after.Apps[g.Package].Public
			if got.Authority != scope.Authority || got.Socket != scope.Socket {
				t.Fatal("hostname or route identity changed")
			}
			if scenario == "auto" {
				if got.Binding.Port != 5001 || EntryPath(got.Binding.Entry) != g.IngressPath || got.State != Active || len(after.EntryActivity) != 1 {
					t.Fatalf("not followed: %+v", after)
				}
			} else if got.State != Paused || len(after.EntryActivity) != 0 {
				t.Fatalf("approval bypass: %+v", after)
			}
			if scenario == "paused" && after.NextRev != before.NextRev {
				t.Fatal("manual pause was changed")
			}
			raw, _ := os.ReadFile(filepath.Join(l.Store.Dir, "apps-state.json"))
			if err := FollowEntries(ctx, l, d); err != nil {
				t.Fatal(err)
			}
			again, _ := l.Store.Load()
			if again.NextRev != after.NextRev || len(again.EntryActivity) != len(after.EntryActivity) {
				t.Fatal("refresh churn")
			}
			unchanged, _ := os.ReadFile(filepath.Join(l.Store.Dir, "apps-state.json"))
			if string(raw) != string(unchanged) {
				t.Fatal("state rewritten on no-op refresh")
			}
		})
	}
}

type entryInterleaveRuntime struct {
	*fixtureRuntime
	validate func()
}

func (r *entryInterleaveRuntime) Validate(context.Context, Target) error {
	if r.validate != nil {
		r.validate()
	}
	return nil
}
func TestFollowEntryCannotUndoConcurrentTurnOff(t *testing.T) {
	ctx := context.Background()
	l := fixtureLifecycle(t.TempDir())
	d := Discovery{PackagesDir: t.TempDir(), DetectPorts: fixturePorts}
	g := gatewayFixture("ExampleApp", SDKPing, 28443)
	g.External = &ExternalPolicy{Connect: "optional", OriginChange: "none"}
	_, sum := ProtectionOf(g)
	scope := fixtureScope(Public)
	scope.Binding.Port = 28443
	scope.Binding.Summary = sum
	scope.Binding.Entry = &PackageEntry{Package: g.Package, Port: 28443}
	if err := l.Open(ctx, Target{Package: g.Package, Kind: Public, Scope: *scope}, false); err != nil {
		t.Fatal(err)
	}
	g.Listen.HTTPS = 0
	g.IngressPath = "/music/"
	writeDiscoveryGateway(t, d.root(), g.Package, g)
	rt := &entryInterleaveRuntime{fixtureRuntime: l.Runtime.(*fixtureRuntime)}
	l.Runtime = rt
	rt.validate = func() {
		rt.validate = nil
		if err := l.Close(ctx, g.Package, Public, "admin_paused"); err != nil {
			t.Fatal(err)
		}
	}
	if err := FollowEntries(ctx, l, d); err != nil {
		t.Fatal(err)
	}
	state, _ := l.Store.Load()
	sc := state.Apps[g.Package].Public
	if sc.State != Paused || sc.Reason != "admin_paused" || len(state.EntryActivity) != 0 {
		t.Fatal("concurrent off was undone")
	}
}

func TestFollowEntriesInBothDirections(t *testing.T) {
	for _, c := range []struct {
		name, before, after string
		oldPort, newPort    int
	}{{"port-path", "", "/music/", 28443, 5001}, {"path-port", "/music/", "", 5001, 28443}, {"path-path", "/music/", "/library/", 5001, 5001}, {"port-port", "", "", 28443, 29443}} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			l := fixtureLifecycle(t.TempDir())
			d := Discovery{PackagesDir: t.TempDir(), DetectPorts: fixturePorts}
			g := gatewayFixture("ExampleApp", SDKPing, c.newPort)
			g.External = &ExternalPolicy{Connect: "optional", OriginChange: "none"}
			g.IngressPath = c.after
			if c.after != "" {
				g.Listen.HTTPS = 0
			}
			_, sum := ProtectionOf(g)
			scope := fixtureScope(Public)
			scope.Binding.Port = c.oldPort
			scope.Binding.Summary = sum
			scope.Binding.Entry = &PackageEntry{Package: g.Package, Port: c.oldPort, Path: c.before}
			if err := l.Open(ctx, Target{Package: g.Package, Kind: Public, Scope: *scope}, false); err != nil {
				t.Fatal(err)
			}
			writeDiscoveryGateway(t, d.root(), g.Package, g)
			if err := FollowEntries(ctx, l, d); err != nil {
				t.Fatal(err)
			}
			state, err := l.Store.Load()
			if err != nil {
				t.Fatal(err)
			}
			sc := state.Apps[g.Package].Public
			if sc.State != Active || sc.Binding.Port != c.newPort || EntryPath(sc.Binding.Entry) != c.after || sc.Authority != scope.Authority || len(state.EntryActivity) != 1 {
				t.Fatalf("migration: %+v", state)
			}
		})
	}
}

func TestDisplayNameFallsBackWithoutInfo(t *testing.T) {
	d := Discovery{PackagesDir: t.TempDir()}
	for pkg, want := range map[string]string{"SynoPkgNavidrome": "Navidrome", "SynoPkg": "SynoPkg", "ExampleMusic": "ExampleMusic"} {
		if got := d.displayName(pkg); got != want {
			t.Errorf("%s: got %q, want %q", pkg, got, want)
		}
	}
}
