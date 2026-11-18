package provider

import (
	"context"
	"dsmapp/internal/connector"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"time"
)

// Snapshots contain accepted settings only. Identity files are private runtime
// references, never copied into the common journal or exposed to clients.
type updateSnapshotter interface {
	SnapshotUpdate() (json.RawMessage, error)
	RestoreUpdate(json.RawMessage) error
}

func (p *CloudflareTunnel) SnapshotUpdate() (json.RawMessage, error) {
	raw, e := p.Tunnel.SnapshotUpdate()
	return json.RawMessage(raw), e
}
func (p *CloudflareTunnel) RestoreUpdate(raw json.RawMessage) error {
	return p.Tunnel.RestoreUpdate(raw)
}
func (p *Tailscale) SnapshotUpdate() (json.RawMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.saved
	s.Settings.Token = ""
	return json.Marshal(s)
}
func (p *Tailscale) RestoreUpdate(raw json.RawMessage) error {
	var s tailscaleSaved
	if json.Unmarshal(raw, &s) != nil || s.Settings.Token != "" || p.Validate(s.Settings) != nil {
		return errApplyRecovery
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.persist(s)
}
func (p *NetBird) SnapshotUpdate() (json.RawMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.saved
	s.Settings.Token = ""
	return json.Marshal(s)
}
func (p *NetBird) RestoreUpdate(raw json.RawMessage) error {
	var s netbirdSaved
	if json.Unmarshal(raw, &s) != nil || s.Settings.Token != "" || p.Validate(s.Settings) != nil {
		return errApplyRecovery
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.persist(s)
}
func (p *IPv6Runtime) SnapshotUpdate() (json.RawMessage, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return json.Marshal(p.config)
}
func (p *IPv6Runtime) RestoreUpdate(raw json.RawMessage) error {
	var s ipv6Config
	if json.Unmarshal(raw, &s) != nil || s.Schema != 1 || validateIPv6Mappings(s.Mappings) != nil {
		return errApplyRecovery
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if e := p.save(s); e != nil {
		return e
	}
	p.config = s
	return nil
}

// CheckHealth must not call the legacy status() methods, which may configure
// routes or persist a failed-login gate. These probes read memory/socket/network.
// Like NetBird's, the read runs without the provider lock, and only what
// Tailscale itself reports counts as a failure: its daemon exited, it asks
// for a sign-in, or the Serve routes of its private Apps are gone. A read that did not
// finish in time, or a daemon still starting, is incomplete.
func (p *Tailscale) observeHealth(ctx context.Context) error {
	p.mu.Lock()
	session, attempt, starting := p.session, p.attempt, p.restoring.Active()
	p.mu.Unlock()
	if starting {
		return healthWaiting("starting", attempt, errors.New("Tailscale is still starting"))
	}
	if session == nil {
		return healthStep("session", attempt, ErrTailscaleStart)
	}
	s, e := session.Inspect(ctx)
	if errors.Is(e, ErrDaemonExited) || e != nil && connector.ErrorCode(e) != "" {
		return healthStep("status", attempt, e)
	}
	if e != nil {
		return healthWaiting("status", attempt, e)
	}
	switch s.BackendState {
	case "Running":
	case "NoState", "Starting":
		return healthWaiting("status", attempt, errors.New("Tailscale is still starting"))
	case "NeedsLogin", "NeedsMachineAuth":
		return healthStep("login", attempt, connector.Coded("apply_reauth_required", "sign in to Tailscale again"))
	default:
		return healthStep("status", attempt, connector.Coded("provider_status_invalid", "Tailscale is not running"))
	}
	if !s.Serving {
		return healthStep("serve", attempt, connector.Coded("apply_readback_failed", "the accepted Serve configuration is missing"))
	}
	return nil
}

// observeHealth reads NetBird's own state without holding the provider lock,
// so a slow read on a loaded NAS never blocks status. Only what NetBird itself
// reports counts as a failure: its daemon exited, it lost its management or
// signal connection, or its sign-in is gone. A read that did not finish in
// time, or a busy DSM, is incomplete: restarting NetBird would not fix it.
func (p *NetBird) observeHealth(ctx context.Context) error {
	p.mu.Lock()
	session, attempt, starting := p.session, p.attempt, p.restoring.Active()
	port := p.verifiedPort
	if port == 0 {
		port = p.healthPort
	}
	failure := p.failure
	p.mu.Unlock()
	if starting {
		return healthWaiting("starting", attempt, errors.New("NetBird is still starting"))
	}
	if session == nil {
		if netbirdLoginGone(connector.ErrorCode(failure)) {
			return healthStep("session", attempt, connector.Coded("apply_reauth_required", "sign in to NetBird again"))
		}
		return healthStep("session", attempt, ErrNetBirdStart)
	}
	responder, answers := session.(interface{ Responsive(context.Context) error })
	read, done := ctx, context.CancelFunc(func() {})
	if deadline, ok := ctx.Deadline(); ok && answers {
		// Keep a quarter of the budget to ask the daemon itself.
		read, done = context.WithDeadline(ctx, deadline.Add(-time.Until(deadline)/4))
	}
	s, e := session.Inspect(read)
	done()
	if errors.Is(e, ErrDaemonExited) {
		return healthStep("status", attempt, e)
	}
	if e != nil {
		// NetBird answers for itself but not for its connection engine: the
		// engine is still trying to reach the management or signal server,
		// which only NetBird can fix. That counts like a lost control server.
		// A daemon that answers nothing may be a swapping NAS: keep waiting.
		if answers && ctx.Err() == nil && responder.Responsive(ctx) == nil {
			return healthStep("network", attempt, nbError("control_unreachable"))
		}
		return healthWaiting("status", attempt, e)
	}
	switch {
	case s.Code != "":
		if netbirdLoginGone(s.Code) {
			return healthStep("login", attempt, connector.Coded("apply_reauth_required", "sign in to NetBird again"))
		}
		return healthStep("status", attempt, nbError(s.Code))
	case s.DaemonStatus == "NeedsLogin" || s.DaemonStatus == "SessionExpired" || s.DaemonStatus == "LoginFailed":
		return healthStep("login", attempt, connector.Coded("apply_reauth_required", "sign in to NetBird again"))
	case !s.ManagementConnected || !s.SignalConnected || s.IP == "":
		return healthStep("network", attempt, nbError("control_unreachable"))
	}
	checker, ok := session.(interface {
		CheckHTTP(context.Context, string, int) error
	})
	if !ok {
		return errApplyUnsupported
	}
	if port == 0 {
		return healthWaiting("dsm-port", attempt, errors.New("DSM port not detected yet"))
	}
	if e = checker.CheckHTTP(ctx, "127.0.0.1", port); e != nil {
		return healthWaiting("dsm-api", attempt, e)
	}
	if _, ok := session.(*netbirdExecSession); ok {
		transport := netbirdLocalTransport(port)
		defer transport.CloseIdleConnections()
		if e = connector.CheckDSMPage(ctx, &http.Client{Transport: transport}, "http://"+net.JoinHostPort("127.0.0.1", strconv.Itoa(port))); e != nil {
			return healthWaiting("dsm-page", attempt, e)
		}
		return nil
	}
	return errApplyUnsupported
}

// netbirdLoginGone names the codes with which NetBird itself refused the
// saved sign-in. No other failure may ask the administrator to sign in again.
func netbirdLoginGone(code string) bool {
	return code == "login_denied" || code == "apply_reauth_required"
}
