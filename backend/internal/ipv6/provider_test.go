package ipv6

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"
)

type sourceFunc func(context.Context) ([]Address, error)

func (f sourceFunc) Addresses(c context.Context) ([]Address, error) { return f(c) }

type portsFunc func(context.Context) ([]Port, error)

func (f portsFunc) Ports(c context.Context) ([]Port, error) { return f(c) }
func addr(ip string) Address                                { return Address{netip.MustParseAddr(ip), "eth0", 64} }
func fixture() Provider {
	return Provider{sourceFunc(func(context.Context) ([]Address, error) { return []Address{addr("2606:4700:1234::1")}, nil }), portsFunc(func(context.Context) ([]Port, error) { return []Port{{5001, "https"}, {5000, "http"}}, nil })}
}

func TestParseRejectsNonPublicAndUnusableAddresses(t *testing.T) {
	ips := []string{"2606:4700:1234::1", "fe80::1", "fd00::1", "::1", "2001:db8::1", "3fff::1", "2001:2::1", "2002::1", "ff02::1"}
	var text strings.Builder
	for _, ip := range ips {
		raw := netip.MustParseAddr(ip).As16()
		fmt.Fprintf(&text, "%x 02 40 00 80 eth0\n", raw)
	}
	for _, flag := range []int{1, 4, 8, 0x20, 0x40} {
		fmt.Fprintf(&text, "26064700123400000000000000000002 02 40 00 %02x eth0\n", flag)
	}
	// mngtmpaddr is NOT temporary; the low-byte proc representation is 00.
	text.WriteString("26064700123400000000000000000003 02 40 00 00 eth0\n")
	got, err := ParseAddresses(strings.NewReader(text.String()))
	if err != nil || len(got) != 2 || got[0].IP.String() != ips[0] || got[0].PrefixBits != 64 {
		t.Fatalf("%v %v", got, err)
	}
}

func TestParseFailsClosedOnMalformedInput(t *testing.T) {
	for _, s := range []string{"bad", "26064700123400000000000000000001 02 81 00 80 eth0", "26064700123400000000000000000001 ZZ 40 00 80 eth0", "26064700123400000000000000000001 02 40 00 ZZ eth0", "26064700123400000000000000000001 02 40 00 80 eth0 extra"} {
		if got, err := ParseAddresses(strings.NewReader(s)); err == nil || len(got) != 0 {
			t.Fatalf("accepted %q: %v %v", s, got, err)
		}
	}
}

func TestNativeSourceFiltersDownInterfacesAndReadErrors(t *testing.T) {
	s := NativeSource{Read: func() ([]byte, error) {
		return []byte("26064700123400000000000000000001 02 40 00 80 eth0\n26064700123400000000000000000002 03 40 00 80 eth1\n"), nil
	}, UpInterfaces: func() (map[string]bool, error) { return map[string]bool{"eth0": true}, nil }}
	got, err := s.Addresses(context.Background())
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
	s.UpInterfaces = func() (map[string]bool, error) { return nil, errors.New("fixture") }
	if _, err = s.Addresses(context.Background()); err == nil {
		t.Fatal("interface errors must fail closed")
	}
}

func TestStatusUsesFreshPortsAndNeverClaimsExternalReachability(t *testing.T) {
	p := fixture()
	s := p.Inspect(context.Background())
	if s.State != "available" || s.Reason != "external_unverified" || len(s.Endpoints) != 2 || s.CheckedAt.IsZero() {
		t.Fatalf("%+v", s)
	}
	if s.Endpoints[1].URL != "https://[2606:4700:1234::1]:5001/" {
		t.Fatalf("%+v", s.Endpoints)
	}
	for _, e := range s.Endpoints {
		if e.Reachability != "not_checked" {
			t.Fatal("local is not external proof")
		}
	}
	p.Ports = portsFunc(func(context.Context) ([]Port, error) { return []Port{{7443, "https"}}, nil })
	s = p.Inspect(context.Background())
	if len(s.Endpoints) != 1 || !strings.Contains(s.Endpoints[0].URL, "]:7443/") {
		t.Fatalf("stale ports: %+v", s)
	}
}

