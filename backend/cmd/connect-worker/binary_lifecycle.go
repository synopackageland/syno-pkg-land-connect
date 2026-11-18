package main

import (
	"context"
	"dsmapp/internal/apps"
	"dsmapp/internal/binaries"
	"dsmapp/internal/connector"
	"errors"
)

// Cancel jobs before waiting on the selector's lifecycle lock. Otherwise a
// shutdown request could wait for update readiness while the job stays alive.
type lifecycleSelection interface {
	connector.Controller
	Restore(context.Context) error
}
type managedSelection struct {
	beforeDisconnect func(string) error
	pinProxies       *apps.PinRegistry
	trustDSM         func(context.Context) error
	dsmDetails       func() *connector.TrustedCertificate
	lifecycleSelection
	binaries *binaries.Service
	checker  *connector.Checker
}

// Connect makes a new connection, so its earlier check no longer applies.
func (m *managedSelection) Connect(ctx context.Context, r connector.Request) (connector.Status, error) {
	if m.trustDSM != nil && (r.Provider == "tailscale" || r.Provider == "cloudflare" && (r.Mode == "domain" || r.Mode == "quick") && r.ConfirmExposure) {
		if err := m.trustDSM(ctx); err != nil {
			return m.Status(), err
		}
	}
	m.checker.Reset(r.Provider)
	s, err := m.lifecycleSelection.Connect(ctx, r)
	return m.details(s), err
}

func (m *managedSelection) details(s connector.Status) connector.Status {
	if m.dsmDetails != nil {
		s.TrustedDSM = m.dsmDetails()
	}
	return s
}
func (m *managedSelection) Status() connector.Status { return m.details(m.lifecycleSelection.Status()) }

func (m *managedSelection) Check(ctx context.Context, provider string) (connector.CheckResult, error) {
	return m.checker.Check(ctx, provider), nil
}

func (m *managedSelection) Stop() error {
	if m.pinProxies != nil {
		m.pinProxies.Close()
	}
	if e := m.binaries.Close(); e != nil {
		return e
	}
	return m.lifecycleSelection.Stop()
}

func (m *managedSelection) DisconnectProvider(id string) (connector.Status, error) {
	if m.beforeDisconnect != nil {
		if e := m.beforeDisconnect(id); e != nil {
			return m.Status(), e
		}
	}
	if scoped, ok := m.lifecycleSelection.(interface {
		DisconnectProvider(string) (connector.Status, error)
	}); ok {
		return scoped.DisconnectProvider(id)
	}
	return connector.Status{}, errors.New("scoped disconnect unavailable")
}

func (m *managedSelection) Forget(id string) (connector.Status, error) {
	if forgetter, ok := m.lifecycleSelection.(interface {
		Forget(string) (connector.Status, error)
	}); ok {
		return forgetter.Forget(id)
	}
	return connector.Status{}, errors.New("forgetting services unavailable")
}

func (m *managedSelection) Disconnect() (connector.Status, error) {
	if m.beforeDisconnect != nil {
		if e := m.beforeDisconnect(""); e != nil {
			return m.Status(), e
		}
	}
	return m.lifecycleSelection.Disconnect()
}
