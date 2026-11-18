package ipv6

import (
	"context"
	"errors"
	"net"
	"reflect"
	"sort"
	"strconv"
	"time"
)

func (Provider) ID() string { return "ipv6" }

func (p Provider) Inspect(ctx context.Context) Status {
	s := Status{State: "unavailable", Addresses: []Address{}, Endpoints: []Endpoint{}, CheckedAt: time.Now().UTC()}
	if ctx.Err() != nil {
		s.Reason = "cancelled"
		return s
	}
	source := p.Source
	if source == nil {
		source = NativeSource{}
	}
	addresses, err := source.Addresses(ctx)
	if err != nil {
		s.Reason = "addresses_unavailable"
		return s
	}
	seen := map[Address]bool{}
	for _, a := range addresses {
		if publicAddress(a.IP) && a.PrefixBits >= 0 && a.PrefixBits <= 128 && a.Interface != "" && !seen[a] {
			s.Addresses = append(s.Addresses, a)
			seen[a] = true
		}
	}
	sort.Slice(s.Addresses, func(i, j int) bool {
		a, b := s.Addresses[i], s.Addresses[j]
		if a.IP != b.IP {
			return a.IP.Less(b.IP)
		}
		if a.Interface != b.Interface {
			return a.Interface < b.Interface
		}
		return a.PrefixBits < b.PrefixBits
	})
	if len(s.Addresses) == 0 {
		s.Reason = "no_global_address"
		return s
	}
	if p.Ports == nil {
		s.Reason = "ports_unavailable"
		return s
	}
	ports, err := p.Ports.Ports(ctx)
	if err != nil {
		s.Reason = "ports_unavailable"
		return s
	}
	if len(ports) == 0 {
		s.Reason = "no_ports"
		return s
	}
	unique := map[Port]bool{}
	valid := []Port{}
	for _, port := range ports {
		if port.Number < 1 || port.Number > 65535 || (port.Scheme != "https" && port.Scheme != "http" && port.Scheme != "tcp") {
			s.Reason = "invalid_ports"
			return s
		}
		if !unique[port] {
			valid = append(valid, port)
			unique[port] = true
		}
	}
	sort.Slice(valid, func(i, j int) bool {
		if valid[i].Number != valid[j].Number {
			return valid[i].Number < valid[j].Number
		}
		return valid[i].Scheme < valid[j].Scheme
	})
	for _, a := range s.Addresses {
		for _, port := range valid {
			u := net.JoinHostPort(a.IP.String(), strconv.Itoa(port.Number))
			if port.Scheme != "tcp" {
				u = port.Scheme + "://" + u + "/"
			}
			s.Endpoints = append(s.Endpoints, Endpoint{a, port, u, "not_checked"})
		}
	}
	if ctx.Err() != nil {
		s.Endpoints = []Endpoint{}
		s.Reason = "cancelled"
		return s
	}
	s.State = "available"
	s.Reason = "external_unverified"
	return s
}

// Watch polls read-only inventories and emits changed snapshots synchronously.
// Prefix loss, read errors and port changes remove old endpoints immediately on
// the next poll. Cancellation owns the ticker; no goroutine outlives this call.
func (p Provider) Watch(ctx context.Context, interval time.Duration, emit func(Status)) error {
	if interval <= 0 || emit == nil {
		return errors.New("invalid IPv6 watch options")
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var previous *Status
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		s := p.Inspect(ctx)
		compare := s
		compare.CheckedAt = time.Time{}
		if previous == nil || !reflect.DeepEqual(*previous, compare) {
			// Keep our own snapshot: callbacks may retain or modify their value.
			copy := compare
			copy.Addresses = append([]Address{}, compare.Addresses...)
			copy.Endpoints = append([]Endpoint{}, compare.Endpoints...)
			previous = &copy
			emit(s)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

var ErrConsentRequired = errors.New("external IPv6 probe requires explicit consent")

// Probe is a trusted adapter, never a browser-provided URL. This package ships
// no external service client. A TCP result proves neither TLS nor HTTP/auth.
type Probe interface {
	Check(context.Context, Endpoint) (string, error)
}

func (p Provider) CheckExternal(ctx context.Context, consent bool, probe Probe) (Status, error) {
	if !consent {
		return Status{}, ErrConsentRequired
	}
	if probe == nil {
		return Status{}, errors.New("external IPv6 probe unavailable")
	}
	s := p.Inspect(ctx)
	if s.State != "available" {
		return s, nil
	}
	for i, e := range s.Endpoints {
		if err := ctx.Err(); err != nil {
			return Status{}, err
		}
		result, err := probe.Check(ctx, e)
		if err != nil || (result != "tcp_reachable" && result != "unreachable") {
			result = "unknown"
		}
		s.Endpoints[i].Reachability = result
	}
	return s, ctx.Err()
}
