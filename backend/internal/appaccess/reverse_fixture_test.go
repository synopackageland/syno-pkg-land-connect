package appaccess

import (
	"context"
	"os"
	"testing"
	"time"

	"dsmapp/internal/apps"
	"dsmapp/internal/connector"
	"dsmapp/internal/ingress"
)

// TestReverseBoundaryFixture is the real Connect approval/proxy/peer assembly.
// The container harness supplies an outbound-only test edge instead of Cloudflare.
func TestReverseBoundaryFixture(t *testing.T) {
	if os.Getenv("CONNECT_REVERSE_FIXTURE") != "1" {
		t.Skip("isolated cross-package fixture")
	}
	f := newFixture(t, false)
	f.owner.cfg.IngressConnector = func() connector.Status {
		state := "disconnected"
		if _, err := os.Stat("/fixture/connector-up"); err == nil {
			state = "connected"
		}
		return connector.Status{State: state}
	}
	s, err := ingress.Start(context.Background(), ingress.Socket, f.service, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	os.WriteFile("/fixture/connect-ready", []byte("ready"), 0644)
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		if _, err := os.Stat("/fixture/done"); err == nil {
			return
		}
		state, err := f.store.Load()
		if err != nil {
			t.Fatal(err)
		}
		for id, def := range state.Custom {
			if def.Ingress == nil {
				continue
			}
			if _, err := os.Stat("/fixture/revoke"); err == nil {
				_, err := f.owner.RevokeIngress(context.Background(), apps.IngressSubject{Package: def.Ingress.Package, UID: def.Ingress.UID}, apps.IngressRef{ID: id})
				if err != nil {
					t.Fatal(err)
				}
				os.WriteFile("/fixture/revoked", []byte("done"), 0644)
			} else if state.Apps[id].Public == nil {
				if _, err := os.Stat("/fixture/approve"); err != nil {
					continue
				}
				approveCustom(t, f, id, "")
				current, _ := f.store.Load()
				os.WriteFile("/fixture/proxy", []byte(apps.PinSocket(current.Apps[id].Public.Socket)), 0644)
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("cross-package fixture timeout")
}
