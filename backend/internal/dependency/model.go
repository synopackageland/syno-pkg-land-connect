// Package dependency owns the self-hosted server's dependency order, failure
// attribution and upstream-removal policy. Free link relays are a separate branch.
package dependency

import (
	"context"
	"dsmapp/internal/connector"
	"strconv"
	"sync"
	"time"
)

type Resource string

const (
	Server        Resource = "netbird-server"
	Tunnel        Resource = "cloudflare-tunnel"
	OwnDomain     Resource = "own-domain"
	Authorization Resource = "domain-authorization"
	Bridge        Resource = "signal-bridge"
	Token         Resource = "workers-token"
	FreeLink      Resource = "free-link"
)

type Edge struct {
	Dependent Resource `json:"dependent"`
	Upstream  Resource `json:"upstream"`
	Impact    string   `json:"impact"`
}

// Impact distinguishes an interrupted connection from a credential needed for
// maintenance: an already-deployed Worker may serve after its API token expires.
var edges = []Edge{
	{Server, Tunnel, "connection"}, {Server, OwnDomain, "connection"}, {Server, Authorization, "maintenance"},
	{Server, Bridge, "connection"}, {Bridge, Tunnel, "connection"}, {Bridge, OwnDomain, "connection"},
	{Bridge, Token, "maintenance"}, {FreeLink, Token, "maintenance"},
}

func Edges() []Edge { return append([]Edge(nil), edges...) }
func needs(from, to Resource) bool {
	for _, e := range edges {
		if e.Dependent == from && (e.Upstream == to || needs(e.Upstream, to)) {
			return true
		}
	}
	return false
}

type Cloudflare struct{ State, Mode, Zone, Code string }
type Problem struct {
	Code   string `json:"code"`
	Piece  string `json:"piece"`
	Action string `json:"action"`
	Page   string `json:"page"`
	Hop    string `json:"hop"`
	Impact string `json:"impact"`
}
type Report struct {
	Checking       bool     `json:"checking,omitempty"`
	Zone           string   `json:"zone,omitempty"`
	UpstreamsReady bool     `json:"upstreamsReady"`
	Enabled        bool     `json:"enabled"`
	Problem        *Problem `json:"problem,omitempty"`
}

func problem(code string) *Problem {
	p := &Problem{Code: code, Piece: "tunnel", Hop: "tunnel", Action: "connect_cloudflare", Page: "cloudflare", Impact: "connection"}
	switch code {
	case "cloudflare_connecting":
		p.Action = "check_status"
	case "cloudflare_own_domain_required":
		p.Piece = "own_domain"
		p.Action = "connect_domain"
	case "cloudflare_reconnect_required":
		p.Piece = "authorization"
		p.Action = "authorize_cloudflare"
		p.Impact = "maintenance"
	case "workers_not_connected", "netbird_server_workers_required":
		p.Piece = "token"
		p.Hop = "bridge"
		p.Action = "save_token"
		p.Page = "netbird"
		p.Impact = "maintenance"
	case "workers_token_invalid", "workers_routes_permission", "workers_zone_account", "workers_permission_denied":
		p.Piece = "token"
		p.Hop = "bridge"
		p.Action = "replace_token"
		p.Page = "netbird"
		p.Impact = "maintenance"
	case "workers_unavailable", "workers_api_refused", "workers_rate_limited", "workers_state_corrupt":
		p.Piece = "token"
		p.Hop = "bridge"
		p.Action = "check_token"
		p.Page = "netbird"
		p.Impact = "maintenance"
	case "workers_route_conflict", "workers_name_conflict":
		p.Piece = "bridge"
		p.Hop = "bridge"
		p.Action = "review_bridge"
		p.Page = "cloudflare"
	case "netbird_server_bridge_missing":
		p.Piece = "bridge"
		p.Hop = "bridge"
		p.Action = "repair_bridge"
		p.Page = "netbird"
	case "netbird_server_signal_blocked":
		p.Piece = "signal"
		p.Hop = "signal"
		p.Action = "repair_bridge"
		p.Page = "netbird"
	case "netbird_server_grpc_blocked":
		p.Piece = "own_domain"
		p.Action = "enable_grpc"
	case "netbird_server_unreachable":
		p.Piece = "own_domain"
		p.Action = "check_route"
	case "cloudflare_tunnel_missing":
		p.Action = "authorize_cloudflare"
	default:
		if code != "cloudflare_not_connected" {
			p.Action = "check_route"
		}
	}
	return p
}

