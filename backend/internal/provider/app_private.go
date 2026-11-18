package provider

import (
	"context"
	"strings"

	"dsmapp/internal/connector"
)

type AppNetwork struct {
	Host      string
	Available bool
	Code      string
}

// AppNetwork reads the authenticated daemon without creating a Serve mapping.
func (p *Tailscale) AppAccessNetwork(ctx context.Context) (AppNetwork, error) {
	s, err := p.AppNetwork(ctx)
	if err != nil {
		return AppNetwork{Code: "tailscale-off"}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	out := AppNetwork{Code: "tailscale-off"}
	if !p.saved.Enabled || p.session == nil {
		return out, nil
	}
	if s.BackendState != "Running" {
		return out, nil
	}
	out.Host = strings.TrimSuffix(s.DNSName, ".")
	if !unixServeVersion(s.Version) {
		out.Code = "tailscale-outdated"
		return out, nil
	}
	for _, name := range s.CertDomains {
		if name == out.Host {
			out.Available = true
			out.Code = ""
			return out, nil
		}
	}
	out.Code = "https-off"
	return out, nil
}

// configureApps serves exactly the approved private Apps; none withdraws
// every owned route.
func (p *Tailscale) configureApps(ctx context.Context) error {
	apps := []PortMapping{}
	if p.AppMappings != nil {
		extra, err := p.AppMappings(ctx)
		if err != nil {
			return err
		}
		apps = extra
	}
	return p.session.Configure(ctx, apps)
}

// RefreshApps shares the runtime lock with tracker replacement and lifecycle.
// It never enables a disconnected provider or changes its saved exposure scope.
func (p *Tailscale) RefreshApps(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.saved.Enabled || p.session == nil {
		return nil
	}
	err := p.configureApps(ctx)
	// A changed route set gets its own serve window; a revoked pin waits for
	// review without one.
	p.stage, p.stageSince = "serve", p.now()
	if connector.ErrorCode(err) == "pin_proxy_required" {
		p.stage = "review"
	}
	if err != nil {
		p.configured = false
		return err
	}
	p.configured = true
	return nil
}
