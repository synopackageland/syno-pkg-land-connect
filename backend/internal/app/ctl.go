package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"time"

	"dsmapp/internal/advisor"
	"dsmapp/internal/connector"
	"dsmapp/internal/ipv6"
	"dsmapp/internal/ports"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

type controlPortEntry struct {
	ID       string         `json:"id"`
	Protocol ports.Protocol `json:"protocol"`
	Port     *int           `json:"port"`
	Source   string         `json:"source"`
	Enabled  bool           `json:"enabled"`
	State    string         `json:"state"`
}

type controlPortsView struct {
	Entries      []controlPortEntry `json:"entries"`
	Detection    string             `json:"detection"`
	MappingState string             `json:"mappingState"`
	Revision     uint64             `json:"revision"`
}

func (cfg Config) controlPortsList(ctx context.Context, caller api.Caller, params json.RawMessage) (any, error) {
	if err := decodeParams(params, &struct{}{}); err != nil {
		return nil, err
	}
	s, err := (ports.Store{Dir: cfg.DataDir}).List(ctx, cfg.portDetector())
	if err != nil {
		return nil, portError(err)
	}
	return controlPorts(s), nil
}

func controlPorts(s ports.Snapshot) controlPortsView {
	v := controlPortsView{Entries: []controlPortEntry{}, Detection: s.Detection, MappingState: s.MappingState, Revision: s.Revision}
	for _, entry := range s.Entries {
		// Names are arbitrary user text and can contain private URLs or secrets.
		v.Entries = append(v.Entries, controlPortEntry{ID: entry.ID, Protocol: entry.Protocol, Port: entry.Port, Source: entry.Source, Enabled: entry.Enabled, State: entry.State})
	}
	return v
}

type controlIPv6Endpoint struct {
	Port         ipv6.Port `json:"port"`
	Reachability string    `json:"reachability"`
}

type controlIPv6View struct {
	State        string                `json:"state"`
	Reason       string                `json:"reason"`
	AddressCount int                   `json:"addressCount"`
	Endpoints    []controlIPv6Endpoint `json:"endpoints"`
	CheckedAt    time.Time             `json:"checkedAt"`
}

func (cfg Config) controlIPv6Status(ctx context.Context, caller api.Caller, params json.RawMessage) (any, error) {
	value, err := cfg.ipv6Status(ctx, caller, params)
	if err != nil {
		return nil, err
	}
	s, ok := value.(ipv6.Status)
	if !ok {
		return nil, &api.Error{Code: "ipv6_status_unavailable", Message: "IPv6 status is unavailable"}
	}
	v := controlIPv6View{State: s.State, Reason: s.Reason, AddressCount: len(s.Addresses), Endpoints: []controlIPv6Endpoint{}, CheckedAt: s.CheckedAt}
	for _, endpoint := range s.Endpoints {
		// Keep diagnostic state, never a NAS address or constructed private URL.
		v.Endpoints = append(v.Endpoints, controlIPv6Endpoint{Port: endpoint.Port, Reachability: endpoint.Reachability})
	}
	return v, nil
}

// Control output is an allowlist. Never serialize a complete daemon status to
// ctl. A sign-in link is handed out only while that sign-in is pending: the
// administrator who called ctl opens it, and the CLI does not log results.
type controlConnectionView struct {
	Apply            *connector.ApplyStatus  `json:"apply,omitempty"`
	Provider         string                  `json:"provider"`
	State            string                  `json:"state"`
	Phase            string                  `json:"phase,omitempty"`
	Code             string                  `json:"code,omitempty"`
	Mode             string                  `json:"mode,omitempty"`
	KnownLimitation  string                  `json:"knownLimitation,omitempty"`
	Simulation       bool                    `json:"simulation"`
	LoggedIn         bool                    `json:"loggedIn"`
	NetworkReady     bool                    `json:"networkReady"`
	ExternalVerified bool                    `json:"externalVerified"`
	LoginURL         string                  `json:"loginUrl,omitempty"`
	UserCode         string                  `json:"userCode,omitempty"`
	LoginDeadline    string                  `json:"loginDeadline,omitempty"`
	Connections      []controlConnectionView `json:"connections,omitempty"`
}

