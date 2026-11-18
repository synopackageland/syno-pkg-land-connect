package provider

import (
	"context"
	"dsmapp/internal/connector"
	"errors"
	"path/filepath"
	"testing"
)

func TestMissingTailscaleBinariesRefuseBeforeStoppingCurrentProvider(t *testing.T) {
	dir := t.TempDir()
	events := []string{}
	cf := &fixtureRuntime{id: "cloudflare", events: &events}
	ts := NewTailscale(filepath.Join(dir, "tailscale"), TailscaleExec{Dir: dir, Binaries: fakeBinaries{missing: true}})
	m := NewSelection(dir, cf, ts)
	if _, e := m.Connect(context.Background(), connector.Request{ConfirmExposure: true}); e != nil {
		t.Fatal(e)
	}
	before := len(events)
	_, e := m.Connect(context.Background(), connector.Request{Provider: "tailscale"})
	if !errors.Is(e, connector.ErrBinaryRequired) || !cf.live || len(events) != before {
		t.Fatal(e, cf.live, events)
	}
	if m.Status().Provider != "cloudflare" {
		t.Fatal("missing download changed selection")
	}
}
