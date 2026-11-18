package appaccess

import (
	"context"
	"dsmapp/internal/apps"
	"dsmapp/internal/connector"
	"errors"
	"strings"
	"time"
)

func (e *Engine) WorkersStatus(ctx context.Context) (apps.WorkersStatus, error) {
	if e.cfg.Workers == nil {
		return apps.WorkersStatus{Code: "workers_unavailable"}, nil
	}
	e.followWorkers(ctx)
	out, err := e.cfg.Workers.Status(ctx)
	return e.workersView(out), err
}

// WorkersConnect turns the Free link on and, the first time, publishes DSM on
// it straight away. A refused DSM leaves the Free link on and says why. A
// token saved only for the NetBird server (NoDSM) never publishes DSM.
func (e *Engine) WorkersConnect(ctx context.Context, in apps.WorkersConnectRequest) (apps.WorkersStatus, error) {
	if e.cfg.Workers == nil {
		return apps.WorkersStatus{}, apps.ErrUnavailable
	}
	if !in.NoDSM {
		if err := e.prepareAccess(ctx, apps.Workers); err != nil {
			return apps.WorkersStatus{}, err
		}
	}
	before, err := e.cfg.Workers.Status(ctx)
	if err != nil {
		return apps.WorkersStatus{}, err
	}
	out, err := e.cfg.Workers.Connect(ctx, in)
	if err != nil || !out.Connected {
		return out, err
	}
	if sub := out.Subdomain; sub != "" {
		if err := e.moveWorkers(ctx, sub); err != nil {
			connector.Logf("workers.dev rename not applied: %s", apps.ErrorCode(err))
		}
	}
	if !before.Connected && !in.NoDSM {
		if err := e.publishWorkersDSM(ctx); err != nil {
			out.DSMCode = apps.ErrorCode(err)
		}
	}
	return e.workersView(out), nil
}

// WorkersPublishDSM is the one-click Add DSM for a Free link that is already on.
func (e *Engine) WorkersPublishDSM(ctx context.Context) (apps.WorkersStatus, error) {
	if e.cfg.Workers == nil {
		return apps.WorkersStatus{}, apps.ErrUnavailable
	}
	if err := e.publishWorkersDSM(ctx); err != nil {
		return apps.WorkersStatus{}, err
	}
	out, err := e.cfg.Workers.Status(ctx)
	return e.workersView(out), err
}

// publishWorkersDSM routes DSM through the Free link's own Worker. DSM signs
// people in itself, so nothing of Connect's stands in front of it, and the
// trust is the NAS's certificate recorded on first use, as the own domain
// does, not a review. DSM is public on one provider at a time: one the own
// domain serves, or own-domain Apps still hold, stays where it is.
func (e *Engine) publishWorkersDSM(ctx context.Context) error {
	if err := e.prepareAccess(ctx, apps.Workers); err != nil {
		return err
	}
	ctx = apps.WithPublicProvider(ctx, apps.Workers)
	state, err := e.cfg.Store.Load()
	if err != nil {
		return err
	}
	if d := state.DSM; d != nil && d.Public != nil {
		if d.Public.Provider == apps.Workers {
			return e.RefreshDSM(ctx, apps.Public)
		}
		return dsmElsewhere()
	}
	if e.publicMode(ctx) != nil {
		return dsmElsewhere()
	}
	if err = e.TrustDSMFirstUse(ctx); err != nil {
		return err
	}
	authority, err := e.cfg.Workers.Authority(ctx, "")
	if err != nil {
		return err
	}
	if err = e.cfg.Workers.Ensure(ctx, []string{""}); err != nil {
		return err
	}
	err = e.cfg.Store.Update(ctx, func(s *apps.State) error {
		d := s.DSM
		if d == nil {
			return connector.Coded("pin_proxy_required", "DSM certificate review required")
		}
		if d.Public != nil {
			if d.Public.Provider == apps.Workers && d.Public.Authority == authority {
				return nil
			}
			return dsmElsewhere()
		}
		if err := e.verifyRetainedDSM(ctx, d, apps.Public, authority); err != nil {
			return err
		}
		id, err := socketID()
		if err != nil {
			return err
		}
		d.Public = &apps.Endpoint{Provider: apps.Workers, Socket: id, Authority: authority}
		d.Origin = "https://" + authority
		_, err = s.Revision()
		return err
	})
	if err != nil {
		return err
	}
	if err = e.activateProxies(apps.Target{Kind: apps.Public}); err != nil {
		return err
	}
	e.workersAddress()
	if state, err = e.cfg.Store.Load(); err != nil {
		return err
	}
	return errors.Join(apps.WriteProjection(e.cfg.ProjectionPath, state), e.Remove(ctx, apps.Target{Kind: apps.Public}))
}

