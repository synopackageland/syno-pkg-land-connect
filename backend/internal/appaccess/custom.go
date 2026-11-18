package appaccess

import (
	"context"
	"errors"
	"net"
	"time"

	"dsmapp/internal/apps"
	"github.com/synopackageland/syno-pkg-land-sdk/dsmservices"
)

// customState answers a running custom proxy's per-request check from the
// durable approval, like StorePinState does for pin proxies.
type customState struct{ store apps.Store }

func (s customState) Active(b apps.PinBinding) bool {
	state, err := s.store.Load()
	if err != nil {
		return false
	}
	_, ok := apps.CustomActive(state, b)
	return ok
}

// ensureCustom starts the proxy for one approved custom App scope. It is not
// in the pin registry: there is no certificate to recheck, and every request
// re-reads the approval and every dial goes through the target guard.
func (e *Engine) ensureCustom(b apps.PinBinding) error {
	state, err := e.cfg.Store.Load()
	if err != nil {
		return err
	}
	def, ok := apps.CustomActive(state, b)
	if !ok {
		return apps.ErrConfirmationChanged
	}
	cfg := apps.CustomConfig{Binding: b, App: def, State: customState{e.cfg.Store}, Guard: e.cfg.Guard, Authorize: e.accessAuthorizer(b)}
	if def.Ingress != nil {
		uid := def.Ingress.UID
		cfg.IngressDial = func(ctx context.Context, network, address string) (net.Conn, error) {
			if err := e.verifyIngressIdentity(def.Ingress.Package, uid); err != nil {
				return nil, err
			}
			if def.Ingress.Socket != "" {
				return apps.DialIngressUnix(ctx, uid, def.Ingress.Socket)
			}
			return apps.DialIngress(ctx, uid, address)
		}
	}
	if !def.AppSignIn {
		if e.cfg.Login == nil {
			return apps.ErrUnavailable
		}
		cfg.Gate = &apps.Gate{Login: e.cfg.Login}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ctx.Err() != nil {
		return e.ctx.Err()
	}
	if _, exists := e.live[b.Socket]; exists {
		return nil
	}
	p, err := apps.StartCustomProxy(e.ctx, cfg)
	if err != nil {
		return err
	}
	e.live[b.Socket] = running{b, p.Stop}
	e.wg.Add(1)
	go func() { defer e.wg.Done(); _ = p.Wait(); p.Stop() }()
	return nil
}

// validateCustom is validateLocal for a custom App: the definition still
// exists and still matches what was approved.
func (e *Engine) validateCustom(t apps.Target) error {
	state, err := e.cfg.Store.Load()
	if err != nil {
		return err
	}
	def, ok := state.Custom[t.Package]
	if !ok || t.Kind != apps.Public || def.Digest() != t.Scope.Binding.Summary || def.Port() != t.Scope.Binding.Port || t.Scope.Binding.Pin.Mode != string(apps.ModeCustom) {
		return apps.ErrConfirmationChanged
	}
	if def.Ingress != nil {
		return e.validateIngress(def, t.Scope)
	}
	return nil
}

// inspectCustom proposes a public scope for a custom App. There is no
// certificate pin: the approval binds the configured origin through the
// definition digest. DSM is still proposed, since sign-in goes through it.
func (e *Engine) inspectCustom(ctx context.Context, app apps.App, kind string, previous *apps.Scope) (apps.Approval, error) {
	if kind != apps.Public || app.Custom == nil {
		return apps.Approval{}, apps.ErrInvalid
	}
	state, err := e.cfg.Store.Load()
	if err != nil {
		return apps.Approval{}, err
	}
	def := state.Custom[app.Package]
	if def.Ingress != nil {
		if requested := apps.Hostname(ctx, app.Package); requested != "" && requested != def.Ingress.Hostname {
			return apps.Approval{}, apps.ErrInvalid
		}
		ctx = apps.WithHostnames(ctx, map[string]string{app.Package: def.Ingress.Hostname})
		if err := e.validateIngress(def, apps.Scope{Provider: apps.PublicProvider(ctx), Authority: def.Ingress.Hostname}); err != nil {
			return apps.Approval{}, err
		}
	}
	authority, dsmAuthority, err := e.authority(ctx, app.Package, kind)
	if err != nil {
		return apps.Approval{}, err
	}
	if previous != nil && (previous.Provider == apps.Workers) != (apps.PublicProvider(ctx) == apps.Workers) {
		return apps.Approval{}, apps.ErrInvalid
	}
	cert := apps.Certificate{Mode: string(apps.ModeCustom)}
	out := apps.Approval{Certificate: cert, Scope: apps.Scope{Provider: apps.PublicProvider(ctx), State: apps.Active, Authority: authority,
		Origin: "https://" + authority, Binding: apps.Binding{Port: app.Port, Summary: app.Summary, Pin: apps.Pin{Mode: string(apps.ModeCustom)}}}}
	if def.Ingress != nil {
		return out, nil
	}
	out.DSM, out.DSMTarget, err = e.dsmProposal(ctx, kind, dsmAuthority)
	return out, err
}

func (e *Engine) AddCustom(ctx context.Context, r apps.CustomRequest) (string, error) {
	var id string
	err := e.cfg.Store.Update(ctx, func(state *apps.State) error {
		var err error
		id, err = state.AddCustom(r)
		return err
	})
	return id, err
}

// EditCustom saves the new definition; an approved scope keeps its address
// and socket and its proxy is replaced, which also signs every visitor out.
func (e *Engine) EditCustom(ctx context.Context, r apps.CustomRequest) error {
	var old *apps.Scope
	err := e.cfg.Store.Update(ctx, func(state *apps.State) error {
		var err error
		old, err = state.EditCustom(r)
		return err
	})
	if err != nil || old == nil {
		return err
	}
	e.stop(old.Socket)
	state, err := e.cfg.Store.Load()
	if err != nil {
		return err
	}
	if old.State == apps.Active {
		if err = e.activateProxies(apps.Target{Package: r.ID, Kind: apps.Public}); err != nil {
			return err
		}
	}
	return apps.WriteProjection(e.cfg.ProjectionPath, state)
}

// RemoveCustom withdraws the route, then deletes the Worker or DNS record. If
// cloud cleanup fails, the removal intent stays and the safety check retries.
func (e *Engine) RemoveCustom(ctx context.Context, id string) error {
	var old *apps.Scope
	err := e.cfg.Store.Update(ctx, func(state *apps.State) error {
		var err error
		old, err = state.RemoveCustom(id)
		return err
	})
	if err != nil || old == nil {
		return err
	}
	t := apps.Target{Package: id, Kind: apps.Public, Scope: *old}
	_ = e.Block(ctx, t)
	state, err := e.cfg.Store.Load()
	if err != nil {
		return err
	}
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	err = errors.Join(apps.WriteProjection(e.cfg.ProjectionPath, state), e.Remove(cleanup, apps.Target{Kind: apps.Public}))
	if err == nil {
		err = e.cleanupRemoved(cleanup, apps.Target{Package: id, Kind: apps.Public})
	}
	if err != nil {
		return errors.Join(apps.ErrCleanup, err)
	}
	return e.cfg.Store.Update(ctx, func(state *apps.State) error {
		if scope := state.Apps[id].Public; scope != nil && scope.Reason == "removed" {
			scope.Cleanup = false
		}
		state.DropRemovedCustom()
		return nil
	})
}

// PauseCustom withdraws an approved custom App and keeps its approval and
// public name, so the ordinary reenable approval resumes it.
func (e *Engine) PauseCustom(ctx context.Context, id string) error {
	state, err := e.cfg.Store.Load()
	if err != nil {
		return err
	}
	if _, ok := state.Custom[id]; !ok {
		return apps.ErrCustomMissing()
	}
	scope := state.Apps[id].Public
	if scope == nil || scope.State != apps.Active {
		return apps.ErrInvalid
	}
	if err = e.PausePublic(ctx, []apps.Target{{Package: id, Kind: apps.Public, Scope: *scope}}, "admin_paused"); err != nil {
		return err
	}
	return e.Remove(ctx, apps.Target{Kind: apps.Public})
}

func (e *Engine) CustomCandidates(ctx context.Context) ([]apps.Candidate, error) {
	var services []dsmservices.Definition
	if e.cfg.Services != nil {
		services = e.cfg.Services(ctx)
	}
	exclude := map[int]bool{}
	if e.cfg.Guard.OwnPorts != nil {
		exclude = e.cfg.Guard.OwnPorts()
	}
	return e.cfg.Discovery.CustomCandidates(ctx, services, exclude), nil
}
