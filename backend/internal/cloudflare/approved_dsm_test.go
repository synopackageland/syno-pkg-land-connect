package cloudflare

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dsmapp/internal/apps"
	"dsmapp/internal/connector"
)

func TestApprovedDSMTargetMatchesStateAuthorityAndFreshPort(t *testing.T) {
	ctx := context.Background()
	store := apps.Store{Dir: t.TempDir()}
	port := 5001
	resolve := ApprovedDSMTarget(store, func(context.Context) (int, error) { return port, nil })
	if _, e := resolve(ctx, "nas.example.com"); connector.ErrorCode(e) != "pin_proxy_required" {
		t.Fatal(e)
	}
	if e := store.Update(ctx, func(s *apps.State) error {
		s.DSM = &apps.DSMState{Port: 5001, Pin: apps.Pin{Mode: "S", SPKI: strings.Repeat("b", 64)}, Origin: "https://nas.example.com", Public: &apps.Endpoint{Socket: "dsm", Authority: "nas.example.com"}}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if path, e := resolve(ctx, "nas.example.com"); e != nil || path != apps.PinSocket("dsm") {
		t.Fatal(path, e)
	}
	if _, e := resolve(ctx, "other.example.com"); connector.ErrorCode(e) != "pin_proxy_required" {
		t.Fatal("wrong authority", e)
	}
	port = 7443
	if _, e := resolve(ctx, "nas.example.com"); connector.ErrorCode(e) != "pin_proxy_required" {
		t.Fatal("changed DSM port", e)
	}
	if e := os.WriteFile(filepath.Join(store.Dir, "apps-state.json"), []byte("invalid"), 0600); e != nil {
		t.Fatal(e)
	}
	if path, e := resolve(ctx, "nas.example.com"); e == nil || path != "" {
		t.Fatal("corrupt state accepted", e)
	}
}
