package apps

import (
	"context"
	"errors"
	"sort"
	"time"
)

const cleanupTimeout = 10 * time.Second

// Target identifies one approved socket, never a provider-supplied mapping.
type Target struct {
	AdditionalPaths []string `json:"additionalPaths,omitempty"`
	Package         string   `json:"package"`
	Path            string   `json:"path,omitempty"`
	Kind            string   `json:"kind"`
	Scope           Scope    `json:"scope"`
}

// Runtime is implemented by the daemon's pin proxy/provider owner. Start must
// enforce the supplied state before accepting requests. Block closes existing
// streams as well as refusing new requests. Prune removes all orphan routes,
// including revoked scopes whose records have already been deleted.
// Validate rechecks metadata, readiness, authority, pin and ping. It never grants.
type Runtime interface {
	Start(context.Context, State) error
	Validate(context.Context, Target) error
	PrepareDNS(context.Context, Target) error
	Activate(context.Context, Target) error
	Block(context.Context, Target) error
	Remove(context.Context, Target) error
	Prune(context.Context, State) error
}

type Lifecycle struct {
	Store          Store
	ProjectionPath string
	Runtime        Runtime
	// step is only a test seam: subprocesses die between persisted operations.
	step func(string)
}

func (l Lifecycle) mark(step string) {
	if l.step != nil {
		l.step(step)
	}
}
func (l Lifecycle) project(state State) error {
	if l.ProjectionPath == "" {
		return ErrUnavailable
	}
	return WriteProjection(l.ProjectionPath, state)
}

// Open is called only after an explicit administrator confirmation. The caller
// supplies a server-derived, freshly checked scope, not browser-selected pins.
// Reenable reuses DNS and must preserve the old pin mode unless separately
// confirmed by the runtime's approval flow.
func (l Lifecycle) Open(ctx context.Context, target Target, reenable bool) error {
	return l.OpenWithDSM(ctx, target, reenable, nil)
}

// OpenWithDSM persists the first DSM scope in the same durable approval as the App.
func (l Lifecycle) OpenWithDSM(ctx context.Context, target Target, reenable bool, dsm *DSMState, expectedRevision ...uint64) error {
	if l.Runtime == nil || (target.Kind != Public && target.Kind != Private) || target.Scope.State != Active {
		return ErrUnavailable
	}
	return l.Store.locked(ctx, func(t *transaction, loadErr error) error {
		if loadErr != nil {
			return loadErr
		}
		if len(expectedRevision) > 0 && expectedRevision[0] != t.state.NextRev {
			return ErrConfirmationChanged
		}
		old := t.state.Apps[target.Package].Scope(target.Kind)
		if reenable && (old == nil || old.State != Paused || old.Binding.Pin.Mode != target.Scope.Binding.Pin.Mode) {
			return ErrInvalid
		}
		if old != nil && old.State == Active {
			return ErrInvalid
		}
		// Validate the complete candidate before creating even a DNS record.
		app := t.state.Apps[target.Package]
		if dsm != nil {
			if validator, ok := l.Runtime.(interface {
				ValidateDSM(context.Context, *DSMState) error
			}); ok {
				if err := validator.ValidateDSM(ctx, dsm); err != nil {
					return err
				}
			} else {
				return ErrUnavailable
			}
			t.state.DSM = dsm
		}
		rev, err := t.state.Revision()
		if err != nil {
			return err
		}
		app.Rev = rev
		candidate := target.Scope
		candidate.Cleanup = false
		app.SetScope(target.Kind, &candidate)
		t.state.Apps[target.Package] = app
		if err = t.state.Validate(); err != nil {
			return err
		}
		if err = l.Runtime.Validate(ctx, target); err != nil {
			return err
		}
		if target.Kind == Public && !reenable {
			if err = l.Runtime.PrepareDNS(ctx, target); err != nil {
				return err
			}
		}
		l.mark("open-dns")
		if err = t.save(); err != nil {
			return err
		}
		l.mark("open-state")
		if err = l.Runtime.Activate(ctx, target); err != nil {
			// Record failure before removing any partially installed runtime.
			candidate.State = Paused
			candidate.Reason = "activation-failed"
			candidate.Cleanup = true
			rev, revErr := t.state.Revision()
			if revErr != nil {
				return revErr
			}
			app.Rev = rev
			t.state.Apps[target.Package] = app
			saveErr := t.save()
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
			defer cancel()
			blockErr := l.Runtime.Block(cleanupCtx, target)
			removeErr := l.Runtime.Remove(cleanupCtx, target)
			if saveErr == nil && blockErr == nil && removeErr == nil {
				candidate.Cleanup = false
				saveErr = t.save()
			}
			projectErr := l.project(t.state)
			return errors.Join(err, saveErr, blockErr, removeErr, projectErr)
		}
		l.mark("open-runtime")
		if err = l.project(t.state); err != nil {
			return err
		}
		l.mark("open-projection")
		return nil
	})
}

