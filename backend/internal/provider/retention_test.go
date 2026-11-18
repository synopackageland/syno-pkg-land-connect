package provider

import (
	"dsmapp/internal/connector"
	"os"
	"path/filepath"
	"testing"
)

func TestRetainedUninstallPreservesOnlyOwnDomainIntent(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(map[bool]string{true: "enabled", false: "disconnected"}[enabled], func(t *testing.T) {
			dir := t.TempDir()
			cloud := filepath.Join(dir, "cloudflare")
			paths := map[string]string{
				filepath.Join(cloud, "domain/domain.json"):         `{"hostname":"photos.example.com","tunnelId":"11111111-2222-4333-8444-555555555555"}`,
				filepath.Join(cloud, "domain/credentials.json"):    `{"AccountTag":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","TunnelID":"11111111-2222-4333-8444-555555555555","TunnelSecret":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="}`,
				filepath.Join(cloud, "home/.cloudflared/cert.pem"): "SYNTHETIC-RETAINED-AUTHORIZATION",
				filepath.Join(cloud, "settings.json"):              `{"enabled":true,"mode":"domain"}`,
			}
			for path, raw := range paths {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := connector.WritePrivate(path, []byte(raw)); err != nil {
					t.Fatal(err)
				}
			}
			cf := selectionState{Provider: "cloudflare", Enabled: enabled, Settings: &Settings{Mode: "domain", ConfirmExposure: true}}
			child := NewSelection(filepath.Join(dir, "connections/cloudflare"))
			os.MkdirAll(child.dir, 0700)
			if err := child.persist(cf); err != nil {
				t.Fatal(err)
			}
			if err := writeConnectionsMarker(dir); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(filepath.Join(child.dir, "provider.json"))
			if err := RetainOffline(dir); err != nil {
				t.Fatal(err)
			}
			after, _ := os.ReadFile(filepath.Join(child.dir, "provider.json"))
			if string(before) != string(after) {
				t.Fatal("own-domain gate changed")
			}
			for path, raw := range paths {
				got, err := os.ReadFile(path)
				if err != nil || string(got) != raw {
					t.Fatal("retained file changed", path, err)
				}
			}
			for _, id := range []string{"tailscale", "netbird", "ipv6"} {
				gate, err := readGate(filepath.Join(dir, "connections", id, "provider.json"))
				if err != nil || gate.Enabled {
					t.Fatal("other provider left on", id, err)
				}
			}
		})
	}
}
