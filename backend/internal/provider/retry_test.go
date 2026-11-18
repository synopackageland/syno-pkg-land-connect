package provider

import (
	"context"
	"dsmapp/internal/connector"
	"testing"
)

func TestRetryPreservesSavedIdentityAndDisabledGate(t *testing.T) {
	session := &tsSession{snapshot: runningTailscale()}
	driver := &failingDriver{session: &customRetrySession{session}}
	p := NewTailscale(t.TempDir(), driver)
	m := NewMultiSelection(t.TempDir(), p)
	if _, err := m.Connect(context.Background(), connector.Request{Provider: "tailscale", Retry: true}); err == nil {
		t.Fatal("disabled retry accepted")
	}
	if _, err := m.Connect(context.Background(), connector.Request{Provider: "tailscale", LoginServer: "https://control.example.test", LoginOrigin: "https://login.example.test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Connect(context.Background(), connector.Request{Provider: "tailscale", Retry: true}); err != nil {
		t.Fatal(err)
	}
	if p.saved.Settings.LoginServer != "https://control.example.test" {
		t.Fatal("retry replaced identity")
	}
	if _, err := m.Connect(context.Background(), connector.Request{Provider: "tailscale", Retry: true, Token: "unexpected"}); err == nil {
		t.Fatal("retry accepted new credentials")
	}
}

type customRetrySession struct{ *tsSession }

func (s *customRetrySession) LoginWithServer(ctx context.Context, key, server, origin string) error {
	return s.Login(ctx, key)
}

type terminalRuntime struct {
	*fixtureRuntime
	terminal bool
}

func (p *terminalRuntime) RecoveryTerminal() bool { return p.terminal }
func TestTerminalProvidersKeepIntentButStopPolling(t *testing.T) {
	events := []string{}
	p := &terminalRuntime{fixtureRuntime: &fixtureRuntime{id: "tailscale", events: &events}}
	m := NewMultiSelection(t.TempDir(), p)
	if m.NeedsPolling() {
		t.Fatal("disabled provider polls")
	}
	child := m.children["tailscale"]
	child.setSaved(selectionState{Provider: "tailscale", Enabled: true})
	if !m.NeedsPolling() {
		t.Fatal("enabled provider has no polling")
	}
	p.terminal = true
	if !m.Enabled() || m.NeedsPolling() {
		t.Fatal("failure cleared intent or retained timers")
	}
	p.terminal = false
	if !m.NeedsPolling() {
		t.Fatal("real trigger did not restore polling")
	}
	child.setSaved(selectionState{Provider: "tailscale"})
	if m.Enabled() || m.NeedsPolling() {
		t.Fatal("disabled provider retained demand")
	}
}
