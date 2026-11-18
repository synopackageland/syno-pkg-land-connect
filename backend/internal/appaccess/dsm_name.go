package appaccess

import (
	"context"
	"dsmapp/internal/apps"
	"dsmapp/internal/connector"
)

type dsmNames interface {
	CheckDSMName(context.Context, string) error
	SetDSMName(context.Context, string) error
}

// InspectDSMName is a read-only proposal using the same public hostname,
// certificate, confirmation token and conflict contracts as App inspection.
func (e *Engine) InspectDSMName(ctx context.Context) (apps.Approval, error) {
	state, err := e.cfg.Store.Load()
	if err != nil {
		return apps.Approval{}, err
	}
	if state.DSM == nil || state.DSM.Public == nil {
		return apps.Approval{}, apps.ErrInvalid
	}
	ep := state.DSM.Public
	if (ep.Provider == apps.Workers) != (apps.PublicProvider(ctx) == apps.Workers) {
		return apps.Approval{}, apps.ErrInvalid
	}
	host := apps.Hostname(ctx, "")
	if host == "" {
		host = ep.Authority
	}
	for _, app := range state.Apps {
		if app.Public != nil && app.Public.Authority == host {
			code := "cloudflare_name_taken"
			if ep.Provider == apps.Workers {
				code = "workers_name_conflict"
			}
			return apps.Approval{}, connector.WithNameConflict(connector.Coded(code, "Name is used by another App"), connector.NameConflict{Reason: "other", ConflictName: host})
		}
	}
	if ep.Provider == apps.Workers {
		if e.cfg.Workers == nil {
			return apps.Approval{}, apps.ErrUnavailable
		}
		ctx = apps.WithHostnames(ctx, map[string]string{"": host})
		if _, err = e.cfg.Workers.Authority(ctx, ""); err != nil {
			return apps.Approval{}, err
		}
		if err = e.cfg.Workers.Check(ctx, []string{""}); err != nil {
			return apps.Approval{}, err
		}
	} else {
		if e.cfg.Public == nil {
			return apps.Approval{}, apps.ErrUnavailable
		}
		domain, loadErr := e.cfg.Public.AppDomain()
		if loadErr != nil {
			return apps.Approval{}, loadErr
		}
		host, err = selectHostname(ep.Authority, host, domain.Zone)
		if err != nil {
			return apps.Approval{}, err
		}
		names, ok := e.cfg.Public.(dsmNames)
		if !ok {
			return apps.Approval{}, apps.ErrUnavailable
		}
		if err = names.CheckDSMName(ctx, host); err != nil {
			return apps.Approval{}, err
		}
	}
	if err = e.verifyRetainedDSM(ctx, state.DSM, apps.Public, host); err != nil {
		return apps.Approval{}, err
	}
	old := apps.PinBinding{Provider: ep.Provider, Target: apps.TargetDSM, Scope: apps.Public, Port: state.DSM.Port, Authority: ep.Authority, Mode: apps.Mode(state.DSM.Pin.Mode), SPKI: state.DSM.Pin.SPKI, Names: state.DSM.Pin.Names, CertSHA256: state.DSM.Pin.CertSHA256}
	target := old
	target.Authority = host
	target.Socket = ""
	target, cert, err := e.proposal(ctx, target, &old)
	if err != nil {
		return apps.Approval{}, err
	}
	return apps.Approval{Kind: apps.Public, ReuseName: connector.ReuseFor(ctx, host), DSM: &apps.DSMApproval{Hostname: host, Port: target.Port, Pin: cert}, DSMTarget: &target}, nil
}

func (e *Engine) OpenDSMName(ctx context.Context, a apps.Approval) error {
	state, err := e.cfg.Store.Load()
	if err != nil {
		return err
	}
	if state.NextRev != a.ExpectedRevision || state.DSM == nil || state.DSM.Public == nil {
		return apps.ErrConfirmationChanged
	}
	old := *state.DSM.Public
	ctx = apps.WithHostnames(ctx, map[string]string{"": a.DSM.Hostname})
	if old.Provider != apps.Workers {
		names, ok := e.cfg.Public.(dsmNames)
		if !ok {
			return apps.ErrUnavailable
		}
		if err = names.SetDSMName(ctx, a.DSM.Hostname); err != nil {
			return err
		}
	}
	if err = e.OpenDSM(ctx, a); err != nil {
		return err
	}
	e.stop(old.Socket)
	if old.Provider == apps.Workers {
		e.workersAddress()
		return e.cfg.Workers.FinishRename(ctx, "")
	}
	if old.Authority != a.DSM.Hostname {
		return e.cfg.Public.DeleteAppRoute(ctx, old.Authority)
	}
	return nil
}
