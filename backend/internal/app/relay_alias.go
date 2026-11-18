package app

import (
	"context"
	"dsmapp/internal/apps"
	"dsmapp/internal/connector"
	"encoding/json"
	"errors"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

// The shipped App URLs page names workers.dev "relay". These UI aliases share
// the workers provider and approval state; there is no second public scope.
func (cfg Config) appsSetRelay(ctx context.Context, caller api.Caller, params json.RawMessage) (any, error) {
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
	if in.Provider != "" && in.Provider != apps.Workers {
		return nil, api.BadRequest("invalid relay provider")
	}
	in.Provider = apps.Workers
	out, err := cfg.appAccess().SetPublic(ctx, in)
	return appsReply(out, relayError(err))
}
func (cfg Config) appsRelayConnect(ctx context.Context, caller api.Caller, params json.RawMessage) (any, error) {
	out, err := cfg.appsWorkersConnect(ctx, caller, params)
	var e *api.Error
	if errors.As(err, &e) {
		err = relayError(connector.Coded(e.Code, "Workers request unavailable"))
		if err != nil {
			return nil, &api.Error{Code: apps.ErrorCode(err), Message: err.Error()}
		}
	}
	return out, err
}
func relayError(err error) error {
	switch connector.ErrorCode(err) {
	case "workers_token_invalid":
		return connector.Coded("apps_relay_token_invalid", "Cloudflare token is invalid")
	case "workers_permission_denied":
		return connector.Coded("apps_relay_token_scope", "Cloudflare token needs Account > Workers Scripts > Edit")
	case "workers_accounts_required":
		return connector.Coded("apps_relay_accounts", "Scope the token to one Cloudflare account or supply accountId")
	case "workers_unavailable":
		return connector.Coded("apps_relay_unreachable", "Cloudflare is currently unavailable")
	}
	return err
}