// Close persists revocation (empty reason) or a pause (nonempty reason) before
// touching the proxy or providers. Failed provider cleanup is safe to retry.
func (l Lifecycle) Close(ctx context.Context, pkg, kind, reason string) error {
	if l.Runtime == nil || (kind != Public && kind != Private) {
		return ErrUnavailable
	}
	return l.Store.locked(ctx, func(t *transaction, loadErr error) error {
		if loadErr != nil {
			return loadErr
		}
		app := t.state.Apps[pkg]
		scope := app.Scope(kind)
		if scope == nil {
			return nil
		}
		if kind == Public {
			t.state.RememberCustomPublication(pkg, scope)
		}
		target := Target{Package: pkg, Kind: kind, Scope: *scope}
		rev, err := t.state.Revision()
		if err != nil {
			return err
		}
		app.Rev = rev
		if reason == "" {
			app.SetScope(kind, nil)
		} else {
			scope.State = Paused
			scope.Reason = reason
			scope.Cleanup = true
		}
		if app.Public == nil && app.Private == nil {
			delete(t.state.Apps, pkg)
		} else {
			t.state.Apps[pkg] = app
		}
		if err = t.save(); err != nil {
			return err
		}
		l.mark("close-state")
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer cancel()
		blockErr := l.Runtime.Block(cleanupCtx, target)
		l.mark("close-block")
		projectErr := l.project(t.state)
		l.mark("close-projection")
		removeErr := l.Runtime.Remove(cleanupCtx, target)
		l.mark("close-provider")
		if reason != "" && blockErr == nil && removeErr == nil {
			scope.Cleanup = false
			if err = t.save(); err != nil {
				return err
			}
		}
		if blockErr != nil || removeErr != nil {
			return errors.Join(ErrCleanup, blockErr, removeErr, projectErr)
		}
		return projectErr
	})
}

// Recover always reconstructs the projection and reconciles from state alone.
// Corruption starts an empty proxy and prunes every route, but is never saved
// over: revision history needs an explicit recovery decision.
func (l Lifecycle) Recover(ctx context.Context) error {
	if l.Runtime == nil {
		return ErrUnavailable
	}
	var restored State
	ready := false
	err := l.Store.locked(ctx, func(t *transaction, loadErr error) error {
		l.mark("startup-read")
		if err := l.Runtime.Start(ctx, t.state); err != nil {
			return err
		}
		l.mark("startup-proxy")
		valid := EmptyState()
		valid.NextRev = t.state.NextRev
		valid.DSM = t.state.DSM
		valid.Custom = t.state.Custom
		var result error
		for _, target := range Targets(t.state) {
			if target.Scope.State != Active {
				continue
			}
			if err := l.Runtime.Validate(ctx, target); err != nil {
				continue
			}
			if err := l.Runtime.Activate(ctx, target); err != nil {
				app := t.state.Apps[target.Package]
				scope := app.Scope(target.Kind)
				scope.State = Paused
				scope.Reason = "activation-failed"
				scope.Cleanup = true
				rev, err := t.state.Revision()
				if err != nil {
					return err
				}
				app.Rev = rev
				t.state.Apps[target.Package] = app
				if err = t.save(); err != nil {
					return err
				}
				result = errors.Join(result, l.Runtime.Block(ctx, target), l.Runtime.Remove(ctx, target))
				continue
			}
			app := valid.Apps[target.Package]
			app.Rev = t.state.Apps[target.Package].Rev
			scope := target.Scope
			app.SetScope(target.Kind, &scope)
			valid.Apps[target.Package] = app
		}
		valid.NextRev = t.state.NextRev
		l.mark("startup-routes")
		restored, ready = valid, true
		return errors.Join(loadErr, result)
	})
	if !ready {
		return err
	}
	// Provider reconciliation may bind retained DSM trust through Store.Update.
	// It must run after releasing the approval lock; otherwise Cloudflare and
	// Tailscale wait on their own recovery transaction until the callback expires.
	pruneErr := l.Runtime.Prune(ctx, restored)
	l.mark("startup-prune")
	// Re-read after callbacks so the projection includes any restored endpoint.
	projectErr := l.Store.locked(ctx, func(t *transaction, loadErr error) error {
		return errors.Join(loadErr, l.project(t.state))
	})
	l.mark("startup-projection")
	return errors.Join(err, pruneErr, projectErr)
}

// Targets has deterministic order for provider batches and recovery.
func Targets(state State) []Target {
	var out []Target
	keys := make([]string, 0, len(state.Apps))
	for pkg := range state.Apps {
		keys = append(keys, pkg)
	}
	sort.Strings(keys)
	for _, pkg := range keys {
		app := state.Apps[pkg]
		for _, kind := range []string{Public, Private} {
			if scope := app.Scope(kind); scope != nil {
				out = append(out, Target{Package: pkg, Kind: kind, Scope: *scope})
			}
		}
	}
	return out
}
