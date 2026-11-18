package app

import (
	"context"
	"dsmapp/internal/control"
	"encoding/json"
	"errors"
	"path/filepath"

	"dsmapp/internal/connector"
	"dsmapp/internal/nbserver"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

// NetBirdServerControl reaches the self-hosted server's owner in connectd.
type NetBirdServerControl interface {
	Call(context.Context, string, nbserver.Request) (nbserver.Result, error)
}

func (cfg Config) netbirdServerControl() NetBirdServerControl {
	if cfg.NetBirdServer != nil {
		return cfg.NetBirdServer
	}
	return nbserver.Client{Socket: control.Socket}
}

// netbirdServerRoute runs one server action in connectd. Setup returns at
// once; the page follows its steps through status.
func (cfg Config) netbirdServerRoute(action string) func(context.Context, api.Caller, json.RawMessage) (any, error) {
	return func(ctx context.Context, _ api.Caller, params json.RawMessage) (any, error) {
		var in nbserver.Request
		switch action {
		case "disable":
			var p struct {
				RemoveData bool `json:"removeData"`
			}
			if e := decodeParams(params, &p); e != nil {
				return nil, e
			}
			in.RemoveData = p.RemoveData
		case "setup":
			var p struct {
				UseAnotherName bool `json:"useAnotherName"`
			}
			if e := decodeParams(params, &p); e != nil {
				return nil, e
			}
			in.UseAnotherName = p.UseAnotherName
		default:
			if e := decodeParams(params, &struct{}{}); e != nil {
				return nil, e
			}
		}
		if action == "setup" {
			if cfg.activeProvider() == "unknown" {
				return nil, settingsError()
			}
			// Joining this NAS runs NetBird through the daemon-owned selector.
			if e := connector.WritePrivate(filepath.Join(cfg.DataDir, "active-provider"), []byte("multiple")); e != nil {
				return nil, settingsError()
			}
		}
		r, e := cfg.netbirdServerControl().Call(ctx, action, in)
		if e != nil {
			var coded *connector.CodedError
			if errors.As(e, &coded) {
				return nil, &api.Error{Code: coded.Code, Message: coded.Message}
			}
			return nil, &api.Error{Code: "netbird_server_unavailable", Message: "the NetBird server could not be reached"}
		}
		if action == "credentials" {
			return map[string]string{"email": r.Email, "password": r.Password}, nil
		}
		return r.Status, nil
	}
}
