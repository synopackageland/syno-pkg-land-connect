package binaries

import (
	"context"
	"errors"
)

type BinaryOwner interface {
	UpdateBinaries(context.Context, string, func() (func() error, func() error, error)) error
}

var connectionFor = map[string]string{"cloudflared": "cloudflare", "tailscale": "tailscale", "netbird": "netbird"}

// RuntimeInstalledHook activates a downloaded product for the connection that
// runs it. The selector stops and restarts only that connection.
func RuntimeInstalledHook(selector BinaryOwner, adapter *RuntimeAdapter) InstalledHook {
	return func(ctx context.Context, product string, _ map[string]string) error {
		id, ok := connectionFor[product]
		if !ok {
			return nil
		}
		err := selector.UpdateBinaries(ctx, id, func() (func() error, func() error, error) { return adapter.PrepareRefresh(ctx, product) })
		if errors.Is(err, errSelectionUnchanged) {
			return nil
		}
		return err
	}
}

// ServerOwner runs a server from one product, outside the connections.
type ServerOwner interface {
	UpdateBinary(context.Context, func() (func() error, func() error, error)) error
}

// WithServer activates product for owner and every other product through next.
func WithServer(next InstalledHook, product string, owner ServerOwner, adapter *RuntimeAdapter) InstalledHook {
	return func(ctx context.Context, p string, paths map[string]string) error {
		if p != product {
			return next(ctx, p, paths)
		}
		err := owner.UpdateBinary(ctx, func() (func() error, func() error, error) { return adapter.PrepareRefresh(ctx, product) })
		if errors.Is(err, errSelectionUnchanged) {
			return nil
		}
		return err
	}
}