func TestStatusFailureReasonsAndNoDefaultPorts(t *testing.T) {
	for _, tc := range []struct {
		reason string
		modify func(*Provider)
	}{
		{"addresses_unavailable", func(p *Provider) {
			p.Source = sourceFunc(func(context.Context) ([]Address, error) { return nil, errors.New("secret path") })
		}},
		{"no_global_address", func(p *Provider) { p.Source = sourceFunc(func(context.Context) ([]Address, error) { return nil, nil }) }},
		{"ports_unavailable", func(p *Provider) { p.Ports = nil }},
		{"ports_unavailable", func(p *Provider) {
			p.Ports = portsFunc(func(context.Context) ([]Port, error) { return nil, errors.New("fixture") })
		}},
		{"no_ports", func(p *Provider) { p.Ports = portsFunc(func(context.Context) ([]Port, error) { return nil, nil }) }},
		{"invalid_ports", func(p *Provider) {
			p.Ports = portsFunc(func(context.Context) ([]Port, error) { return []Port{{0, "https"}}, nil })
		}},
		{"invalid_ports", func(p *Provider) {
			p.Ports = portsFunc(func(context.Context) ([]Port, error) { return []Port{{65536, "https"}}, nil })
		}},
		{"invalid_ports", func(p *Provider) {
			p.Ports = portsFunc(func(context.Context) ([]Port, error) { return []Port{{443, "javascript"}}, nil })
		}},
	} {
		t.Run(tc.reason, func(t *testing.T) {
			p := fixture()
			tc.modify(&p)
			s := p.Inspect(context.Background())
			if s.State != "unavailable" || s.Reason != tc.reason || len(s.Endpoints) != 0 {
				t.Fatalf("%+v", s)
			}
		})
	}
}

func TestCanonicalSnapshotDeduplicatesAndDoesNotModifySource(t *testing.T) {
	p := fixture()
	input := []Address{addr("2606:4700:1234::2"), addr("2606:4700:1234::1"), addr("2606:4700:1234::1"), addr("fd00::1")}
	p.Source = sourceFunc(func(context.Context) ([]Address, error) { return input, nil })
	p.Ports = portsFunc(func(context.Context) ([]Port, error) { return []Port{{443, "https"}, {443, "https"}, {22, "tcp"}}, nil })
	s := p.Inspect(context.Background())
	if len(s.Addresses) != 2 || len(s.Endpoints) != 4 || input[0].IP.String() != "2606:4700:1234::2" {
		t.Fatalf("%+v", s)
	}
}

func TestWatchTracksPrefixPortsAndFailuresWithoutStaleResults(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	p := fixture()
	reads := 0
	p.Source = sourceFunc(func(context.Context) ([]Address, error) {
		reads++
		switch reads {
		case 1, 2:
			return []Address{addr("2606:4700:1234::1")}, nil
		case 3:
			return []Address{addr("2606:4700:5678::1")}, nil
		default:
			return nil, errors.New("fixture")
		}
	})
	var states []Status
	err := p.Watch(ctx, time.Millisecond, func(s Status) {
		states = append(states, s)
		if len(states) == 3 {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) || len(states) != 3 || states[2].Reason != "addresses_unavailable" || len(states[2].Endpoints) != 0 || states[1].Addresses[0].IP == states[0].Addresses[0].IP {
		t.Fatalf("%+v %v", states, err)
	}
	if p.Watch(context.Background(), 0, func(Status) {}) == nil {
		t.Fatal("zero interval")
	}
}

func TestExternalProbeRequiresConsentAndFreshTargets(t *testing.T) {
	p := fixture()
	calls := 0
	probe := probeFunc(func(_ context.Context, e Endpoint) (string, error) {
		calls++
		if e.Port.Scheme == "http" {
			return "unreachable", nil
		}
		return "tcp_reachable", nil
	})
	if _, err := p.CheckExternal(context.Background(), false, probe); !errors.Is(err, ErrConsentRequired) || calls != 0 {
		t.Fatal("probe ran without consent")
	}
	s, err := p.CheckExternal(context.Background(), true, probe)
	if err != nil || calls != 2 || s.Endpoints[1].Reachability != "tcp_reachable" {
		t.Fatalf("%+v %v", s, err)
	}
	// Probe evidence is never reused by routine status calls.
	if p.Inspect(context.Background()).Endpoints[1].Reachability != "not_checked" {
		t.Fatal("stale proof")
	}
}

type probeFunc func(context.Context, Endpoint) (string, error)

func (f probeFunc) Check(c context.Context, e Endpoint) (string, error) { return f(c, e) }
