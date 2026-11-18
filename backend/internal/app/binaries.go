package app

import (
	"context"
	"dsmapp/internal/binaries"
	"dsmapp/internal/control"
	"encoding/json"
	"errors"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

func (cfg Config) binaryRoute(action string) func(context.Context, api.Caller, json.RawMessage) (any, error) {
	return func(ctx context.Context, _ api.Caller, params json.RawMessage) (any, error) {
		var in struct {
			Product string `json:"product"`
		}
		if e := decodeParams(params, &in); e != nil {
			return nil, e
		}
		if in.Product != "cloudflared" && in.Product != "tailscale" && in.Product != "netbird" && in.Product != "netbird-server" {
			return nil, api.BadRequest("unknown binary")
		}

		owner := cfg.BinaryControl
		if owner == nil {
			owner = binaries.Client{Socket: control.Socket}
		}
		status, e := owner.Call(ctx, action, in.Product)
		if errors.Is(e, binaries.ErrBusy) {
			return nil, &api.Error{Code: "binary_busy", Message: "binary operation already in progress"}
		}
		if errors.Is(e, binaries.ErrNoPrevious) {
			return nil, &api.Error{Code: "binary_no_previous", Message: "no previous version to roll back to"}
		}
		if e != nil {
			return nil, &api.Error{Code: "binary_failed", Message: "binary operation failed; inspect status"}
		}
		return status, nil
	}
}
