package app

import (
	"context"
	"encoding/json"
	"sync"

	appapi "dsmapp/internal/api"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

func previewRoutes() []api.Route {
	return []api.Route{
		api.Handle("external.set.preview", operationPreview("external.set")),
		api.Handle("ctl.connection.disconnect.preview", operationPreview("ctl.connection.disconnect")),
		api.Handle("ctl.connection.connect.preview", operationPreview("ctl.connection.connect")),
		api.Handle("ctl.connection.forget.preview", operationPreview("ctl.connection.forget")),
		api.Handle("ctl.ports.followset.preview", operationPreview("ctl.ports.followset")),
		api.Handle("ctl.cloudflare.access.set.preview", operationPreview("ctl.cloudflare.access.set")),
		api.Handle("ctl.cloudflare.access.sync.preview", operationPreview("ctl.cloudflare.access.sync")),
		api.Handle("ctl.ports.upsert.preview", operationPreview("ctl.ports.upsert")),
		api.Handle("ctl.ports.delete.preview", operationPreview("ctl.ports.delete")),
		api.Handle("ctl.ports.dsmset.preview", operationPreview("ctl.ports.dsmset")),
		api.Handle("binaries.check.preview", operationPreview("binaries.check")),
		api.Handle("binaries.update.preview", operationPreview("binaries.update")),
		api.Handle("binaries.rollback.preview", operationPreview("binaries.rollback")),
	}
}

// operationPreview reads trusted metadata only. It never opens a store or
// contacts DSM, a daemon or a provider. The CLI plan displays redacted input;
// a separate human-confirmed call runs the existing validating write handler.
func operationPreview(method string) func(context.Context, api.Caller, json.RawMessage) (any, error) {
	operation, ok := previewOperations()[method]
	if !ok || operation.AI != "draft" {
		panic("preview requires a registered draft")
	}
	return func(_ context.Context, caller api.Caller, params json.RawMessage) (any, error) {
		if err := api.RequireAdmin(caller); err != nil {
			return nil, err
		}
		var input map[string]json.RawMessage
		if err := decodeParams(params, &input); err != nil {
			return nil, err
		}
		if input == nil {
			return nil, api.BadRequest("params must be an object")
		}
		return struct {
			Method               string `json:"method"`
			Effect               string `json:"effect"`
			RequiresConfirmation bool   `json:"requiresConfirmation"`
		}{method, operation.Effect, true}, nil
	}
}

var previewOperations = sync.OnceValue(func() map[string]api.Operation {
	var registry struct {
		Operations map[string]api.Operation `json:"operations"`
	}
	if err := json.Unmarshal(appapi.Operations(), &registry); err != nil {
		panic(err)
	}
	return registry.Operations
})
