// Package ipv6 provides read-only IPv6 direct-connection diagnostics. Local
// address availability never implies Internet reachability or TLS validity.
package ipv6

import (
	"context"
	"net/netip"
	"time"
)

type Address struct {
	IP         netip.Addr `json:"ip"`
	Interface  string     `json:"interface"`
	PrefixBits int        `json:"prefixBits"`
}

// Source must return only usable, non-temporary global unicast addresses.
// NativeSource additionally verifies the interface is up.
type Source interface {
	Addresses(context.Context) ([]Address, error)
}

// PortSource is the adapter seam for C-PORT. No hardcoded DSM ports are used.
// Scheme is http, https or tcp; TCP endpoints are displayed as host:port.
type Port struct {
	Number int    `json:"number"`
	Scheme string `json:"scheme"`
}
type PortSource interface {
	Ports(context.Context) ([]Port, error)
}

type Endpoint struct {
	Address      Address `json:"address"`
	Port         Port    `json:"port"`
	URL          string  `json:"url"`
	Reachability string  `json:"reachability"`
}

type Status struct {
	State     string     `json:"state"`
	Reason    string     `json:"reason"`
	Addresses []Address  `json:"addresses"`
	Endpoints []Endpoint `json:"endpoints"`
	CheckedAt time.Time  `json:"checkedAt"`
}

// Provider has no enable/disable operation: it cannot open a firewall, start
// a listener, update DNS or change DSM configuration.
type Provider struct {
	Source Source
	Ports  PortSource
}
