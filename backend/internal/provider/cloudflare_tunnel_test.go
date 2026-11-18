package provider

import (
	"context"
	"dsmapp/internal/apps"
	"os"
	"path/filepath"
	"testing"

	"dsmapp/internal/cloudflare"
	"dsmapp/internal/connector"
)

type idleProcess struct{}

func (idleProcess) Stop() error { return nil }
func (idleProcess) Alive() bool { return true }
func (idleProcess) Ready() bool { return false }

type idleLauncher struct{ started int }

func (l *idleLauncher) Start(context.Context, cloudflare.Command) (connector.Process, error) {
	l.started++
	return idleProcess{}, nil
}
func (*idleLauncher) Run(context.Context, cloudflare.Command) error { return nil }

func TestCloudflareTunnelStartsTheRequestedModeOnly(t *testing.T) {
	launcher := &idleLauncher{}
	var p Runtime = &CloudflareTunnel{Tunnel: cloudflare.NewTunnel(cloudflare.Config{DSMTarget: func(context.Context, string) (string, error) { return apps.PinSocket("dsm"), nil },
		Dir:         t.TempDir(),
		Cloudflared: func(context.Context) (string, error) { return "/opt/cloudflared", nil },
		DSMPort:     func(context.Context) (int, error) { return 5000, nil },
		Launcher:    launcher,
		MetricsAddr: func() (string, error) { return "127.0.0.1:1", nil },
	})}
	defer p.Stop()
	if p.ID() != "cloudflare" || p.Binaries()[0].Name != "cloudflared" {
		t.Fatal("metadata")
	}
	if p.AcceptMappings([]PortMapping{{LocalPort: 5001, Protocol: "https", PublicPort: 443}}) == nil {
		t.Fatal("accepted port mappings")
	}
	for _, s := range []Settings{{Mode: "sealed"}, {Mode: "quick", Mappings: []PortMapping{{LocalPort: 1}}}} {
		if _, e := p.Start(context.Background(), s); e == nil {
			t.Errorf("accepted %+v", s)
		}
	}
	if launcher.started != 0 {
		t.Fatal("started an invalid request")
	}
	s, e := p.Start(context.Background(), Settings{Mode: "quick", ConfirmExposure: true})
	if e != nil || s.Mode != "quick" || s.State != "connecting" || launcher.started != 1 {
		t.Fatalf("status %+v, %v", s, e)
	}
}

func TestDomainReconnectKeepsAccountAuthorizationUntilExplicitDisconnect(t *testing.T) {
	for _, mode := range []string{"domain", "quick", "token", ""} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(`{"mode":"`+mode+`"}`), 0600); err != nil {
			t.Fatal(err)
		}
		p := &CloudflareTunnel{Tunnel: cloudflare.NewTunnel(cloudflare.Config{Dir: dir})}
		if p.PreserveOnReconnect(Settings{Mode: "domain", Subdomain: "photos"}) != (mode != "token") {
			t.Fatal("domain replacement discarded authorization", mode)
		}
		if p.PreserveOnReconnect(Settings{Mode: "quick"}) {
			t.Fatal("temporary connection kept authorization")
		}
	}
}
