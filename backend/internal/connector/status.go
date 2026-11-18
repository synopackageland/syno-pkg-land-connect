// Package connector is the daemon boundary: the control socket, connection
// status and checks, owned child processes and private files.
package connector

import (
	"github.com/synopackageland/syno-pkg-land-sdk/supervision"
	"time"
)

type PortMapping struct {
	// Socket is resolved by the package-owned App source, never by browser input.
	Socket     string `json:"socket,omitempty"`
	LocalPort  int    `json:"localPort"`
	Protocol   string `json:"protocol"`
	PublicPort int    `json:"publicPort"`
}
type Endpoint struct {
	URL       string `json:"url"`
	Public    bool   `json:"public"`
	LocalPort int    `json:"localPort"`
}

type Request struct {
	Retry                   bool          `json:"retry,omitempty"`
	ReuseName               *NameReuse    `json:"reuseName,omitempty"`
	LoginOrigins            []string      `json:"loginOrigins,omitempty"`
	LoginServer             string        `json:"loginServer,omitempty"`
	LoginOrigin             string        `json:"loginOrigin,omitempty"`
	ManagementURL           string        `json:"managementUrl,omitempty"`
	ConfirmManagementPolicy bool          `json:"confirmManagementPolicy,omitempty"`
	ServiceIDs              []string      `json:"serviceIds,omitempty"`
	Mode                    string        `json:"mode,omitempty"`
	Subdomain               string        `json:"subdomain,omitempty"`
	Provider                string        `json:"provider,omitempty"`
	Mappings                []PortMapping `json:"mappings,omitempty"`
	ConfirmExposure         bool          `json:"confirmExposure,omitempty"`
	// Replace accepts that the running connection drops while it switches:
	// the change reconnects instead of being refused as apply_unavailable.
	Replace  bool   `json:"replace,omitempty"`
	Token    string `json:"token"`
	Hostname string `json:"hostname"`
}
type ApplyStatus struct {
	AddedPorts   []int  `json:"addedPorts,omitempty"`
	RemovedPorts []int  `json:"removedPorts,omitempty"`
	Phase        string `json:"phase"`
	Code         string `json:"code,omitempty"`
}

type TrustedCertificate struct {
	Issuer     string `json:"issuer"`
	Expires    string `json:"notAfter"`
	CertSHA256 string `json:"certSha256,omitempty"`
}

type DSMPortChange struct {
	From int `json:"from"`
	To   int `json:"to"`
}

type Status struct {
	DSMPortChange *DSMPortChange        `json:"dsmPortChange,omitempty"`
	Supervision   *supervision.Snapshot `json:"supervision,omitempty"`
	*NameConflict
	CleanupURL     string              `json:"cleanupUrl,omitempty"`
	RecoveryNotice string              `json:"recoveryNotice,omitempty"`
	CleanupNote    string              `json:"cleanupNote,omitempty"`
	TrustedDSM     *TrustedCertificate `json:"trustedDsm,omitempty"`
	// AddressCreatedAt is local evidence of a newly issued public hostname.
	// It is not refreshed by readiness polls or reconnects of a saved domain.
	AddressCreatedAt time.Time    `json:"-"`
	SuggestedName    string       `json:"suggestedName,omitempty"`
	Apply            *ApplyStatus `json:"apply,omitempty"`
	AttemptID        uint64       `json:"attemptId,omitempty"`
	Phase            string       `json:"phase,omitempty"`
	StartedAt        time.Time    `json:"startedAt,omitempty"`
	Deadline         time.Time    `json:"deadline,omitempty"`
	UserCode         string       `json:"userCode,omitempty"`
	LoginOrigins     []string     `json:"loginOrigins,omitempty"`
	NetworkReady     bool         `json:"networkReady"`
	NetBirdFQDN      string       `json:"netbirdFQDN,omitempty"`
	LoginServer      string       `json:"loginServer,omitempty"`
	LoginOrigin      string       `json:"loginOrigin,omitempty"`
	LoginDeadline    string       `json:"loginDeadline,omitempty"`
	TailscaleIPs     []string     `json:"tailscaleIPs,omitempty"`
	NetBirdIP        string       `json:"netbirdIP,omitempty"`
	ManagementURL    string       `json:"managementUrl,omitempty"`
	ServiceIDs       []string     `json:"serviceIds,omitempty"`
	Connections      []Status     `json:"connections,omitempty"`
	KnownLimitation  string       `json:"knownLimitation,omitempty"`
	Mode             string       `json:"mode,omitempty"`
	Endpoints        []Endpoint   `json:"endpoints,omitempty"`
	LoggedIn         bool         `json:"loggedIn"`
	// KeptIdentity: an off connection still holds its device or peer key, so
	// turning it on resumes the same device and the provider still lists it.
	KeptIdentity     bool          `json:"keptIdentity,omitempty"`
	Tailnet          string        `json:"tailnet,omitempty"`
	MagicDNS         string        `json:"magicDNS,omitempty"`
	LoginURL         string        `json:"loginUrl,omitempty"`
	Mappings         []PortMapping `json:"mappings,omitempty"`
	Generation       uint64        `json:"-"`
	Provider         string        `json:"provider"`
	State            string        `json:"state"`
	Code             string        `json:"code,omitempty"`
	Simulation       bool          `json:"simulation"`
	ShareURL         string        `json:"shareUrl"`
	ExternalVerified bool          `json:"externalVerified"`
	ShareAcceptance  string        `json:"shareAcceptance"`
	Hostname         string        `json:"hostname"`
	PeersOnline      *int          `json:"peersOnline,omitempty"`
}
type Process interface {
	Stop() error
	Alive() bool
}
