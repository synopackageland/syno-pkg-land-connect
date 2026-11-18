package provider

import (
	"context"
	"errors"

	"dsmapp/internal/cloudflare"
	"dsmapp/internal/connector"
	"dsmapp/internal/dependency"
)

// CloudflareTunnel runs official cloudflared for one public address to DSM.
type CloudflareTunnel struct{ *cloudflare.Tunnel }

func (*CloudflareTunnel) ID() string { return "cloudflare" }
func request(s Settings) cloudflare.Request {
	return cloudflare.Request{ReuseName: s.ReuseName, Mode: s.Mode, Subdomain: s.Subdomain, Token: s.Token, Hostname: s.Hostname}
}
func (p *CloudflareTunnel) Validate(s Settings) error {
	if len(s.Mappings) != 0 {
		return errors.New("unsupported cloudflare settings")
	}
	return cloudflare.Validate(request(s))
}
func (p *CloudflareTunnel) Start(ctx context.Context, s Settings) (connector.Status, error) {
	if e := p.Validate(s); e != nil {
		return p.Status(), e
	}
	e := p.Tunnel.Start(ctx, request(s))
	return p.Status(), e
}
func (p *CloudflareTunnel) Schema() []Field {
	return []Field{{"mode", "string", true, false}, {"subdomain", "string", false, false}}
}
func (p *CloudflareTunnel) Endpoints() []Endpoint { return p.Status().Endpoints }
func (*CloudflareTunnel) Binaries() []BinaryRequirement {
	return []BinaryRequirement{{Name: "cloudflared", VersionArgs: []string{"--version"}}}
}
func (*CloudflareTunnel) AcceptMappings(m []PortMapping) error {
	if len(m) != 0 {
		return errors.New("cloudflare serves DSM only")
	}
	return nil
}

func (p *CloudflareTunnel) BeforeDisable() error { return p.CheckRemoval(dependency.Tunnel) }
func (p *CloudflareTunnel) PreflightSettings(_ context.Context, s Settings) error {
	if s.Mode != "domain" || !p.PreserveDomain(s.Subdomain) {
		return p.CheckRemoval(dependency.OwnDomain)
	}
	return nil
}
func (p *CloudflareTunnel) PreserveOnReconnect(s Settings) bool {
	// A domain setup reuses retained account authorization, including when
	// recovering a different name. Only explicit disconnect revokes it.
	return s.Mode == "domain" && p.Status().Mode != "token"
}
