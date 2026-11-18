package app

import (
	"context"
	"dsmapp/internal/apps"
	"dsmapp/internal/cfaccess"
	"encoding/json"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

func (cfg Config) accessRoute(method string) func(context.Context, api.Caller, json.RawMessage) (any, error) {
	return func(ctx context.Context, caller api.Caller, params json.RawMessage) (any, error) {
		if e := api.RequireAdmin(caller); e != nil {
			return nil, e
		}
		p, ok := cfg.appAccess().(apps.AccessAPI)
		if !ok {
			return appsReply(nil, apps.ErrUnavailable)
		}
		switch method {
		case "apply":
			var in apps.AccessRequest
			if e := decodeParams(params, &in); e != nil {
				return nil, e
			}
			if !in.Confirm {
				return nil, api.BadRequest("confirmation required")
			}
			return appsReply(p.AccessApply(ctx, in))
		case "token":
			var in apps.AccessTokenRequest
			if e := decodeParams(params, &in); e != nil {
				return nil, e
			}
			return appsReply(p.AccessToken(ctx, in))
		default:
			if e := decodeParams(params, &struct{}{}); e != nil {
				return nil, e
			}
			if method == "sync" {
				return appsReply(p.AccessSync(ctx))
			}
			return appsReply(p.AccessGet(ctx))
		}
	}
}

// ctl never returns private email lists or public hostname inventories.
func (cfg Config) accessControlRoute(operation string) func(context.Context, api.Caller, json.RawMessage) (any, error) {
	return func(ctx context.Context, caller api.Caller, params json.RawMessage) (any, error) {
		value, e := cfg.accessRoute(operation)(ctx, caller, params)
		if e != nil {
			return nil, e
		}
		s, ok := value.(cfaccess.Status)
		if !ok {
			return nil, api.BadRequest("Access status unavailable")
		}
		protected := 0
		for _, ep := range s.Endpoints {
			if ep.Protected {
				protected++
			}
		}
		return struct {
			State          string `json:"state"`
			Code           string `json:"code,omitempty"`
			Revision       uint64 `json:"revision"`
			Working        bool   `json:"working"`
			EmailCount     int    `json:"emailCount"`
			EndpointCount  int    `json:"endpointCount"`
			ProtectedCount int    `json:"protectedCount"`
		}{s.State, s.Code, s.Revision, s.Working, len(s.Emails), len(s.Endpoints), protected}, nil
	}
}
