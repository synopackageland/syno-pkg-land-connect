package app

import (
	"context"
	"dsmapp/internal/apps"
	"encoding/json"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

func (cfg Config) appsWorkersStatus(ctx context.Context, caller api.Caller, params json.RawMessage) (any, error) {
	if err := api.RequireAdmin(caller); err != nil {
		return nil, err
	}
	if err := decodeParams(params, &struct{}{}); err != nil {
		return nil, err
	}
	p, ok := cfg.appAccess().(apps.WorkersAPI)
	if !ok {
		return appsReply(nil, apps.ErrUnavailable)
	}
	out, err := p.WorkersStatus(ctx)
	return appsReply(out, err)
}
func (cfg Config) appsWorkersConnect(ctx context.Context, caller api.Caller, params json.RawMessage) (any, error) {
	if err := api.RequireAdmin(caller); err != nil {
		return nil, err
	}
	var in apps.WorkersConnectRequest
	if err := decodeParams(params, &in); err != nil {
		return nil, err
	}
	p, ok := cfg.appAccess().(apps.WorkersAPI)
	if !ok {
		return appsReply(nil, apps.ErrUnavailable)
	}
	out, err := p.WorkersConnect(ctx, in)
	return appsReply(out, err)
}
func (cfg Config) appsWorkersPublishDSM(ctx context.Context, caller api.Caller, params json.RawMessage) (any, error) {
	if err := api.RequireAdmin(caller); err != nil {
		return nil, err
	}
	if err := decodeParams(params, &struct{}{}); err != nil {
		return nil, err
	}
	p, ok := cfg.appAccess().(apps.WorkersDSMAPI)
	if !ok {
		return appsReply(nil, apps.ErrUnavailable)
	}
	out, err := p.WorkersPublishDSM(ctx)
	return appsReply(out, err)
}
func (cfg Config) appsWorkersDisconnect(ctx context.Context, caller api.Caller, params json.RawMessage) (any, error) {
	if err := api.RequireAdmin(caller); err != nil {
		return nil, err
	}
	if err := decodeParams(params, &struct{}{}); err != nil {
		return nil, err
	}
	p, ok := cfg.appAccess().(apps.WorkersAPI)
	if !ok {
		return appsReply(nil, apps.ErrUnavailable)
	}
	err := p.WorkersDisconnect(ctx)
	return appsReply(map[string]bool{"disconnected": err == nil}, err)
}

// appsResetState starts App URL records over; the page confirms it first.
func (cfg Config) appsResetState(ctx context.Context, caller api.Caller, params json.RawMessage) (any, error) {
	if err := api.RequireAdmin(caller); err != nil {
		return nil, err
	}
	var in struct {
		Confirm bool `json:"confirm"`
	}
	if err := decodeParams(params, &in); err != nil {
		return nil, err
	}
	if !in.Confirm {
		return nil, api.BadRequest("confirmation required")
	}
	p, ok := cfg.appAccess().(apps.StateResetAPI)
	if !ok {
		return appsReply(nil, apps.ErrUnavailable)
	}
	return appsReply(p.ResetState(ctx))
}

func (cfg Config) appsClearDSM(ctx context.Context, caller api.Caller, params json.RawMessage) (any, error) {
	if err := api.RequireAdmin(caller); err != nil {
		return nil, err
	}
	var in apps.DSMInspectRequest
	if err := decodeParams(params, &in); err != nil {
		return nil, err
	}
	p, ok := cfg.appAccess().(apps.DSMClearAPI)
	if !ok {
		return appsReply(nil, apps.ErrUnavailable)
	}
	err := p.ClearDSM(ctx, in)
	return appsReply(map[string]bool{"cleared": err == nil}, err)
}
