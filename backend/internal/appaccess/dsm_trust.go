package appaccess

import (
	"context"
	"dsmapp/internal/apps"
	"dsmapp/internal/connector"
	"errors"
)

// TrustDSMFirstUse is called only by an administrator's connect request, never
// restore, health repair or polling. A retained pin (including a withdrawn
// endpoint) is a trust history: reconnect must never replace it.
func (e *Engine) TrustDSMFirstUse(ctx context.Context) error {
	state, err := e.cfg.Store.Load()
	if err != nil || state.DSM != nil {
		return err
	}
	port, err := e.dsmHTTPSPort(ctx)
	if err != nil {
		return err
	}
	b, cert, err := e.proposal(ctx, apps.PinBinding{Target: apps.TargetDSM, Port: port, Authority: "localhost"}, nil)
	if err != nil {
		return err
	}
	b.CertSHA256 = cert.CertSHA256
	return e.cfg.Store.Update(ctx, func(s *apps.State) error {
		if s.DSM != nil {
			return nil
		}
		s.DSM = &apps.DSMState{Port: port, Pin: pin(b), Certificate: &apps.TrustedCertificate{Issuer: cert.Issuer, Expires: cert.Expires, CertSHA256: cert.CertSHA256}}
		_, err := s.Revision()
		return err
	})
}

// BindTrustedDSM supplies a provider endpoint from the retained trust. It can
// reuse an unchanged certificate in either scope, but cannot establish trust.
// No provider call here: the caller may hold its own runtime lock.
func (e *Engine) BindTrustedDSM(ctx context.Context, kind, authority string) error {
	// Lifecycle activation/withdrawal already holds the state writer lock.
	// Revalidating an existing endpoint is read-only: taking that lock again
	// deadlocks, even when Update would make no logical change. Fresh TLS and
	// port checks still run; the provider resolves the current saved endpoint
	// again before using it, so this cannot restore a concurrently revoked one.
	state, err := e.cfg.Store.Load()
	if err != nil {
		return err
	}
	if d := state.DSM; d != nil {
		ep := d.Public
		if kind == apps.Private {
			ep = d.Private
		}
		if ep != nil && ep.Authority == authority {
			if err := e.verifyRetainedDSM(ctx, d, kind, authority); err != nil {
				return err
			}
			return e.activateProxies(apps.Target{Kind: kind})
		}
	}
	err = e.cfg.Store.Update(ctx, func(s *apps.State) error {
		d := s.DSM
		if d == nil {
			return connector.Coded("pin_proxy_required", "DSM certificate review required")
		}
		if err := e.verifyRetainedDSM(ctx, d, kind, authority); err != nil {
			return err
		}
		ep := d.Public
		if kind == apps.Private {
			ep = d.Private
		}
		if ep != nil && ep.Authority == authority {
			return nil
		}
		if kind == apps.Public {
			for _, t := range apps.Targets(*s) {
				if t.Kind == apps.Public && t.Scope.Provider == apps.Workers {
					return apps.ErrUnavailable
				}
			}
			if d.Public != nil && d.Public.Provider == apps.Workers {
				return apps.ErrUnavailable
			}
		}
		id, err := dsmSocketID(kind, authority)
		if err != nil {
			return err
		}
		ep = &apps.Endpoint{Socket: id, Authority: authority}
		if kind == apps.Private {
			d.Private = ep
		} else {
			d.Public = ep
			d.Origin = "https://" + authority
		}
		_, err = s.Revision()
		return err
	})
	if err != nil {
		return err
	}
	return e.activateProxies(apps.Target{Kind: kind})
}

// verifyRetainedDSM checks that the retained trust still matches DSM's port
// and certificate; a change needs the certificate review.
func (e *Engine) verifyRetainedDSM(ctx context.Context, d *apps.DSMState, kind, authority string) error {
	port, err := e.dsmHTTPSPort(ctx)
	if err != nil {
		return err
	}
	if d.Port != port {
		return connector.DSMPortChanged(d.Port, port)
	}
	b := apps.PinBinding{Target: apps.TargetDSM, Scope: kind, Port: d.Port, Authority: authority, Mode: apps.Mode(d.Pin.Mode), Names: d.Pin.Names, SPKI: d.Pin.SPKI, CertSHA256: d.Pin.CertSHA256}
	if err := e.cfg.Verifier.VerifyTarget(ctx, b); err != nil {
		if apps.ReasonOf(err) == "" {
			return dsmHTTPSUnavailable(err)
		}
		return connector.Coded("pin_proxy_required", "DSM certificate changed")
	}
	return nil
}

// RefreshDSM repairs a valid approval whose proxy or provider status is stale.
func (e *Engine) RefreshDSM(ctx context.Context, kind string) error {
	if err := e.activateProxies(apps.Target{Kind: kind}); err != nil {
		return err
	}
	// Force reconciliation even when the durable route signature did not change.
	e.mu.Lock()
	delete(e.applied, kind)
	e.mu.Unlock()
	if err := e.Remove(ctx, apps.Target{Kind: kind}); err != nil {
		return err
	}
	return e.refreshDSMRoute(ctx, kind)
}

func dsmSocketID(kind, authority string) (string, error) {
	if kind == apps.Public && apps.QuickDSMHost(authority) {
		return apps.QuickDSMSocketID, nil
	}
	return socketID()
}

func (e *Engine) refreshDSMRoute(ctx context.Context, kind string) error {
	if kind == apps.Public {
		if p, ok := e.cfg.Public.(interface{ RefreshDSMRoute(context.Context) error }); ok {
			return p.RefreshDSMRoute(ctx)
		}
	}
	return nil
}

// Browser origin and advertised DSM addresses never select this local port.
func (e *Engine) dsmHTTPSPort(ctx context.Context) (int, error) {
	if e.cfg.DSMPort == nil {
		return 0, dsmHTTPSUnavailable(apps.ErrUnavailable)
	}
	port, err := e.cfg.DSMPort(ctx)
	if err != nil {
		return 0, dsmHTTPSUnavailable(err)
	}
	if port < 1 || port > 65535 {
		return 0, dsmHTTPSUnavailable(apps.ErrUnavailable)
	}
	return port, nil
}
func dsmHTTPSUnavailable(err error) error {
	if errors.Is(err, context.Canceled) {
		return err
	}
	return connector.Coded("apps_dsm_https_unavailable", "DSM HTTPS is unavailable; enable HTTPS in DSM Login Portal and try again")
}