type Model struct {
	observeMu       sync.Mutex
	observation     Report
	observationKey  string
	observing       bool
	generation      uint64
	observedUntil   time.Time
	Enabled         func() bool
	Cloudflare      func() Cloudflare
	Token           func(context.Context, string) error
	Bridge          func(context.Context, string, string) error
	Signal          func(context.Context, string) error
	mu              sync.Mutex
	key             string
	until           time.Time
	cached          *Problem
	cachedUpstreams bool
}

func (m *Model) Guard(upstream Resource) error {
	if m.Enabled != nil && m.Enabled() && needs(Server, upstream) {
		return connector.Coded("netbird_cloudflare_in_use", "NetBird devices will lose connection; turn off the NetBird server first, then Cloudflare")
	}
	return nil
}
func (m *Model) local() (Cloudflare, *Problem) {
	c := m.Cloudflare()
	if c.State != "connected" {
		code := "cloudflare_not_connected"
		if c.State == "connecting" || c.State == "waiting" || c.State == "backoff" {
			code = "cloudflare_connecting"
		}
		if c.Code == "cloudflare_tunnel_missing" {
			code = c.Code
		}
		return c, problem(code)
	}
	if c.Mode != "domain" {
		return c, problem("cloudflare_own_domain_required")
	}
	if c.Code != "" {
		return c, problem(c.Code)
	}
	return c, nil
}
func errorProblem(e error) *Problem {
	if e == nil {
		return nil
	}
	code := connector.ErrorCode(e)
	if code == "" {
		code = "workers_unavailable"
	}
	return problem(code)
}

// Read names the first broken upstream. Downstream network symptoms cannot
// override a disconnected tunnel. Remote checks are bounded, serialized and
// shared by both pages for 15 seconds; local connection state is always fresh.
func (m *Model) Read(ctx context.Context, host string, enabled bool) Report {
	r := Report{Enabled: enabled}
	c, p := m.local()
	r.Zone = c.Zone
	if p != nil {
		r.Problem = p
		return r
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := c.Zone + "\x00" + host + strconv.FormatBool(enabled)
	if time.Now().Before(m.until) && m.key == key {
		r.Problem = m.cached
		r.UpstreamsReady = m.cachedUpstreams
		return r
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	if m.Token != nil {
		p = errorProblem(m.Token(ctx, c.Zone))
	}
	r.UpstreamsReady = p == nil
	if p == nil && host != "" && enabled && m.Bridge != nil {
		p = errorProblem(m.Bridge(ctx, c.Zone, host))
	}
	if p == nil && host != "" && enabled && m.Signal != nil {
		p = errorProblem(m.Signal(ctx, "https://"+host))
	}
	m.key, m.cached, m.until = key, p, time.Now().Add(15*time.Second)
	m.cachedUpstreams = r.UpstreamsReady
	r.Problem = p
	return r
}

// Preflight checks only existing upstreams. Setup itself creates the server's
// own bridge, so it must not require that bridge to exist before setup starts.
func (m *Model) Preflight(ctx context.Context) (string, error) {
	r := m.Read(ctx, "", false)
	if r.Problem != nil {
		return "", connector.Coded(r.Problem.Code, "the NetBird server dependency needs attention")
	}
	return r.Zone, nil
}

// Invalidate follows an explicit retry or a completed repair, so a previous
// failure cannot hide fresh ownership and readiness evidence.
func (m *Model) Invalidate() {
	m.observeMu.Lock()
	m.generation++
	m.observedUntil = time.Time{}
	m.observeMu.Unlock()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.until = time.Time{}
}

// Observe never makes a status RPC wait behind an active cloud mutation or a
// remote check. Unknown evidence stays unknown while one shared refresh runs.
func (m *Model) Observe(_ context.Context, host string, enabled bool) Report {
	c, p := m.local()
	if p != nil {
		return Report{Zone: c.Zone, Enabled: enabled, Problem: p}
	}
	key := c.Zone + "\x00" + host + strconv.FormatBool(enabled)
	m.observeMu.Lock()
	defer m.observeMu.Unlock()
	r := m.observation
	if key != m.observationKey || time.Now().After(m.observedUntil) {
		r = Report{Zone: c.Zone, Enabled: enabled, Checking: true}
	}
	if r.Checking && !m.observing {
		m.observing = true
		generation := m.generation
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			next := m.Read(ctx, host, enabled)
			m.observeMu.Lock()
			defer m.observeMu.Unlock()
			m.observing = false
			if generation == m.generation {
				m.observation = next
				m.observationKey = key
				m.observedUntil = time.Now().Add(15 * time.Second)
			}
		}()
	}
	return r
}
