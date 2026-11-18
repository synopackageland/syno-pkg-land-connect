package apps

import (
	"context"
	"errors"
	"reflect"
)

// FollowEntries only replaces active package targets with the same approved
// protection and a target that still verifies against the saved pin. Nothing
// here grants new certificate trust, changes a hostname or adopts custom URLs.
func FollowEntries(ctx context.Context, l Lifecycle, d Discovery) error {
	if l.Runtime == nil {
		return nil
	}
	state, err := l.Store.Load()
	if err != nil {
		return err
	}
	var errs []error
	for _, t := range Targets(state) {
		if IsCustom(t.Package) || t.Scope.State != Active {
			continue
		}
		g, err := d.ReadGateway(t.Package)
		if err != nil {
			continue
		} // upgrade in progress
		port := g.Listen.HTTPS
		if g.IngressPath != "" {
			if d.DetectPorts == nil {
				continue
			}
			_, port, err = d.DetectPorts(ctx)
			if err != nil || !validPort(port) {
				continue
			}
		}
		next := PackageEntry{Package: t.Package, Port: port, Path: g.IngressPath}
		protection, summary := ProtectionOf(g)
		old := t.Scope.Binding
		if old.Entry != nil && *old.Entry == next && old.Summary == summary {
			continue
		}
		if old.Entry == nil && old.Port == port && next.Path == "" && old.Summary == summary {
			continue
		}
		candidate := t
		candidate.Scope.Binding = Binding{Protection: &protection, Entry: &next, Port: port, Summary: summary, Pin: old.Pin}
		reason := ""
		if old.Entry == nil || summary != old.Summary || g.External.Connect == "none" {
			reason = "protection_changed"
		}
		if reason == "" {
			if err = l.Runtime.Validate(ctx, candidate); err != nil {
				reason = "entry_changed"
			}
		}
		updated := false
		err = l.Store.Update(ctx, func(current *State) error {
			app := current.Apps[t.Package]
			saved := app.Scope(t.Kind)
			if app.Rev != state.Apps[t.Package].Rev || !reflect.DeepEqual(saved, &t.Scope) {
				return ErrConfirmationChanged
			}
			rev, e := current.Revision()
			if e != nil {
				return e
			}
			if reason != "" {
				saved.State = Paused
				saved.Reason = reason
				saved.Cleanup = false
			} else {
				before := PackageEntry{Package: t.Package, Port: old.Port}
				if old.Entry != nil {
					before = *old.Entry
				}
				saved.Binding = candidate.Scope.Binding
				current.EntryActivity = append(current.EntryActivity, EntryActivity{Package: t.Package, Scope: t.Kind, Before: before, After: next, Revision: rev})
				if len(current.EntryActivity) > 64 {
					current.EntryActivity = current.EntryActivity[len(current.EntryActivity)-64:]
				}
			}
			app.Rev = rev
			current.Apps[t.Package] = app
			updated = true
			return nil
		})
		if errors.Is(err, ErrConfirmationChanged) {
			continue
		}
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if updated {
			_ = l.Runtime.Block(ctx, t)
			fresh, e := l.Store.Load()
			if e == nil {
				e = WriteProjection(l.ProjectionPath, fresh)
			}
			if e == nil && reason == "" {
				e = l.Runtime.Activate(ctx, candidate)
			}
			errs = append(errs, e)
			state, _ = l.Store.Load()
		}
	}
	return errors.Join(errs...)
}
