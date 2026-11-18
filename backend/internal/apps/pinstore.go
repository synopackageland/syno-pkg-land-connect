package apps

import (
	"context"
	"slices"
)

// StorePinState connects the proxy directly to the approval store. The adapter
// never reconstructs a pin or a name set from a provider or a live socket.
// Cleanup removes provider routes and confirmed-uninstall resources only after
// the proxy has closed traffic.
type StorePinState struct {
	Store          Store
	ProjectionPath string
	Cleanup        func(context.Context, PinBinding) error
}

func pinMatches(b PinBinding, p Pin) bool {
	return string(b.Mode) == p.Mode && b.SPKI == p.SPKI && slices.Equal(b.Names, p.Names)
}
func (s StorePinState) matches(state State, b PinBinding) bool {
	if b.Scope != Private && b.Scope != Public {
		return false
	}
	if b.Target == TargetDSM {
		d := state.DSM
		if d == nil || d.Port != b.Port || !pinMatches(b, d.Pin) || b.CertSHA256 != d.Pin.CertSHA256 {
			return false
		}
		ep := d.Public
		if b.Scope == Private {
			ep = d.Private
		}
		return ep != nil && ep.Socket == b.Socket && ep.Authority == b.Authority
	}
	sc := state.Apps[b.Package].Scope(b.Scope)
	return sc != nil && sc.State == Active && sc.Socket == b.Socket && sc.Authority == b.Authority && sc.Binding.Port == b.Port && EntryPath(sc.Binding.Entry) == b.EntryPath && sc.Binding.Summary == b.Summary && pinMatches(b, sc.Binding.Pin)
}
func (s StorePinState) Active(b PinBinding) bool {
	state, err := s.Store.Load()
	return err == nil && s.matches(state, b)
}

func (s StorePinState) Suspend(ctx context.Context, b PinBinding, reason string, removed bool) error {
	return s.Store.Update(ctx, func(state *State) error {
		if !s.matches(*state, b) {
			return nil
		} // Never pause a replacement approval.
		if b.Target == TargetDSM {
			// The DSM schema models endpoint presence rather than a paused flag.
			// Removing only this endpoint is durable revocation; approval is required
			// to create it again. Keep the other scope and the confirmed pin intact.
			if b.Scope == Public {
				state.DSM.Public = nil
				state.DSM.Origin = ""
			} else {
				state.DSM.Private = nil
			}
			_, err := state.Revision()
			return err
		}
		a := state.Apps[b.Package]
		sc := a.Scope(b.Scope)
		sc.State, sc.Reason, sc.Cleanup = Paused, reason, true
		if removed {
			sc.Reason = "removed"
		}
		rev, err := state.Revision()
		if err != nil {
			return err
		}
		a.Rev = rev
		state.Apps[b.Package] = a
		return nil
	})
}

// AfterBlock runs only after the durable refusal and connection closure. Failed
// cleanup retains Cleanup=true for the core's next reconciliation; it never
// restores forwarding. A removed App is represented as paused/reason=removed.
func (s StorePinState) AfterBlock(ctx context.Context, b PinBinding) error {
	state, err := s.Store.Load()
	if err != nil {
		return err
	}
	if s.ProjectionPath == "" {
		return ErrUnavailable
	}
	if err = WriteProjection(s.ProjectionPath, state); err != nil {
		return err
	}
	if s.Cleanup == nil {
		return ErrCleanup
	}
	if err = s.Cleanup(ctx, b); err != nil {
		return err
	}
	if b.Target == TargetDSM {
		return nil
	}
	return s.Store.Update(ctx, func(state *State) error {
		a := state.Apps[b.Package]
		sc := a.Scope(b.Scope)
		if sc != nil && sc.Socket == b.Socket && sc.State == Paused {
			sc.Cleanup = false
			state.Apps[b.Package] = a
		}
		return nil
	})
}

// PinBindings is a detached snapshot of active targets, one per scope/socket.
// App revisions are response/resume-token revisions, not pin identity: changing
// one scope must not shut down an unchanged approval in the other scope.
func PinBindings(state State) []PinBinding {
	if state.Validate() != nil {
		return nil
	}
	var out []PinBinding
	for _, target := range Targets(state) {
		sc := target.Scope
		if sc.State != Active {
			continue
		}
		out = append(out, PinBinding{EntryPath: EntryPath(sc.Binding.Entry), Provider: sc.Provider, Socket: sc.Socket, Scope: target.Kind, Revision: state.Apps[target.Package].Rev, Summary: sc.Binding.Summary, Package: target.Package, Target: TargetApp, Port: sc.Binding.Port, Authority: sc.Authority, Mode: Mode(sc.Binding.Pin.Mode), Names: slices.Clone(sc.Binding.Pin.Names), SPKI: sc.Binding.Pin.SPKI})
	}
	if d := state.DSM; d != nil {
		for _, kind := range []string{Public, Private} {
			ep := d.Public
			if kind == Private {
				ep = d.Private
			}
			if ep == nil {
				continue
			}
			out = append(out, PinBinding{Provider: ep.Provider, Socket: ep.Socket, Scope: kind, Target: TargetDSM, Port: d.Port, Authority: ep.Authority, Mode: Mode(d.Pin.Mode), Names: slices.Clone(d.Pin.Names), SPKI: d.Pin.SPKI, CertSHA256: d.Pin.CertSHA256})
		}
	}
	return out
}

// PinBindingFor maps the durable core target to an immutable proxy binding.
// It copies names; callers may safely retain it after the state snapshot changes.
func PinBindingFor(target Target) PinBinding {
	sc := target.Scope
	return PinBinding{EntryPath: EntryPath(sc.Binding.Entry), Provider: sc.Provider, Socket: sc.Socket, Scope: target.Kind, Summary: sc.Binding.Summary,
		Package: target.Package, Target: TargetApp, Port: sc.Binding.Port, Authority: sc.Authority,
		Mode: Mode(sc.Binding.Pin.Mode), Names: slices.Clone(sc.Binding.Pin.Names), SPKI: sc.Binding.Pin.SPKI}
}
