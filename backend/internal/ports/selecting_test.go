package ports

import (
	"context"
	"dsmapp/internal/connector"
	"dsmapp/internal/provider"
	"errors"
	"testing"
	"time"
)

func TestSelectingApplierTracksIPv6AndResetsAcrossProviderEpoch(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store := Store{Dir: dir}
	detector := goodDetector()
	store.SetDSM(ctx, detector, DSMHTTPS, true, true)
	v6 := provider.NewIPv6Runtime(dir, ipv6Addresses{})
	v6.CheckLocal = func(context.Context, []provider.PortMapping) error { return nil }
	selection := provider.NewSelection(dir, v6)
	source := ResolvedSource{Store: store, Detector: detector}
	selection.SetPortSource(source)
	r := Reconciler{Store: store, Detector: detector, Applier: SelectingApplier{Selection: selection}, Debounce: time.Millisecond}
	now := time.Now()
	view, e := r.Step(ctx, now)
	if !errors.Is(e, ErrProviderPending) || view.MappingState != "pending-provider" {
		t.Fatal(view, e)
	}
	if _, e = selection.Connect(ctx, connector.Request{Provider: "ipv6", ConfirmExposure: true}); e != nil {
		t.Fatal(e)
	}
	r.Step(ctx, now)
	view, e = r.Step(ctx, now.Add(time.Second))
	if e != nil || view.MappingState != "applied" || v6.Endpoints()[0].LocalPort != 5001 {
		t.Fatal(view, e, v6.Endpoints())
	}
	moved := detectorFunc(func(context.Context) (DSMPorts, error) { return DSMPorts{6000, 6001}, nil })
	source.Detector = moved
	selection.SetPortSource(source)
	r.Detector = moved
	r.Step(ctx, now.Add(2*time.Second))
	if len(v6.Endpoints()) != 0 {
		t.Fatal("stale endpoint during debounce")
	}
	r.Step(ctx, now.Add(3*time.Second))
	if v6.Endpoints()[0].LocalPort != 6001 {
		t.Fatal(v6.Endpoints())
	}
	if _, e = selection.Disconnect(); e != nil {
		t.Fatal(e)
	}
	view, e = r.Step(ctx, now.Add(4*time.Second))
	if !errors.Is(e, ErrProviderPending) || view.MappingState != "pending-provider" {
		t.Fatal("unchanged plan hid disconnect", view, e)
	}
}
