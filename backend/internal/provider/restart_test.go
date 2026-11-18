package provider

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"dsmapp/internal/connector"
)

type restartTSDriver struct {
	session *appSession
	starts  int
}

func (d *restartTSDriver) Open(context.Context) (TailscaleSession, error) {
	d.starts++
	return d.session, nil
}

// Fresh provider owners model replacement binaries with unchanged appdata.
// Daemon launch/configuration and login reuse are checked separately from
// App approval recovery, which uses real TLS/pin sockets in appaccess tests.
func TestPrivateAndIPv6StopStartAndUpgradeReuseDurableIntent(t *testing.T) {
	ctx := context.Background()
	for _, way := range []string{"tailscale", "netbird", "ipv6"} {
		t.Run(way, func(t *testing.T) {
			dir := t.TempDir()
			var old Runtime
			app := PortMapping{Protocol: "unix", LocalPort: 28443, PublicPort: 28443, Socket: filepath.Join(dir, "app.sock")}
			for generation := 0; generation < 3; generation++ {
				if old != nil {
					if err := old.Stop(); err != nil {
						t.Fatal(err)
					}
				}
				switch way {
				case "tailscale":
					driver := &restartTSDriver{session: &appSession{tsSession: tsSession{snapshot: TailscaleSnapshot{Version: "1.104.1", BackendState: "Running", IPs: []string{"100.64.0.1"}, DNSName: "nas.example.ts.net.", CertDomains: []string{"nas.example.ts.net"}, Serving: true}}}}
					p := NewTailscale(dir, driver)
					p.AppMappings = func(context.Context) ([]PortMapping, error) { return []PortMapping{app}, nil }
					if generation == 0 {
						if _, err := p.Start(ctx, Settings{}); err != nil {
							t.Fatal(err)
						}
					} else if err := p.Restore(ctx); err != nil {
						t.Fatal(err)
					}
					s := p.Status()
					if s.State != "connected" || driver.starts != 1 || driver.session.loginKey != "" || !reflect.DeepEqual(driver.session.seen, []PortMapping{app}) {
						t.Fatalf("lost identity/Serve routes: %+v %+v", s, driver.session.seen)
					}
					old = p
				case "netbird":
					driver := &nbDriver{session: &nbSession{snapshot: NetBirdSnapshot{DaemonStatus: "Connected", ManagementConnected: true, SignalConnected: true, IP: "100.88.0.2/16"}}}
					p := NewNetBird(dir, driver)
					if generation == 0 {
						if _, err := p.Start(ctx, nbSettings()); err != nil {
							t.Fatal(err)
						}
					} else if err := p.Restore(ctx); err != nil {
						t.Fatal(err)
					}
					if s := p.Status(); s.State != "connected" || driver.opens != 1 || generation > 0 && driver.session.key != "" {
						t.Fatalf("identity not reused: %+v", s)
					}
					old = p
				case "ipv6":
					p := NewIPv6Runtime(dir, runtimeSource())
					if generation == 0 {
						if _, err := p.Start(ctx, runtimeSettings()); err != nil {
							t.Fatal(err)
						}
					} else if err := p.Restore(ctx); err != nil {
						t.Fatal(err)
					}
					if s := p.Status(); s.State != "connected" || len(s.Endpoints) != 1 || s.Endpoints[0].LocalPort != 7443 {
						t.Fatalf("lost IPv6 exposure: %+v", s)
					}
					old = p
				}
			}
			if err := old.Stop(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

// A package stop retains all independent selection gates; a new owner must
// restore every enabled provider, rather than only whichever was used last.
func TestAllSelectionGatesSurvivePackageStopAndUpgrade(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	events := []string{}
	runtimes := func() []Runtime {
		out := []Runtime{}
		for _, id := range []string{"cloudflare", "tailscale", "netbird"} {
			out = append(out, &fixtureRuntime{id: id, events: &events})
		}
		return out
	}
	m := NewMultiSelection(dir, runtimes()...)
	for _, id := range []string{"cloudflare", "tailscale", "netbird"} {
		if _, err := m.Connect(ctx, connector.Request{Provider: id, ConfirmExposure: true}); err != nil {
			t.Fatal(err)
		}
	}
	for generation := 0; generation < 2; generation++ {
		if err := m.Stop(); err != nil {
			t.Fatal(err)
		}
		events = nil
		m = NewMultiSelection(dir, runtimes()...)
		if err := m.Restore(ctx); err != nil {
			t.Fatal(err)
		}
		for _, s := range m.Status().Connections {
			if s.Provider != "ipv6" && s.State != "connected" {
				t.Fatalf("%s not restored: %+v", s.Provider, s)
			}
		}
		if len(events) != 3 {
			t.Fatalf("restored %v", events)
		}
	}
}
