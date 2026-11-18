package app

import (
	"context"
	"encoding/json"

	"dsmapp/internal/apps"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

// Custom App routes are administrator-only, like every apps.* route. Saving a
// definition shares nothing; sharing is the ordinary approval afterwards.
func (cfg Config) customAPI(caller api.Caller, params json.RawMessage, in any) (apps.CustomAPI, error) {
	if err := api.RequireAdmin(caller); err != nil {
		return nil, err
	}
	if err := decodeParams(params, in); err != nil {
		return nil, err
	}
	p, ok := cfg.appAccess().(apps.CustomAPI)
	if !ok {
		return nil, &api.Error{Code: apps.ErrorCode(apps.ErrUnavailable), Message: apps.ErrUnavailable.Error()}
	}
	return p, nil
}

func (cfg Config) appsCustomAdd(ctx context.Context, caller api.Caller, params json.RawMessage) (any, error) {
	var in apps.CustomRequest
	p, err := cfg.customAPI(caller, params, &in)
	if err != nil {
		return nil, err
	}
	return appsReply(p.CustomAdd(ctx, in))
}
func (cfg Config) appsCustomEdit(ctx context.Context, caller api.Caller, params json.RawMessage) (any, error) {
	var in apps.CustomRequest
	p, err := cfg.customAPI(caller, params, &in)
	if err != nil {
		return nil, err
	}
	return appsReply(p.CustomEdit(ctx, in))
}
func (cfg Config) appsCustomRemove(ctx context.Context, caller api.Caller, params json.RawMessage) (any, error) {
	var in apps.CustomID
	p, err := cfg.customAPI(caller, params, &in)
	if err != nil {
		return nil, err
	}
	err = p.CustomRemove(ctx, in)
	return appsReply(map[string]bool{"removed": err == nil}, err)
}
func (cfg Config) appsCustomPause(ctx context.Context, caller api.Caller, params json.RawMessage) (any, error) {
	var in apps.CustomID
	p, err := cfg.customAPI(caller, params, &in)
	if err != nil {
		return nil, err
	}
	err = p.CustomPause(ctx, in)
	return appsReply(map[string]bool{"paused": err == nil}, err)
}
func (cfg Config) appsCustomCandidates(ctx context.Context, caller api.Caller, params json.RawMessage) (any, error) {
	var in struct {
		Portals []apps.PortalEntry `json:"portals,omitempty"`
	}
	p, err := cfg.customAPI(caller, params, &in)
	if err != nil {
		return nil, err
	}
	out, err := p.CustomCandidates(ctx)
	if err == nil {
		out.Candidates = apps.WithSynologyPortals(out.Candidates, in.Portals)
	}
	return appsReply(out, err)
}