// workersAddress tells the connection check that DSM's Free link address is new.
func (e *Engine) workersAddress() {
	if e.cfg.WorkersAddress != nil {
		e.cfg.WorkersAddress()
	}
}

func dsmElsewhere() error {
	return connector.Coded("apps_dsm_public_elsewhere", "DSM is public on the own domain; it is public on one way at a time")
}

// workersView adds where public DSM opens, so the Free link tab can list it
// or name the own domain that serves it.
func (e *Engine) workersView(out apps.WorkersStatus) apps.WorkersStatus {
	state, err := e.cfg.Store.Load()
	if err != nil {
		return out
	}
	out.Usage = &apps.PublicUsage{Relay: out.Connected}
	if e.cfg.Public != nil {
		if d, err := e.cfg.Public.AppDomain(); err == nil {
			out.Usage.Public = d.Enabled
		}
	}
	for _, a := range state.Apps {
		if a.Public != nil {
			if a.Public.Provider == apps.Workers {
				out.Usage.Relay = true
			} else {
				out.Usage.Public = true
			}
		}
	}
	if state.DSM == nil || state.DSM.Public == nil {
		return out
	}
	provider := "cloudflare"
	if state.DSM.Public.Provider == apps.Workers {
		provider = apps.Workers
	}
	if provider == apps.Workers {
		out.Usage.Relay = true
	} else {
		out.Usage.Public = true
	}
	out.DSM = &apps.WorkersDSM{Provider: provider, URL: state.DSM.Origin}
	return out
}

// WorkersDSMURL is where public DSM opens on the Free link, or "" while DSM
// is not public there. The Free link's connection check opens it.
func (e *Engine) WorkersDSMURL() string {
	state, err := e.cfg.Store.Load()
	if err != nil || state.DSM == nil || state.DSM.Public == nil || state.DSM.Public.Provider != apps.Workers {
		return ""
	}
	return state.DSM.Origin
}

// followWorkers picks up a workers.dev rename made in Cloudflare and moves
// every saved Free link address to it. It runs on a page's read only.
func (e *Engine) followWorkers(ctx context.Context) {
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	sub, _ := e.cfg.Workers.Follow(bounded)
	if sub == "" {
		return
	}
	if err := e.moveWorkers(ctx, sub); err != nil {
		connector.Logf("workers.dev rename not applied: %s", apps.ErrorCode(err))
	}
}

