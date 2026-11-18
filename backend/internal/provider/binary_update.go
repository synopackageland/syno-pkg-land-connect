package provider

import (
	"context"
	"errors"
	"time"
)

// UpdateBinaries serializes Tailscale activation with connect/disconnect/restore.
// prepare verifies a candidate and captures an exact rollback before any stop;
// apply/revert only change binary selection, never start a process. The shared
// Runtime and BinaryManager interfaces are intentionally unchanged.
func (m *Selection) UpdateBinaries(ctx context.Context, id string, prepare func() (apply func() error, revert func() error, err error)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending != nil {
		return errApplyBusy
	}
	if e := ctx.Err(); e != nil {
		return e
	}
	if m.portBlocked {
		return errors.New("port reconciliation required before binary update")
	}
	p := m.registry[id]
	if (id != "tailscale" && id != "cloudflare" && id != "netbird") || p == nil || prepare == nil {
		return ErrUnavailable
	}
	apply, revert, e := prepare()
	if e != nil {
		return e
	}
	if apply == nil || revert == nil {
		return ErrUnavailable
	}
	if m.saved.Provider != id || !m.saved.Enabled {
		if e = apply(); e != nil {
			if re := revert(); re != nil {
				return errors.New("binary selection recovery failed")
			}
		}
		return e
	}
	original := m.saved
	// DSM may change while a verified update holds the owner lock. Resolve again
	// before both candidate and rollback restarts; never replay stale upstreams.
	restoreSelected := func(ctx context.Context) error {
		if m.ports == nil || id == "netbird" {
			return p.Restore(ctx)
		}
		e := m.restorePorts(ctx, p)
		original.Settings = m.saved.Settings
		return e
	}
	allowLogin := p.Status().State == "needs-login"
	// A crash during activation must not automatically run a candidate that has
	// not passed readiness. The old accepted restart intent is restored below.
	gate := original
	gate.Enabled = false
	if e = m.persist(gate); e != nil {
		return e
	}
	m.generation++
	m.failed = true
	if e = p.Stop(); e != nil {
		return errors.New("connector cleanup required before binary update")
	}
	if e = apply(); e == nil {
		if e = ctx.Err(); e == nil {
			readyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
			e = restoreSelected(readyCtx)
			if e == nil {
				e = waitBinaryReady(readyCtx, p, allowLogin)
			}
			cancel()
		}
	}
	if e == nil {
		e = m.persist(original)
		if e == nil {
			m.failed = false
			return nil
		}
	}
	// Never overlap a replacement with the old child; uncertain cleanup keeps
	// the disabled gate and refuses to change the selected executable again.
	if stopErr := p.Stop(); stopErr != nil {
		return errors.New("connector cleanup required after binary update")
	}
	if restoreErr := revert(); restoreErr != nil {
		return errors.New("binary selection recovery failed")
	}
	if ctx.Err() != nil {
		// Package shutdown cancels the job. Preserve the accepted old version for
		// next boot, but do not launch another child during shutdown.
		if restoreErr := m.persist(original); restoreErr != nil {
			return restoreErr
		}
		return ctx.Err()
	}
	recoveryCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if restoreErr := restoreSelected(recoveryCtx); restoreErr != nil {
		_ = p.Stop()
		return errors.New("previous connector version could not restart")
	}
	if restoreErr := waitBinaryReady(recoveryCtx, p, allowLogin); restoreErr != nil {
		_ = p.Stop()
		return errors.New("previous connector version is not ready")
	}
	if restoreErr := m.persist(original); restoreErr != nil {
		_ = p.Stop()
		return restoreErr
	}
	m.failed = false
	return errors.New("binary activation failed; previous version restored")
}
func waitBinaryReady(ctx context.Context, p Runtime, allowLogin bool) error {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if e := ctx.Err(); e != nil {
			return e
		}
		state := p.Status().State
		switch state {
		case "connected":
			return nil
		case "needs-login":
			if allowLogin {
				return nil
			}
			return ErrUnavailable
		case "connecting", "unknown":
		default:
			return ErrUnavailable
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
