package appaccess

import (
	"context"
	"dsmapp/internal/apps"
	"dsmapp/internal/cfaccess"
	"dsmapp/internal/connector"
	"slices"
	"sort"
)

func (e *Engine) AccessGet(context.Context) (cfaccess.Status, error) {
	if e.cfg.Access == nil {
		return cfaccess.Status{}, apps.ErrUnavailable
	}
	return e.cfg.Access.Status(), nil
}
func (e *Engine) AccessApply(ctx context.Context, in apps.AccessRequest) (cfaccess.Status, error) {
	if e.cfg.Access == nil {
		return cfaccess.Status{}, apps.ErrUnavailable
	}
	return e.cfg.Access.Submit(in.Request)
}
func (e *Engine) AccessSync(ctx context.Context) (cfaccess.Status, error) {
	if e.cfg.Access == nil {
		return cfaccess.Status{}, apps.ErrUnavailable
	}
	return e.cfg.Access.SubmitSync()
}
func (e *Engine) AccessToken(ctx context.Context, in apps.AccessTokenRequest) (cfaccess.Status, error) {
	if e.cfg.Access == nil || e.cfg.Workers == nil {
		return cfaccess.Status{}, apps.ErrUnavailable
	}
	if err := e.cfg.Workers.SaveAccountToken(ctx, in.Token, e.cfg.Access.ValidateToken); err != nil {
		return cfaccess.Status{}, err
	}
	return e.AccessSync(ctx)
}

// AccessInventory includes retained approvals, even while a provider is stopped.
// Cloudflare adds routed DNS names so paused routes keep their gate until the
// route owner has actually withdrawn them. Workers/Quick cannot be half-covered.
func (e *Engine) AccessInventory() (cfaccess.Inventory, error) {
	state, err := e.cfg.Store.Load()
	if err != nil {
		return cfaccess.Inventory{}, err
	}
	out := cfaccess.Inventory{}
	add := func(provider, host string) {
		if provider == apps.Workers {
			out.Unsupported = true
		} else if host != "" {
			out.Hosts = append(out.Hosts, host)
		}
	}
	if state.DSM != nil && state.DSM.Public != nil {
		add(state.DSM.Public.Provider, state.DSM.Public.Authority)
	}
	for pkg, app := range state.Apps {
		if def := state.Custom[pkg]; def.Ingress != nil && app.Public != nil && app.Public.State == apps.Active {
			out.Unsupported = true
		}
		if app.Public != nil && app.Public.Reason != "removed" {
			add(app.Public.Provider, app.Public.Authority)
		}
	}
	sort.Strings(out.Hosts)
	out.Hosts = slices.Compact(out.Hosts)
	return out, nil
}
func (e *Engine) StopPublicStreams() {
	e.mu.Lock()
	ids := []string{}
	for id, r := range e.live {
		if r.binding.Scope == apps.Public {
			ids = append(ids, id)
		}
	}
	e.mu.Unlock()
	for _, id := range ids {
		e.stop(id)
	}
}
func (e *Engine) prepareAccess(ctx context.Context, provider string, hosts ...string) error {
	if e.cfg.Access == nil || !e.cfg.Access.Enabled() {
		return nil
	}
	if provider == apps.Workers {
		return connector.Coded("access_alias_unsupported", "email restrictions do not support this public connection")
	}
	return e.cfg.Access.Prepare(ctx, hosts)
}
