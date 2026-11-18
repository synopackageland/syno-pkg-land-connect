package apps

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestPinStoreAdapterAndOrdering(t *testing.T) {
	ctx := context.Background()
	store := Store{Dir: t.TempDir()}
	if err := store.Update(ctx, func(s *State) error { return approve(s, Public) }); err != nil {
		t.Fatal(err)
	}
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	bs := PinBindings(state)
	if len(bs) != 1 {
		t.Fatal(bs)
	}
	b := bs[0]
	cleaned := false
	adapter := StorePinState{Store: store, ProjectionPath: filepath.Join(store.Dir, "spl", "external.json"), Cleanup: func(ctx context.Context, got PinBinding) error {
		if adapterState, e := store.Load(); e != nil || adapterState.Apps[b.Package].Public.State != Paused {
			t.Fatal("cleanup preceded persistence")
		}
		if got.Socket != b.Socket {
			t.Fatal(got)
		}
		cleaned = true
		return nil
	}}
	if !adapter.Active(b) {
		t.Fatal("active binding refused")
	}
	changed := b
	changed.SPKI = strings.Repeat("c", 64)
	if adapter.Active(changed) {
		t.Fatal("different binding accepted")
	}
	if err = adapter.Suspend(ctx, changed, "stale", false); err != nil {
		t.Fatal(err)
	}
	if !adapter.Active(b) {
		t.Fatal("stale proxy paused replacement")
	}
	if err = adapter.Suspend(ctx, b, "cert_mismatch", false); err != nil {
		t.Fatal(err)
	}
	if adapter.Active(b) || cleaned {
		t.Fatal("incorrect pre-block ordering")
	}
	if err = adapter.AfterBlock(ctx, b); err != nil {
		t.Fatal(err)
	}
	next, _ := store.Load()
	if !cleaned || next.Apps[b.Package].Public.Cleanup || next.Apps[b.Package].Rev <= state.Apps[b.Package].Rev {
		t.Fatal(next)
	}
}

func TestPinStoreRemovalAndDSMScopes(t *testing.T) {
	ctx := context.Background()
	store := Store{Dir: t.TempDir()}
	if err := store.Update(ctx, func(s *State) error {
		if err := approve(s, Public); err != nil {
			return err
		}
		s.DSM = &DSMState{Port: 5001, Pin: Pin{Mode: "S", SPKI: strings.Repeat("d", 64)}, Origin: "https://dsm.example.com", Public: &Endpoint{Socket: "dsm-public", Authority: "dsm.example.com"}, Private: &Endpoint{Socket: "dsm-private", Authority: "nas.example.com:5001"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	state, _ := store.Load()
	bs := PinBindings(state)
	if len(bs) != 3 {
		t.Fatal(bs)
	}
	adapter := StorePinState{Store: store}
	for _, b := range bs {
		if b.Target == TargetApp {
			if err := adapter.Suspend(ctx, b, "removed", true); err != nil {
				t.Fatal(err)
			}
		}
		if b.Target == TargetDSM && b.Scope == Public {
			if err := adapter.Suspend(ctx, b, "dsm_port_changed", false); err != nil {
				t.Fatal(err)
			}
		}
	}
	next, _ := store.Load()
	if next.Apps["ExampleApp"].Public.Reason != "removed" || next.DSM.Public != nil || next.DSM.Private == nil || next.DSM.Origin != "" {
		t.Fatal(next)
	}
}

func TestFailedPausePersistenceRetriesWithoutReopening(t *testing.T) {
	l := newPinLab(t)
	run := startPin(t, l, l.binding(), "tailscale", nil)
	run.s.err = errors.New("storage failed")
	if err := run.p.Pause(context.Background(), "cert_mismatch", false); err == nil {
		t.Fatal("failed save hidden")
	}
	if run.p.active() {
		t.Fatal("save failure reopened traffic")
	}
	run.s.mu.Lock()
	run.s.err = nil
	run.s.mu.Unlock()
	if err := run.p.Check(context.Background(), PinObservation{}); err != nil {
		t.Fatal(err)
	}
	_, _, n := run.s.snapshot()
	if n != 2 || run.p.active() {
		t.Fatalf("persistence did not retry: %d", n)
	}
}