// moveWorkers rewrites each Free link address still on another workers.dev
// name to <worker>.<sub>.workers.dev. Worker names, certificates and
// approvals stay; each moved route gets a new socket, so its pin proxy and
// relay stream restart on the new address. Nothing changes when all match,
// so a failed move is retried by the next read.
func (e *Engine) moveWorkers(ctx context.Context, sub string) error {
	suffix := "." + sub + ".workers.dev"
	moved := func(authority string) (string, bool) {
		label, _, ok := strings.Cut(authority, ".")
		if !ok || !strings.HasSuffix(authority, ".workers.dev") || authority == label+suffix {
			return authority, false
		}
		return label + suffix, true
	}
	var targets []apps.Target
	var old []string
	dsm := false
	err := e.cfg.Store.Update(ctx, func(s *apps.State) error {
		targets, old, dsm = nil, nil, false
		for pkg, a := range s.Apps {
			sc := a.Public
			if sc == nil || sc.Provider != apps.Workers {
				continue
			}
			authority, ok := moved(sc.Authority)
			if !ok {
				continue
			}
			id, err := socketID()
			if err != nil {
				return err
			}
			rev, err := s.Revision()
			if err != nil {
				return err
			}
			scope := *sc
			old = append(old, scope.Socket)
			scope.Socket, scope.Authority, scope.Origin = id, authority, "https://"+authority
			a.Rev, a.Public = rev, &scope
			s.Apps[pkg] = a
			targets = append(targets, apps.Target{Package: pkg, Kind: apps.Public, Scope: scope})
		}
		if d := s.DSM; d != nil && d.Public != nil && d.Public.Provider == apps.Workers {
			if authority, ok := moved(d.Public.Authority); ok {
				id, err := socketID()
				if err != nil {
					return err
				}
				ep := *d.Public
				old = append(old, ep.Socket)
				ep.Socket, ep.Authority = id, authority
				d.Public, d.Origin, dsm = &ep, "https://"+authority, true
				if len(targets) == 0 {
					_, err = s.Revision()
					return err
				}
			}
		}
		return nil
	})
	if err != nil || len(targets) == 0 && !dsm {
		return err
	}
	// A route that was serving keeps serving; a paused one stays paused.
	e.mu.Lock()
	wasAllowed := map[string]bool{}
	for _, id := range old {
		wasAllowed[id] = e.allowed[id]
	}
	e.mu.Unlock()
	var errs []error
	for i, t := range targets {
		e.stop(old[i])
		if wasAllowed[old[i]] && t.Scope.State == apps.Active {
			errs = append(errs, e.activateProxies(t))
		}
	}
	if dsm {
		id := old[len(old)-1]
		allowed := wasAllowed[id]
		e.stop(id)
		if allowed {
			errs = append(errs, e.activateProxies(apps.Target{Kind: apps.Public}))
		}
		e.workersAddress()
	}
	state, err := e.cfg.Store.Load()
	if err != nil {
		return errors.Join(append(errs, err)...)
	}
	errs = append(errs, apps.WriteProjection(e.cfg.ProjectionPath, state), e.cfg.Workers.Reconcile(e.ctx, e.workerBindings()))
	return errors.Join(errs...)
}
func (e *Engine) workerBindings() []apps.PinBinding {
	state, err := e.cfg.Store.Load()
	if err != nil {
		return nil
	}
	dsmAllowed := false
	if d := state.DSM; d != nil && d.Public != nil && d.Public.Provider == apps.Workers {
		e.mu.Lock()
		dsmAllowed = e.allowed[d.Public.Socket]
		e.mu.Unlock()
	}
	out := []apps.PinBinding{}
	for _, b := range apps.PinBindings(state) {
		// Native App SSO needs its approved DSM origin; custom Apps use
		// their own sign-in or the local DSM gate, without that origin.
		if !dsmAllowed && !apps.IsCustom(b.Package) {
			continue
		}
		e.mu.Lock()
		allowed := e.allowed[b.Socket]
		e.mu.Unlock()
		if allowed && b.Scope == apps.Public && b.Provider == apps.Workers {
			out = append(out, b)
		}
	}
	return out
}

