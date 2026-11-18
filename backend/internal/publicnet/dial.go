// Package publicnet reaches public provider hostnames without the NAS resolver's
// negative cache. It never substitutes the local service for the public edge.
package publicnet

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"time"
)

// Dialer resolves through Cloudflare, then Google. Dial and the DoH endpoints
// are injectable for isolated tests; production uses the fixed bootstrap IPs.
type Dialer struct {
	// Lookup is the public DNS fallback when HTTPS DNS cannot be reached.
	Lookup    func(context.Context, string) ([]netip.Addr, error)
	Client    *http.Client
	Endpoints []string
	Dial      func(context.Context, string, string) (net.Conn, error)
}

var dohClient = &http.Client{
	Timeout: 2 * time.Second,
	Transport: &http.Transport{DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		ip, ok := map[string]string{"cloudflare-dns.com": "1.1.1.1", "dns.google": "8.8.8.8"}[host]
		if !ok {
			return nil, errors.New("unknown DoH endpoint")
		}
		return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, net.JoinHostPort(ip, port))
	}, TLSHandshakeTimeout: time.Second},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// Transport preserves the request hostname for Host, SNI and TLS verification.
// A proxy could resolve the hostname using its own stale cache, so checks go
// directly to the public edge. Existing explicit test transports stay separate.
func (d Dialer) Transport() *http.Transport {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.DialContext = d.DialContext
	return tr
}

func (d Dialer) query(ctx context.Context, endpoint, host, kind string) ([]netip.Addr, bool) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, false
	}
	q := u.Query()
	q.Set("name", host)
	q.Set("type", kind)
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, false
	}
	req.Header.Set("Accept", "application/dns-json")
	req.Header.Set("Cache-Control", "no-cache, no-store")
	client := d.Client
	if client == nil {
		client = dohClient
	}
	// Bound injected clients too; neither DNS family can hold the probe forever.
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	res, err := client.Do(req.WithContext(bounded))
	if err != nil {
		return nil, false
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, false
	}
	var reply struct {
		Status *int
		TC     bool
		Answer []struct {
			Type int
			Data string
		}
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 65537))
	if err != nil || len(raw) > 65536 || json.Unmarshal(raw, &reply) != nil || reply.Status == nil || reply.TC {
		return nil, false
	}
	if *reply.Status != 0 {
		return nil, true
	}
	ips := []netip.Addr{}
	for _, a := range reply.Answer {
		ip, err := netip.ParseAddr(a.Data)
		if err == nil && ((kind == "A" && a.Type == 1 && ip.Is4()) || (kind == "AAAA" && a.Type == 28 && ip.Is6())) && ip.IsGlobalUnicast() && !ip.IsPrivate() {
			ips = append(ips, ip)
		}
	}
	return ips, true
}

func (d Dialer) lookup(ctx context.Context, host string) ([]netip.Addr, error, bool) {
	endpoints := d.Endpoints
	if len(endpoints) == 0 {
		endpoints = []string{"https://cloudflare-dns.com/dns-query", "https://dns.google/resolve"}
	}
	answered := false
	for _, endpoint := range endpoints {
		type result struct {
			ips      []netip.Addr
			answered bool
		}
		a := make(chan result, 1)
		aaaa := make(chan result, 1)
		for kind, ch := range map[string]chan result{"A": a, "AAAA": aaaa} {
			go func() { ips, ok := d.query(ctx, endpoint, host, kind); ch <- result{ips, ok} }()
		}
		v4, v6 := <-a, <-aaaa
		answered = answered || v4.answered || v6.answered
		ips := append(v4.ips, v6.ips...)
		if ctx.Err() != nil {
			return nil, ctx.Err(), true
		}
		if len(ips) > 0 {
			return ips, nil, true
		}
	}
	return nil, &net.DNSError{Name: host, Err: "public DNS has no usable address", IsNotFound: true}, answered
}

func (d Dialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dial := d.Dial
	if dial == nil {
		dial = (&net.Dialer{Timeout: 2 * time.Second}).DialContext
	}
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if net.ParseIP(host) != nil {
		return dial(ctx, network, address)
	}
	ips, err, answered := d.lookup(ctx, strings.TrimSuffix(host, "."))
	if !answered {
		lookup := d.Lookup
		if lookup == nil {
			lookup = publicLookup
		}
		ips, err = lookup(ctx, host)
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var dns *net.DNSError
		if err != nil {
			if errors.As(err, &dns) && dns.IsNotFound {
				return nil, err
			}
			return dial(ctx, network, address)
		}
	}
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		c, e := dial(ctx, network, net.JoinHostPort(ip.String(), port))
		if e == nil {
			return c, nil
		}
		err = e
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, err
}

// Some networks block DoH but allow public DNS. Use a fresh Go resolver with
// an explicit public server before consulting the NAS's cached resolver. The
// resolver handles A, AAAA, CNAME and truncated UDP replies (TCP retry).
func publicLookup(ctx context.Context, host string) ([]netip.Addr, error) {
	var last error
	for _, server := range []string{"1.1.1.1:53", "8.8.8.8:53"} {
		resolver := &net.Resolver{PreferGo: true, StrictErrors: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{Timeout: time.Second}).DialContext(ctx, network, server)
		}}
		bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
		ips, err := resolver.LookupNetIP(bounded, "ip", host)
		cancel()
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if err == nil {
			public := []netip.Addr{}
			for _, ip := range ips {
				if ip.IsGlobalUnicast() && !ip.IsPrivate() {
					public = append(public, ip)
				}
			}
			if len(public) > 0 {
				return public, nil
			}
			return nil, &net.DNSError{Name: host, Err: "public DNS has no usable address", IsNotFound: true}
		}
		var dns *net.DNSError
		if errors.As(err, &dns) && dns.IsNotFound {
			return nil, err
		}
		last = err
	}
	return nil, last
}
