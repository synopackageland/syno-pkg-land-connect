package cloudflare

import (
	"context"

	"dsmapp/internal/apps"
	"dsmapp/internal/connector"
)

// ApprovedDSMTarget adapts the durable state shared with the proxy owner. It
// never reconstructs approval from DNS, an existing ingress or a socket file.
// The proxy owns listening and validates its state on every request.
func ApprovedDSMTarget(store apps.Store, port func(context.Context) (int, error)) func(context.Context, string) (string, error) {
	return func(ctx context.Context, authority string) (string, error) {
		if port == nil {
			return "", errProxyRequired
		}
		current, e := port(ctx)
		if e != nil {
			return "", errProxyRequired
		}
		state, e := store.Load()
		if e != nil {
			return "", e
		}
		d := state.DSM
		if d != nil && d.Port != current {
			return "", connector.DSMPortChanged(d.Port, current)
		}
		if d == nil || d.Public == nil || d.Public.Authority != authority {
			return "", errProxyRequired
		}
		path := apps.PinSocket(d.Public.Socket)
		if !privateSocket(path) {
			return "", errProxyRequired
		}
		return path, ctx.Err()
	}
}
