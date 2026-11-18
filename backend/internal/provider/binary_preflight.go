package provider

import (
	"context"
	"dsmapp/internal/connector"
	"path/filepath"
)

// Preflight is checked before Selection disables the current connection. It
// verifies local executable availability without starting processes or logging in.
func (p *Tailscale) Preflight(ctx context.Context) error {
	if p.driver == nil {
		return connector.ErrBinaryRequired
	}
	if d, ok := p.driver.(interface{ CheckBinaries(context.Context) error }); ok {
		return d.CheckBinaries(ctx)
	}
	return nil
}
func (d TailscaleExec) CheckBinaries(ctx context.Context) error {
	if d.Binaries == nil {
		return connector.ErrBinaryRequired
	}
	for _, name := range []string{"tailscaled", "tailscale"} {
		b, e := d.Binaries.Resolve(ctx, name)
		if e != nil || !filepath.IsAbs(b.Path) {
			return connector.ErrBinaryRequired
		}
	}
	return nil
}
