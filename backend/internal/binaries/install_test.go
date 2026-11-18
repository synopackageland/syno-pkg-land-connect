package binaries

import "context"

// install installs one release under the manager lock, as Update does after its check.
func (m *Manager) install(ctx context.Context, r Release) error {
	return m.locked(ctx, func() error { return m.installLocked(ctx, r) })
}
