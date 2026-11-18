package app

import (
	"context"
	"dsmapp/internal/control"
	"encoding/json"

	"dsmapp/internal/apps"
	"dsmapp/internal/connector"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

func (cfg Config) appAccess() apps.API {
	if cfg.Apps != nil {
		return cfg.Apps
	}
	return apps.Client{Socket: control.Socket}
}

func appsReply(out any, err error) (any, error) {
	if err != nil {
		return nil, api.WithDetails(&api.Error{Code: apps.ErrorCode(err), Message: err.Error()}, connector.Conflict(err))
	}
	return out, nil
}

func (cfg Config) appsList(ctx context.Context, caller api.Caller, params json.RawMessage) (any, error) {
	if err := api.RequireAdmin(caller); err != nil {
		return nil, err
	}
	if err := decodeParams(params, &struct{}{}); err != nil {
		return nil, err
	}
	out, err := cfg.appAccess().List(ctx)
	return appsReply(out, err)
}
func (cfg Config) appsSetPrivate(ctx context.Context, caller api.Caller, params json.RawMessage) (any, error) {
	if err := api.RequireAdmin(caller); err != nil {
		return nil, err
	}
	var in apps.PrivateRequest
	if err := decodeParams(params, &in); err != nil {
		return nil, err
	}
	var required struct {
		Enabled *bool `json:"enabled"`
	}
	_ = json.Unmarshal(params, &required)
	if required.Enabled == nil {
		return nil, api.BadRequest("enabled is required")
	}
	out, err := cfg.appAccess().SetPrivate(ctx, in)
	return appsReply(out, err)
}
func (cfg Config) appsSetPublic(ctx context.Context, caller api.Caller, params json.RawMessage) (any, error) {
	if err := api.RequireAdmin(caller); err != nil {
		return nil, err
	}
	var in apps.PublicRequest
	if err := decodeParams(params, &in); err != nil {
		return nil, err
	}
	var required struct {
		Enabled *bool `json:"enabled"`
	}
	_ = json.Unmarshal(params, &required)
	if required.Enabled == nil {
		return nil, api.BadRequest("enabled is required")
	}
	out, err := cfg.appAccess().SetPublic(ctx, in)
	return appsReply(out, err)
}
func (cfg Config) appsReenable(ctx context.Context, caller api.Caller, params json.RawMessage) (any, error) {
	if err := api.RequireAdmin(caller); err != nil {
		return nil, err
	}
	var in apps.ReenableRequest
	if err := decodeParams(params, &in); err != nil {
		return nil, err
	}
	out, err := cfg.appAccess().Reenable(ctx, in)
	return appsReply(out, err)
}
func (cfg Config) appsClear(ctx context.Context, caller api.Caller, params json.RawMessage) (any, error) {
	if err := api.RequireAdmin(caller); err != nil {
		return nil, err
	}
	var in apps.ScopedChange
	if err := decodeParams(params, &in); err != nil {
		return nil, err
	}
	err := cfg.appAccess().Clear(ctx, in)
	return appsReply(map[string]bool{"cleared": err == nil}, err)
}

func (cfg Config) appsInspect(ctx context.Context, caller api.Caller, params json.RawMessage) (any, error) {
	if err := api.RequireAdmin(caller); err != nil {
		return nil, err
	}
	var in apps.InspectRequest
	if err := decodeParams(params, &in); err != nil {
		return nil, err
	}
	out, err := cfg.appAccess().Inspect(ctx, in)
	return appsReply(out, err)
}
func (cfg Config) appsInspectDSM(ctx context.Context, caller api.Caller, params json.RawMessage) (any, error) {
	if err := api.RequireAdmin(caller); err != nil {
		return nil, err
	}
	var in apps.DSMInspectRequest
	if err := decodeParams(params, &in); err != nil {
		return nil, err
	}
	out, err := cfg.appAccess().InspectDSM(ctx, in)
	return appsReply(out, err)
}
func (cfg Config) appsSetDSM(ctx context.Context, caller api.Caller, params json.RawMessage) (any, error) {
	if err := api.RequireAdmin(caller); err != nil {
		return nil, err
	}
	var in apps.DSMRequest
	if err := decodeParams(params, &in); err != nil {
		return nil, err
	}
	out, err := cfg.appAccess().SetDSM(ctx, in)
	return appsReply(out, err)
}
func (cfg Config) appsCloudflareConnect(ctx context.Context, caller api.Caller, params json.RawMessage) (any, error) {
	if err := api.RequireAdmin(caller); err != nil {
		return nil, err
	}
	if err := decodeParams(params, &struct{}{}); err != nil {
		return nil, err
	}
	out, err := cfg.appAccess().CloudflareConnect(ctx)
	return appsReply(out, err)
}
func (cfg Config) appsCloudflareDisconnect(ctx context.Context, caller api.Caller, params json.RawMessage) (any, error) {
	if err := api.RequireAdmin(caller); err != nil {
		return nil, err
	}
	if err := decodeParams(params, &struct{}{}); err != nil {
		return nil, err
	}
	err := cfg.appAccess().CloudflareDisconnect(ctx)
	return appsReply(map[string]bool{"disconnected": err == nil}, err)
}

func (cfg Config) appsCleanupOrphans(ctx context.Context, caller api.Caller, params json.RawMessage) (any, error) {
	if err := api.RequireAdmin(caller); err != nil {
		return nil, err
	}
	if err := decodeParams(params, &struct{}{}); err != nil {
		return nil, err
	}
	p, ok := cfg.appAccess().(apps.OrphanCleanupAPI)
	if !ok {
		return appsReply(nil, apps.ErrUnavailable)
	}
	out, err := p.CleanupPublicOrphans(ctx)
	return appsReply(out, err)
}
