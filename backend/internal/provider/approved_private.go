package provider

import (
	"context"
	"net"
	"strconv"

	"dsmapp/internal/apps"
	"dsmapp/internal/connector"
)

// ApprovedPrivateTargets resolves only durable approvals using fresh daemon
// authority and DSM detection. A pre-upgrade direct DSM HTTPS route cannot
// survive without explicit proxy approval. This adapter never grants or changes
// an authority in the background; the approval owner records confirmed names.
type ApprovedPrivateTargets struct {
	Store     apps.Store
	DSMPort   func(context.Context) (int, error)
	EnsureDSM func(context.Context, string, string) error
}

func (a ApprovedPrivateTargets) ResolvePrivateTargets(ctx context.Context, mappings []PortMapping, name string) ([]PortMapping, error) {
	state, e := a.Store.Load()
	if e != nil {
		return nil, e
	}
	approved := apps.PinBindings(state)
	native := func(m PortMapping) bool {
		for _, b := range approved {
			if b.Scope == apps.Private && b.Target == apps.TargetApp && b.EntryPath != "" && b.Port == m.LocalPort && apps.PinSocket(b.Socket) == m.Socket {
				return true
			}
		}
		return false
	}
	if a.EnsureDSM != nil && a.DSMPort != nil {
		port, err := a.DSMPort(ctx)
		if err != nil {
			return nil, err
		}
		for _, m := range mappings {
			if !native(m) && (m.Protocol == "https" || m.Protocol == "unix") && m.LocalPort == port {
				if err := a.EnsureDSM(ctx, apps.Private, net.JoinHostPort(name, strconv.Itoa(m.PublicPort))); err != nil {
					return nil, err
				}
			}
		}
	}
	if a.DSMPort == nil {
		return nil, apps.ErrUnavailable
	}
	dsmPort, e := a.DSMPort(ctx)
	if e != nil || dsmPort < 1 || dsmPort > 65535 {
		return nil, apps.ErrUnavailable
	}
	out := append([]PortMapping(nil), mappings...)
	for i, m := range out {
		dsm := !native(m) && (m.Protocol == "https" || m.Protocol == "unix") && (m.LocalPort == dsmPort || (state.DSM != nil && m.LocalPort == state.DSM.Port))
		if m.Protocol != "unix" && !dsm {
			continue
		}
		authority := net.JoinHostPort(name, strconv.Itoa(m.PublicPort))
		found := false
		for _, binding := range approved {
			socket := apps.PinSocket(binding.Socket)
			if binding.Scope != apps.Private || binding.Port != m.LocalPort || binding.Authority != authority {
				continue
			}
			if binding.Target == apps.TargetDSM && binding.Port != dsmPort {
				continue
			}
			if dsm && binding.Target != apps.TargetDSM {
				continue
			}
			if !dsm && m.Socket != socket {
				continue
			}
			out[i].Protocol, out[i].Socket = "unix", socket
			found = true
			break
		}
		if !found {
			if !dsm {
				return nil, connector.Coded("apps_confirmation_changed", "review the private App binding in App URLs")
			}
			return nil, connector.Coded("pin_proxy_required", "review the DSM certificate")
		}
	}
	return out, ctx.Err()
}