func controlConnection(s connector.Status) controlConnectionView {
	v := controlConnectionView{Apply: s.Apply, Provider: s.Provider, State: s.State, Phase: s.Phase, Code: s.Code, Mode: s.Mode,
		KnownLimitation: s.KnownLimitation, Simulation: s.Simulation, LoggedIn: s.LoggedIn,
		NetworkReady: s.NetworkReady, ExternalVerified: s.ExternalVerified}
	if s.State == "needs-login" && trustedLoginURL(s.LoginURL) {
		v.LoginURL, v.UserCode, v.LoginDeadline = s.LoginURL, s.UserCode, s.LoginDeadline
	}
	for _, child := range s.Connections {
		v.Connections = append(v.Connections, controlConnection(child))
	}
	return v
}

func trustedLoginURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host != "" && u.User == nil
}

func controlResult(value any, err error) (any, error) {
	if err != nil {
		return nil, err
	}
	switch s := value.(type) {
	case connector.Status:
		return controlConnection(s), nil
	case connectionView:
		return controlConnectionView{Provider: s.Provider, State: s.State, Simulation: s.Simulation}, nil
	default:
		return nil, &api.Error{Code: "connection_status_unavailable", Message: "connection status is unavailable"}
	}
}

func (cfg Config) controlConnectionStatus(ctx context.Context, caller api.Caller, params json.RawMessage) (any, error) {
	return controlResult(cfg.connectionStatus(ctx, caller, params))
}

func (cfg Config) controlConnectionDisconnect(ctx context.Context, caller api.Caller, params json.RawMessage) (any, error) {
	var in struct {
		Provider string `json:"provider"`
	}
	if err := decodeParams(params, &in); err != nil {
		return nil, err
	}
	if in.Provider == "all" {
		in.Provider = ""
	}
	forward, err := json.Marshal(in)
	if err != nil {
		return nil, api.BadRequest("invalid provider")
	}
	// Reuse the UI's lock, daemon and validation. A stopping response stays
	// stopping; callers must read status to confirm completion, never replay.
	return controlResult(cfg.lockedConnectionDisconnect(ctx, caller, forward))
}

// Port mutations use the same validation, store and authority as the UI, but
// return the control allowlist instead of arbitrary names or internal plans.
func (cfg Config) controlPortMutation(handler func(context.Context, api.Caller, json.RawMessage) (any, error)) func(context.Context, api.Caller, json.RawMessage) (any, error) {
	return func(ctx context.Context, caller api.Caller, params json.RawMessage) (any, error) {
		value, err := handler(ctx, caller, params)
		if err != nil {
			return nil, err
		}
		snapshot, ok := value.(ports.Snapshot)
		if !ok {
			return nil, &api.Error{Code: "port_settings_unavailable", Message: "port settings are unavailable"}
		}
		return controlPorts(snapshot), nil
	}
}

func (cfg Config) controlConnectionConnect(ctx context.Context, caller api.Caller, params json.RawMessage) (any, error) {
	var in connector.Request
	if err := decodeParams(params, &in); err != nil {
		return nil, err
	}
	// The UI's legacy simulation is not a real connection task.
	if !isRuntimeProvider(in.Provider) {
		return nil, api.BadRequest("unknown provider")
	}
	// Existing daemon acceptance is asynchronous. Never wait here for login,
	// readiness or network verification; clients query connection status/check.
	value, err := cfg.lockedConnectionConnect(ctx, caller, params)
	var refused *api.Error
	if errors.As(err, &refused) && refused.Code == "binary_required" {
		// The CLI replaces refusal hints, so the way forward is result data.
		return controlConnectorRequired{Provider: in.Provider, State: "connector-required", Code: refused.Code, Next: []advisor.Step{
			{Kind: "command", Command: []string{"binaries", "update"}, Input: map[string]any{"product": connectorProduct(in.Provider)}},
			{Kind: "command", Command: []string{"connection", "connect"}},
		}}, nil
	}
	return controlResult(value, err)
}

type controlConnectorRequired struct {
	Provider string         `json:"provider"`
	State    string         `json:"state"`
	Code     string         `json:"code"`
	Next     []advisor.Step `json:"next"`
}

// connectorProduct is the downloadable program a provider runs; IPv6 has none.
func connectorProduct(provider string) string {
	if provider == "cloudflare" {
		return "cloudflared"
	}
	if provider == "tailscale" || provider == "netbird" {
		return provider
	}
	return ""
}

func (cfg Config) controlConnectionForget(ctx context.Context, caller api.Caller, params json.RawMessage) (any, error) {
	return controlResult(cfg.lockedConnectionForget(ctx, caller, params))
}
