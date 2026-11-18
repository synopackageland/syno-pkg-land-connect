package ports

import (
	"context"
	"net/netip"
	"reflect"
	"testing"

	"dsmapp/internal/ipv6"
	"dsmapp/internal/provider"
)

type ipv6Addresses struct{}

func (ipv6Addresses) Addresses(context.Context) ([]ipv6.Address, error) {
	return []ipv6.Address{{IP: netip.MustParseAddr("2606:4700:1234::1"), Interface: "eth0", PrefixBits: 64}}, nil
}
func TestIPv6ResolvedSourceUsesIdenticalPublicAndLocalPortsAndFollowsDSM(t *testing.T) {
	ctx := context.Background()
	store := Store{Dir: t.TempDir()}
	store.SetDSM(ctx, goodDetector(), DSMHTTPS, true, true)
	store.Upsert(ctx, goodDetector(), Input{Name: "web", Protocol: HTTP, Port: 80, Enabled: true, ConfirmExposure: true})
	source := ResolvedSource{Store: store, Detector: goodDetector()}
	var _ ipv6.PortSource = source
	got, e := source.RuntimeMappings(ctx)
	want := []provider.PortMapping{{LocalPort: 5001, PublicPort: 5001, Protocol: "https"}, {LocalPort: 80, PublicPort: 80, Protocol: "http"}}
	if e != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, e)
	}
	runtime := provider.NewIPv6Runtime(t.TempDir(), ipv6Addresses{})
	if e := runtime.AcceptMappings(got); e != nil {
		t.Fatal(e)
	}
	p := ipv6.Provider{Source: ipv6Addresses{}, Ports: source}
	snapshot := p.Inspect(ctx)
	if snapshot.State != "available" || !hasEndpointURL(snapshot, "https://[2606:4700:1234::1]:5001/") {
		t.Fatal(snapshot)
	}
	source.Detector = detectorFunc(func(context.Context) (DSMPorts, error) { return DSMPorts{6000, 6001}, nil })
	got, e = source.RuntimeMappings(ctx)
	if e != nil || got[0].LocalPort != 6001 || got[0].PublicPort != 6001 {
		t.Fatal("not direct", got, e)
	}
	p.Ports = source
	snapshot = p.Inspect(ctx)
	if !hasEndpointURL(snapshot, "https://[2606:4700:1234::1]:6001/") {
		t.Fatal(snapshot)
	}
	source.Detector = detectorFunc(func(context.Context) (DSMPorts, error) { return DSMPorts{}, ErrDetection })
	got, e = source.RuntimeMappings(ctx)
	if e == nil || len(got) != 0 {
		t.Fatal("stale DSM mapping", got, e)
	}
	p.Ports = source
	snapshot = p.Inspect(ctx)
	if snapshot.Reason != "ports_unavailable" || len(snapshot.Endpoints) != 0 {
		t.Fatal(snapshot)
	}
}
func TestIPv6SourceOmitsDisabledAndConflictingEntries(t *testing.T) {
	ctx := context.Background()
	s := Store{Dir: t.TempDir()}
	s.Upsert(ctx, goodDetector(), Input{Name: "web", Protocol: HTTP, Port: 80, Enabled: true, ConfirmExposure: true})
	moved := detectorFunc(func(context.Context) (DSMPorts, error) { return DSMPorts{80, 5001}, nil })
	source := ResolvedSource{Store: s, Detector: moved}
	got, e := source.Ports(ctx)
	if e != nil || len(got) != 0 {
		t.Fatal("conflict/disabled entry became direct endpoint", got, e)
	}
}

func hasEndpointURL(s ipv6.Status, url string) bool {
	for _, e := range s.Endpoints {
		if e.URL == url {
			return true
		}
	}
	return false
}