// Disconnect persists withdrawal before closing local streams and deleting any
// remote resource. Failed cloud cleanup keeps the private token and inventory.
func (e *Engine) WorkersDisconnect(ctx context.Context) error {
	if e.cfg.Workers == nil {
		return apps.ErrUnavailable
	}
	var targets []apps.Target
	var dsmSocket string
	err := e.cfg.Store.Update(ctx, func(state *apps.State) error {
		for pkg, a := range state.Apps {
			if a.Public == nil || a.Public.Provider != apps.Workers {
				continue
			}
			targets = append(targets, apps.Target{Package: pkg, Kind: apps.Public, Scope: *a.Public})
			rev, err := state.Revision()
			if err != nil {
				return err
			}
			a.Rev = rev
			a.Public = nil
			if a.Private == nil {
				delete(state.Apps, pkg)
			} else {
				state.Apps[pkg] = a
			}
		}
		if state.DSM != nil && state.DSM.Public != nil && state.DSM.Public.Provider == apps.Workers {
			dsmSocket = state.DSM.Public.Socket
			state.DSM.Public = nil
			state.DSM.Origin = ""
			_, err := state.Revision()
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, t := range targets {
		_ = e.Block(ctx, t)
	}
	if dsmSocket != "" {
		e.stop(dsmSocket)
	}
	state, err := e.cfg.Store.Load()
	if err != nil {
		return err
	}
	projectErr := apps.WriteProjection(e.cfg.ProjectionPath, state)
	stopErr := e.cfg.Workers.Reconcile(e.ctx, e.workerBindings())
	cloudErr := e.cfg.Workers.Disconnect(ctx)
	return errors.Join(projectErr, stopErr, cloudErr)
}

// Schema 1 has one DSM origin. A public mode cannot borrow the approval or
// DSM sign-in origin of the other mode, even while an App is paused.
func (e *Engine) publicMode(ctx context.Context) error {
	state, err := e.cfg.Store.Load()
	if err != nil {
		return err
	}
	mode := apps.ProviderMode(apps.PublicProvider(ctx))
	if state.DSM != nil && state.DSM.Public != nil && apps.ProviderMode(state.DSM.Public.Provider) != mode {
		return connector.Coded("apps_public_mode_conflict", "Clear the current public App and DSM approvals before switching public mode")
	}
	for _, a := range state.Apps {
		if a.Public != nil && apps.ProviderMode(a.Public.Provider) != mode {
			return connector.Coded("apps_public_mode_conflict", "Clear the current public App and DSM approvals before switching public mode")
		}
	}
	return nil
}

func (e *Engine) ClearDSM(ctx context.Context, in apps.DSMInspectRequest) error {
	if in.Scope != apps.Public {
		return apps.ErrInvalid
	}
	var socket string
	err := e.cfg.Store.Update(ctx, func(state *apps.State) error {
		for _, a := range state.Apps {
			if a.Public != nil {
				return connector.Coded("apps_public_apps_remaining", "Clear public App approvals before clearing the public DSM approval")
			}
		}
		if state.DSM == nil || state.DSM.Public == nil {
			return nil
		}
		if in.Provider != "" && apps.ProviderMode(state.DSM.Public.Provider) != apps.ProviderMode(in.Provider) {
			return apps.ErrConfirmationChanged
		}
		socket = state.DSM.Public.Socket
		state.DSM.Public = nil
		state.DSM.Origin = ""
		_, err := state.Revision()
		return err
	})
	if err != nil {
		return err
	}
	if socket != "" {
		e.stop(socket)
	}
	state, err := e.cfg.Store.Load()
	if err != nil {
		return err
	}
	projectErr := apps.WriteProjection(e.cfg.ProjectionPath, state)
	removeErr := e.Remove(ctx, apps.Target{Kind: apps.Public})
	if connector.ErrorCode(removeErr) == "pin_proxy_required" {
		removeErr = nil
	}
	return errors.Join(projectErr, removeErr)
}

func (e *Engine) domainPublicMode() bool {
	state, err := e.cfg.Store.Load()
	if err != nil {
		return true
	}
	if state.DSM != nil && state.DSM.Public != nil && state.DSM.Public.Provider == apps.Workers {
		return false
	}
	for _, a := range state.Apps {
		if a.Public != nil && a.Public.Provider == apps.Workers {
			return false
		}
	}
	return true
}

// ClearWorker deletes only this App's owned Worker. A cloud failure leaves a
// blocked, retryable row and the exact inventory/credential needed for cleanup.
func (e *Engine) ClearWorker(ctx context.Context, pkg string, previous *apps.Scope) error {
	if e.cfg.Workers == nil {
		return nil
	}
	err := e.cfg.Workers.DeleteApp(ctx, pkg)
	if err == nil || previous == nil || previous.Provider != apps.Workers {
		return err
	}
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	persistErr := e.cfg.Store.Update(persistCtx, func(state *apps.State) error {
		a := state.Apps[pkg]
		if a.Public != nil {
			return nil
		}
		scope := *previous
		scope.State = apps.Paused
		scope.Reason = "worker-delete-failed"
		scope.Cleanup = true
		rev, err := state.Revision()
		if err != nil {
			return err
		}
		a.Rev = rev
		a.Public = &scope
		state.Apps[pkg] = a
		return nil
	})
	state, loadErr := e.cfg.Store.Load()
	var projectErr error
	if loadErr == nil {
		projectErr = apps.WriteProjection(e.cfg.ProjectionPath, state)
	}
	return errors.Join(err, persistErr, loadErr, projectErr)
}
