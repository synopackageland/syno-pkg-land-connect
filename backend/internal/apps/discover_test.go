package apps

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixturePorts(context.Context) (int, int, error) { return 5000, 5001, nil }
func writeDiscoveryGateway(t *testing.T, root, pkg string, g Gateway) string {
	t.Helper()
	g.Package = pkg
	path := filepath.Join(root, pkg, "target", "sso", "gateway.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(g)
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
	return path
}
func gatewayFixture(pkg, version string, port int) Gateway {
	g := Gateway{Package: pkg, NativeIngress: true, Mode: "header"}
	g.Listen.HTTPS = port
	return g
}

func TestDiscoveryRealSPKFixturesWithoutExternal(t *testing.T) {
	d := Discovery{PackagesDir: t.TempDir(), DetectPorts: fixturePorts, Ping: func(context.Context, string, int) string { return NeedsUpdate }}
	for _, version := range []string{"0.26.0", "0.27.0"} {
		raw, e := os.ReadFile("testdata/gateway-" + version + ".json")
		if e != nil {
			t.Fatal(e)
		}
		var g Gateway
		if e = json.Unmarshal(raw, &g); e != nil {
			t.Fatal(e)
		}
		path := writeDiscoveryGateway(t, d.root(), g.Package, g)
		if e = os.WriteFile(path, raw, 0644); e != nil {
			t.Fatal(e)
		}
	}
	got, e := d.Scan(context.Background())
	if e != nil || len(got) != 2 {
		t.Fatalf("real fixtures rejected: %+v %v", got, e)
	}
	for _, app := range got {
		if !app.NeedsAppUpdate || app.External.Connect != "unavailable" || app.PublicCapable || app.External.OriginChange != "none" || app.Protection.Mode != "oidc" || len(app.Summary) != 64 {
			t.Fatalf("bad legacy defaults: %+v", app)
		}
	}
}

func TestDiscoveryUnsafeAndConflictingMetadata(t *testing.T) {
	for _, test := range []string{"symlink", "group-writable", "other-writable", "oversized", "package-mismatch", "not-native", "dsm-https", "dsm-http", "duplicate", "none", "unknown-external", "detector-unavailable", "fifo"} {
		t.Run(test, func(t *testing.T) {
			d := Discovery{PackagesDir: t.TempDir(), DetectPorts: fixturePorts, Ping: func(context.Context, string, int) string { return NeedsUpdate }}
			g := gatewayFixture("ExampleApp", SDKPing, 28443)
			switch test {
			case "package-mismatch":
				g.Package = "OtherApp"
			case "not-native":
				g.NativeIngress = false
			case "dsm-https":
				g.Listen.HTTPS = 5001
			case "dsm-http":
				g.Listen.HTTPS = 5000
			case "none":
				g.External = &ExternalPolicy{Connect: "none"}
			case "unknown-external":
				g.External = &ExternalPolicy{Connect: "required"}
			case "detector-unavailable":
				d.DetectPorts = func(context.Context) (int, int, error) { return 0, 0, ErrUnavailable }
			}
			path := writeDiscoveryGateway(t, d.root(), "ExampleApp", g)
			switch test {
			case "package-mismatch":
				g.Package = "OtherApp"
				raw, _ := json.Marshal(g)
				_ = os.WriteFile(path, raw, 0644)
			case "symlink":
				_ = os.Rename(path, path+".real")
				_ = os.Symlink(path+".real", path)
			case "group-writable":
				_ = os.Chmod(path, 0664)
			case "other-writable":
				_ = os.Chmod(path, 0646)
			case "oversized":
				_ = os.WriteFile(path, []byte(strings.Repeat(" ", gatewayLimit+1)), 0644)
			case "duplicate":
				writeDiscoveryGateway(t, d.root(), "OtherApp", gatewayFixture("OtherApp", SDKPing, 28443))
			case "fifo":
				_ = os.Remove(path)
				if e := makeFIFO(path); e != nil {
					t.Fatal(e)
				}
			}
			got, e := d.Scan(context.Background())
			if test == "detector-unavailable" && !errors.Is(e, ErrUnavailable) {
				t.Fatal(e)
			}
			if len(got) != 0 {
				t.Fatalf("unsafe App listed: %+v", got)
			}
		})
	}
}

func TestDiscoveryCapabilitiesAndSummary(t *testing.T) {
	for _, status := range []string{Ready, NeedsUpdate, Waiting, "unknown"} {
		for _, external := range []bool{false, true} {
			d := Discovery{PackagesDir: t.TempDir(), DetectPorts: fixturePorts, Ping: func(context.Context, string, int) string { return status }}
			g := gatewayFixture("ExampleApp", "", 28443)
			if external {
				g.External = &ExternalPolicy{Connect: "optional", OriginChange: "none"}
			}
			g.UpstreamAccess = "public"
			g.Passthrough = []string{"/api", "/stream"}
			g.OptionalPassthrough = map[string]OptionalRule{"share": {Prefixes: []string{"/share"}}}
			writeDiscoveryGateway(t, d.root(), "ExampleApp", g)
			got, e := d.Scan(context.Background())
			if e != nil || len(got) != 1 {
				t.Fatal(got, e)
			}
			if got[0].NeedsAppUpdate != (status == NeedsUpdate) || got[0].PublicNeedsAppUpdate == external || (got[0].SettingsURL != "") != external || got[0].PublicCapable != external || (got[0].External.Connect == "optional") != external {
				t.Fatalf("wrong capability gate: %+v", got[0])
			}
			if status == "unknown" && got[0].PingStatus != Waiting {
				t.Fatal("unknown was allowed")
			}
			_, before := ProtectionOf(g)
			g.OptionalPassthrough["share"] = OptionalRule{Prefixes: []string{"/new"}}
			_, after := ProtectionOf(g)
			if before == after {
				t.Fatal("protection change not detected")
			}
		}
	}
}

func TestDiscoverySingleOrigin(t *testing.T) {
	d := Discovery{PackagesDir: t.TempDir(), DetectPorts: fixturePorts, Ping: func(context.Context, string, int) string { return Ready }}
	g := gatewayFixture("ExampleApp", "", 28443)
	g.External = &ExternalPolicy{Connect: "optional", OriginChange: "confirm"}
	path := writeDiscoveryGateway(t, d.root(), "ExampleApp", g)
	raw, _ := os.ReadFile(path)
	// The SDK's own gateway.json spelling.
	raw = []byte(strings.Replace(string(raw), `"origin_change":"confirm"`, `"origin_change":"confirm","single_origin":true`, 1))
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
	got, err := d.Scan(context.Background())
	if err != nil || len(got) != 1 || !got[0].External.SingleOrigin {
		t.Fatalf("%+v %v", got, err)
	}
	out, _ := json.Marshal(got[0])
	if !strings.Contains(string(out), `"external":{"connect":"optional","origin_change":"confirm","single_origin":true}`) {
		t.Fatal(string(out))
	}
}

func TestDiscoveryIconFromUIConfig(t *testing.T) {
	d := Discovery{PackagesDir: t.TempDir(), DetectPorts: fixturePorts, Ping: func(context.Context, string, int) string { return Ready }}
	writeDiscoveryGateway(t, d.root(), "ExampleApp", gatewayFixture("ExampleApp", SDKExt, 28443))
	config := filepath.Join(d.root(), "ExampleApp", "target", "ui", "config")
	if err := os.MkdirAll(filepath.Dir(config), 0755); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ name, config, want string }{
		{"sdk", `{".url":{"ExampleApp":{"type":"url","icon":"images/app_0123456789ab_{0}.png"}}}`, "/webman/3rdparty/ExampleApp/images/app_0123456789ab_64.png"},
		{"only-entry", `{".url":{"SYNO.SDS.ExampleApp.Instance":{"icon":"images/icon_{0}.png"}}}`, "/webman/3rdparty/ExampleApp/images/icon_64.png"},
		{"no-size", `{".url":{"ExampleApp":{"icon":"images/icon.png"}}}`, ""},
		{"outside-images", `{".url":{"ExampleApp":{"icon":"../../etc/{0}.png"}}}`, ""},
		{"absolute", `{".url":{"ExampleApp":{"icon":"https://example.com/images/{0}.png"}}}`, ""},
		{"two-other-entries", `{".url":{"A":{"icon":"images/a_{0}.png"},"B":{"icon":"images/b_{0}.png"}}}`, ""},
		{"not-json", `icon`, ""},
	} {
		if err := os.WriteFile(config, []byte(test.config), 0644); err != nil {
			t.Fatal(err)
		}
		got, err := d.Scan(context.Background())
		if err != nil || len(got) != 1 || got[0].Icon != test.want {
			t.Fatalf("%s: %+v %v", test.name, got, err)
		}
	}
	if err := os.Remove(config); err != nil {
		t.Fatal(err)
	}
	if got, err := d.Scan(context.Background()); err != nil || got[0].Icon != "" {
		t.Fatalf("missing config: %+v %v", got, err)
	}
}
