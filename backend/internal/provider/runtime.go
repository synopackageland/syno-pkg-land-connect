package provider

import (
	"context"
	"dsmapp/internal/connector"
)

// Runtime is owned by connectd, never by a short-lived CGI. Stop preserves
// restart intent; Disable durably revokes it before stopping owned processes.
// Calls are serialized by the selecting manager.
type Runtime interface {
	ID() string
	Start(context.Context, Settings) (connector.Status, error)
	Restore(context.Context) error
	Stop() error
	Disable() error
	Status() connector.Status
	Schema() []Field
	Endpoints() []Endpoint
	Binaries() []BinaryRequirement
	AcceptMappings([]PortMapping) error
}

type Settings struct {
	ReuseName               *connector.NameReuse `json:"reuseName,omitempty"`
	LoginOrigins            []string             `json:"loginOrigins,omitempty"`
	LoginServer             string               `json:"loginServer,omitempty"`
	LoginOrigin             string               `json:"loginOrigin,omitempty"`
	ManagementURL           string               `json:"managementUrl,omitempty"`
	ConfirmManagementPolicy bool                 `json:"confirmManagementPolicy,omitempty"`
	ServiceIDs              []string             `json:"-"`
	Mode                    string               `json:"mode,omitempty"`
	Subdomain               string               `json:"subdomain,omitempty"`
	Token                   string               `json:"token,omitempty"`
	Hostname                string               `json:"hostname,omitempty"`
	Mappings                []PortMapping        `json:"mappings,omitempty"`
	ConfirmExposure         bool                 `json:"confirmExposure,omitempty"`
}

// PortMapping contains ports only, never a caller-selected host or URL.
// C-PORT can translate its resolved DSM/custom mappings into this boundary.
type PortMapping = connector.PortMapping
type Field struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Required bool   `json:"required"`
	Secret   bool   `json:"secret"`
}
type Endpoint = connector.Endpoint
type BinaryRequirement struct {
	Name        string
	VersionArgs []string
}
type Binary struct {
	Path    string
	Version string
}

// BinaryManager resolves verified, active binaries. Implementations must not
// return an unverified download or a user-supplied executable path.
type BinaryManager interface {
	Resolve(context.Context, string) (Binary, error)
	Version(context.Context, string) (string, error)
}
