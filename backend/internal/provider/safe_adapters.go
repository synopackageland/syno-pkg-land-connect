package provider

import (
	"context"
	"dsmapp/internal/connector"
	"net"
	"reflect"
	"strconv"
)

func checkLocalPorts(ctx context.Context, m []PortMapping) error {
	for _, p := range m {
		network, target := "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p.LocalPort))
		if p.Protocol == "unix" {
			network, target = "unix", p.Socket
		}
		c, e := (&net.Dialer{}).DialContext(ctx, network, target)
		if e != nil {
			return connector.Coded("apply_target_unreachable", "a selected local service is not listening")
		}
		c.Close()
	}
	return nil
}
func (p *CloudflareTunnel) PrecheckUpdate(ctx context.Context, s Settings) error {
	return p.Tunnel.CheckUpdate(ctx, request(s))
}
func (p *CloudflareTunnel) ApplyUpdate(ctx context.Context, s Settings) error {
	return p.Tunnel.StageUpdate(ctx, request(s))
}
func (p *CloudflareTunnel) VerifyUpdate(ctx context.Context, _ Settings) (bool, error) {
	return p.Tunnel.VerifyUpdate(ctx)
}
func (p *CloudflareTunnel) CommitUpdate(ctx context.Context) error { return p.Tunnel.CommitUpdate(ctx) }
func (p *CloudflareTunnel) RollbackUpdate(ctx context.Context, _ Settings) error {
	return p.Tunnel.UndoUpdate(ctx)
}
func (p *CloudflareTunnel) FinishUpdate(committed bool)           { p.Tunnel.FinishUpdate(committed) }
func (p *CloudflareTunnel) CheckHealth(ctx context.Context) error { return p.Tunnel.CheckHealth(ctx) }

var _ liveUpdater = (*CloudflareTunnel)(nil)

// NetBird has no local mapping API. Replacing a logged-in identity or its
// management server cannot be done without losing its only private connection.
func (p *NetBird) PrecheckUpdate(ctx context.Context, s Settings) error {
	if e := p.Validate(s); e != nil {
		return e
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	url, e := netbirdManagementURL(s.ManagementURL)
	if e != nil {
		return e
	}
	if s.Token != "" || url != p.saved.Settings.ManagementURL || !reflect.DeepEqual(s.LoginOrigins, p.saved.Settings.LoginOrigins) {
		return errApplyUnsupported
	}
	if p.session == nil {
		return ErrNetBirdStart
	}
	observed, e := p.session.Inspect(ctx)
	if e != nil {
		return e
	}
	// Only NetBird itself saying the sign-in is gone asks for a new one.
	if observed.DaemonStatus == "NeedsLogin" || observed.DaemonStatus == "SessionExpired" || observed.DaemonStatus == "LoginFailed" {
		return connector.Coded("apply_reauth_required", "sign in to NetBird again")
	}
	if !observed.ManagementConnected || !observed.SignalConnected || observed.IP == "" {
		return nbError("control_unreachable")
	}
	return nil
}
func (p *NetBird) sessionSnapshot(ctx context.Context) (bool, error) {
	if p.session == nil {
		return false, nil
	}
	s, e := p.session.Inspect(ctx)
	return s.ManagementConnected && s.SignalConnected && s.IP != "", e
}
func (*NetBird) ApplyUpdate(context.Context, Settings) error { return nil }
func (p *NetBird) VerifyUpdate(ctx context.Context, _ Settings) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sessionSnapshot(ctx)
}
func (*NetBird) RollbackUpdate(context.Context, Settings) error { return nil }
func (p *NetBird) CheckHealth(ctx context.Context) error        { return p.observeHealth(ctx) }

// Tailscale has no local setting to change in place: Serve follows the
// approved private Apps. A new key or control server replaces the identity.
func (p *Tailscale) PrecheckUpdate(ctx context.Context, s Settings) error {
	if e := p.Validate(s); e != nil {
		return e
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if s.Token != "" || s.LoginServer != p.saved.Settings.LoginServer || s.LoginOrigin != p.saved.Settings.LoginOrigin {
		return errApplyUnsupported
	}
	if p.session == nil {
		return connector.Coded("apply_reauth_required", "sign in to Tailscale again")
	}
	snap, e := p.session.Inspect(ctx)
	if e != nil {
		return e
	}
	if snap.BackendState != "Running" {
		return connector.Coded("apply_reauth_required", "sign in to Tailscale again")
	}
	return nil
}
func (*Tailscale) ApplyUpdate(context.Context, Settings) error { return nil }
func (p *Tailscale) VerifyUpdate(ctx context.Context, _ Settings) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.session == nil {
		return false, errApplyUnsupported
	}
	observed, e := p.session.Inspect(ctx)
	if e != nil {
		return false, e
	}
	return observed.BackendState == "Running" && observed.Serving, nil
}
func (*Tailscale) RollbackUpdate(context.Context, Settings) error { return nil }
func (p *Tailscale) CheckHealth(ctx context.Context) error        { return p.observeHealth(ctx) }

func (p *IPv6Runtime) PrecheckUpdate(ctx context.Context, s Settings) error {
	if !s.ConfirmExposure {
		return errApplyUnsupported
	}
	if e := validateIPv6Mappings(s.Mappings); e != nil {
		return e
	}
	p.mu.Lock()
	snapshot := p.inspect(ctx, ipv6Config{Mappings: s.Mappings})
	p.mu.Unlock()
	if snapshot.State != "available" {
		return connector.Coded("apply_target_unreachable", "IPv6 prerequisites are unavailable")
	}
	return p.checkLocal(ctx, s.Mappings)
}
func (p *IPv6Runtime) ApplyUpdate(_ context.Context, s Settings) error {
	return p.AcceptMappings(s.Mappings)
}
func (p *IPv6Runtime) VerifyUpdate(ctx context.Context, s Settings) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	saved, e := p.read()
	if e != nil {
		return false, e
	}
	return reflect.DeepEqual(saved.Mappings, s.Mappings) && p.inspect(ctx, saved).State == "available", nil
}
func (p *IPv6Runtime) RollbackUpdate(ctx context.Context, s Settings) error {
	return p.ApplyUpdate(ctx, s)
}
func (p *IPv6Runtime) CheckHealth(ctx context.Context) error {
	p.mu.Lock()
	m := append([]PortMapping(nil), p.config.Mappings...)
	state := p.inspect(ctx, p.config)
	p.mu.Unlock()
	if state.State != "available" {
		return connector.Coded("apply_target_unreachable", "IPv6 prerequisites are unavailable")
	}
	return p.checkLocal(ctx, m)
}

func (p *IPv6Runtime) checkLocal(ctx context.Context, m []PortMapping) error {
	if p.CheckLocal != nil {
		return p.CheckLocal(ctx, m)
	}
	return checkLocalPorts(ctx, m)
}
