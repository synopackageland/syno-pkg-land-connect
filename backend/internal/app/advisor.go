package app

import (
	"context"
	"encoding/json"
	"net/http"

	"dsmapp/internal/advisor"
	"dsmapp/internal/binaries"
	"dsmapp/internal/connector"
	"dsmapp/internal/dsmexternal"
	"dsmapp/internal/ipv6"
	"dsmapp/internal/ports"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

var runtimeProviders = []string{"cloudflare", "tailscale", "netbird", "ipv6"}

// recommend ranks the catalog for the Overview and ctl recommend alike. It
// reads only; nothing is enabled.
func (cfg Config) recommend(ctx context.Context, caller api.Caller, params json.RawMessage) (any, error) {
	var in struct {
		Goal string `json:"goal"`
	}
	if err := decodeParams(params, &in); err != nil {
		return nil, err
	}
	catalog := advisor.Default()
	if in.Goal != "" && !contains(catalog.Goals, in.Goal) {
		return nil, api.BadRequest("unknown goal")
	}
	facts := advisor.Facts{IPv6Global: ipv6Global(cfg.inspectIPv6(ctx)), States: map[string]string{}, Modes: map[string]string{}, DSMHTTPPort: cfg.dsmHTTPPort(ctx)}
	for _, s := range cfg.liveConnections(ctx, caller) {
		facts.States[s.Provider], facts.Modes[s.Provider] = s.State, s.Mode
	}
	return advisor.Recommend(catalog, in.Goal, facts), nil
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (cfg Config) inspectIPv6(ctx context.Context) ipv6.Status {
	value, _ := cfg.ipv6Status(ctx, api.Caller{}, nil)
	s, _ := value.(ipv6.Status)
	return s
}

// ipv6Global is nil when the address check itself could not run.
func ipv6Global(s ipv6.Status) *bool {
	found := len(s.Addresses) > 0
	if !found && s.Reason != "no_global_address" {
		return nil
	}
	return &found
}

func (cfg Config) dsmHTTPPort(ctx context.Context) int {
	s, err := (ports.Store{Dir: cfg.DataDir}).List(ctx, cfg.portDetector())
	if err != nil {
		return 0
	}
	for _, e := range s.Entries {
		if e.ID == "dsm-http" && e.Port != nil {
			return *e.Port
		}
	}
	return 0
}

// liveConnections is one status per runtime provider; providers the daemon
// does not report, or every provider without a daemon, are disconnected.
func (cfg Config) liveConnections(ctx context.Context, caller api.Caller) []connector.Status {
	byProvider := map[string]connector.Status{}
	if value, err := cfg.connectionStatus(ctx, caller, nil); err == nil {
		if s, ok := value.(connector.Status); ok {
			byProvider[s.Provider] = s
			for _, child := range s.Connections {
				byProvider[child.Provider] = child
			}
		}
	}
	out := []connector.Status{}
	for _, id := range runtimeProviders {
		s, ok := byProvider[id]
		if !ok {
			s = connector.Status{Provider: id, State: "disconnected"}
		}
		out = append(out, s)
	}
	return out
}

type diagnosis struct {
	controlConnectionView
	Connector string         `json:"connector,omitempty"` // installed | missing | busy | unknown
	Addresses []string       `json:"addresses"`
	Next      []advisor.Step `json:"next"`
}

type diagnoseResult struct {
	CatalogVersion string            `json:"catalogVersion"`
	IPv6Global     *bool             `json:"ipv6Global"`
	Providers      []diagnosis       `json:"providers"`
	DSMExternal    externalDiagnosis `json:"dsmExternal"`
	DSMSecurity    securityDiagnosis `json:"dsmSecurity"`
}

// externalDiagnosis is DSM's external access address against the stable one
// a running method offers; next proposes the change but never makes it.
type externalDiagnosis struct {
	dsmexternal.Advice
	Next []advisor.Step `json:"next"`
}

// controlDiagnose explains each method's state and the one next step. It
// reads only; a pending sign-in link is the caller's to open.
func (cfg Config) controlDiagnose(ctx context.Context, caller api.Caller, request *http.Request, params json.RawMessage) (any, error) {
	if err := decodeParams(params, &struct{}{}); err != nil {
		return nil, err
	}
	out := diagnoseResult{CatalogVersion: advisor.Default().Version, IPv6Global: ipv6Global(cfg.inspectIPv6(ctx)), Providers: []diagnosis{}}
	for _, s := range cfg.liveConnections(ctx, caller) {
		d := diagnosis{controlConnectionView: controlConnection(s), Addresses: addresses(s)}
		d.Connections = nil
		if product := connectorProduct(s.Provider); product != "" {
			d.Connector = cfg.connectorInstalled(ctx, product)
		}
		d.Next = diagnoseNext(d, s.Mode == "domain" && s.Hostname != "")
		out.Providers = append(out.Providers, d)
	}
	out.DSMExternal = externalDiagnosis{Advice: cfg.externalAdvice(ctx, caller, request), Next: []advisor.Step{}}
	if s := out.DSMExternal.Suggested; s != nil && (out.DSMExternal.State == "differs" || out.DSMExternal.State == "not-set") {
		// The user decides: a value DSM already has is shown, never overwritten unasked.
		out.DSMExternal.Next = []advisor.Step{{Kind: "command", Command: []string{"external", "set"}, Input: map[string]any{"hostname": s.Hostname, "httpsPort": s.HTTPSPort}, Needs: []string{"user-consent"}}}
	}
	out.DSMSecurity = cfg.securityAdvice(ctx, request)
	return out, nil
}

func (cfg Config) connectorInstalled(ctx context.Context, product string) string {
	value, err := cfg.binaryRoute("status")(ctx, api.Caller{}, json.RawMessage(`{"product":"`+product+`"}`))
	s, ok := value.(binaries.Status)
	switch {
	case err != nil || !ok:
		return "unknown"
	case s.Busy:
		return "busy"
	case s.Current == "":
		return "missing"
	}
	return "installed"
}

func addresses(s connector.Status) []string {
	out := []string{}
	for _, e := range s.Endpoints {
		out = append(out, e.URL)
	}
	out = append(out, s.TailscaleIPs...)
	if s.NetBirdIP != "" {
		out = append(out, s.NetBirdIP)
	}
	if s.NetBirdFQDN != "" {
		out = append(out, s.NetBirdFQDN)
	}
	return out
}

// saved is a Cloudflare own domain that reconnects without signing in; its
// name stays out of the result like every other connection detail.
func diagnoseNext(d diagnosis, saved bool) []advisor.Step {
	again := advisor.Step{Kind: "command", Command: []string{"diagnose"}}
	switch {
	case d.State == "failed" && d.Code == "binary_required":
		return []advisor.Step{{Kind: "command", Command: []string{"binaries", "update"}, Input: map[string]any{"product": connectorProduct(d.Provider)}}, {Kind: "command", Command: []string{"connection", "connect"}, Input: map[string]any{"provider": d.Provider}}}
	case d.State == "needs-login" && d.LoginURL != "":
		return []advisor.Step{{Kind: "user", Action: "open-login-url", URL: d.LoginURL}, again}
	case d.State == "needs-login":
		return []advisor.Step{{Kind: "user", Action: "open-connect-page"}, again}
	case d.State == "needs-machine-auth":
		return []advisor.Step{{Kind: "user", Action: "approve-device"}, again}
	case d.State == "connecting" || d.State == "stopping":
		return []advisor.Step{again}
	case d.State == "connected":
		return []advisor.Step{{Kind: "command", Command: []string{"connection", "check"}, Input: map[string]any{"provider": d.Provider}}}
	case d.State == "failed" && d.Provider == "cloudflare" && saved:
		// A saved own domain reconnects without signing in; it publishes DSM again.
		return []advisor.Step{{Kind: "command", Command: []string{"connection", "connect"}, Input: map[string]any{"provider": "cloudflare", "mode": "domain", "confirmExposure": true}, Needs: []string{"user-consent"}}}
	case d.State == "failed":
		return []advisor.Step{{Kind: "user", Action: "resolve-failure"}}
	}
	return []advisor.Step{}
}
