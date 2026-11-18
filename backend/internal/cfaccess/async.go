package cfaccess

import (
	"context"
	"time"
)

// Submit acknowledges a bounded owner job, not completion. Preflight runs
// before Apply persists intent; polling only reads the local result.
func (m *Manager) Submit(in Request) (Status, error) {
	if in.SessionDuration != "" && !sessionChoice(in.SessionDuration) {
		return m.Status(), failure("access_invalid_request")
	}
	emails, e := Emails(in.Emails)
	if e != nil {
		return m.Status(), e
	}
	in.Emails = emails
	if in.Revision != m.Status().Revision {
		return m.Status(), failure("access_revision_changed")
	}
	return m.submit(func(ctx context.Context) error { _, e := m.Apply(ctx, in); return e })
}
func (m *Manager) SubmitSync() (Status, error) {
	if !m.InUse() {
		m.attempt.Store(nil)
		return m.Status(), nil
	}
	return m.submit(m.Sync)
}
func (m *Manager) submit(run func(context.Context) error) (Status, error) {
	if !m.working.CompareAndSwap(false, true) {
		return m.Status(), failure("access_busy")
	}
	m.attempt.Store(nil)
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		ctx, cancel := context.WithTimeout(m.ctx, 90*time.Second)
		defer cancel()
		e := run(ctx)
		if e != nil {
			code := Code(e)
			m.attempt.Store(&code)
		} else if m.cfg.Reconciled != nil {
			m.cfg.Reconciled(ctx)
		}
		m.working.Store(false)
		m.Trigger()
		if m.cfg.Activity != nil {
			m.cfg.Activity()
		}
	}()
	return m.Status(), nil
}
func (m *Manager) Close() { m.cancel(); m.wg.Wait() }
