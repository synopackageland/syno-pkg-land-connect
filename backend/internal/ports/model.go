// Package ports owns desired local service mappings. It neither opens listeners
// nor runs a provider binary. Provider adapters must reconcile complete plans.
package ports

import "context"

const (
	DSMHTTP   = "dsm-http"
	DSMHTTPS  = "dsm-https"
	MaxCustom = 64
)

// All supported application protocols use TCP as their transport. Port denotes
// an existing local service, not a port for this package to bind or reserve.
type Protocol string

const (
	HTTP  Protocol = "http"
	HTTPS Protocol = "https"
	TCP   Protocol = "tcp"
)

type DSMPorts struct {
	HTTP  int
	HTTPS int
}
type Detector interface {
	Detect(context.Context) (DSMPorts, error)
}

// Mapping is provider-neutral. TargetHost is always loopback for proxy providers;
// IPv6 adapters must separately resolve an approved NAS address and firewall rule.
// No user-supplied hostname, URL, command, or TLS bypass enters this contract.
type Mapping struct {
	ID         string   `json:"id"`
	Protocol   Protocol `json:"protocol"`
	Port       int      `json:"port"`
	Source     string   `json:"source"`
	TargetHost string   `json:"targetHost"`
}

type Plan struct {
	Mappings []Mapping `json:"mappings"`
}

// Applier replaces the entire owned mapping set, including deletion of old
// targets. It must reject unsupported protocols, preserve TLS verification,
// and never append routes or modify resources belonging to another package.
// Revoke removes ALL mappings owned by this contract after a failed Apply.
// Implementations must bound their calls and honor context cancellation.
type Applier interface {
	Apply(context.Context, Plan) error
	Revoke(context.Context) error
}

type Input struct {
	ID              string   `json:"id,omitempty"`
	Name            string   `json:"name"`
	Protocol        Protocol `json:"protocol"`
	Port            int      `json:"port"`
	Enabled         bool     `json:"enabled"`
	ConfirmExposure bool     `json:"confirmExposure"`
}

type Entry struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Protocol Protocol `json:"protocol"`
	Port     *int     `json:"port"`
	Source   string   `json:"source"`
	Enabled  bool     `json:"enabled"`
	State    string   `json:"state"`
}

type Snapshot struct {
	FollowServices bool      `json:"followServices"`
	FollowState    string    `json:"followState"`
	Services       []Service `json:"services"`
	UDPSkipped     bool      `json:"udpSkipped"`
	Entries        []Entry   `json:"entries"`
	Detection      string    `json:"detection"`
	MappingState   string    `json:"mappingState"`
	Revision       uint64    `json:"revision"`
	Plan           Plan      `json:"plan"`
}
