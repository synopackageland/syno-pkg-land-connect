package binaries

import (
	"context"
	"dsmapp/internal/provider"
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeAdapterRejectsTampering(t *testing.T) {
	m, s, b := fixture(t)
	ctx := context.Background()
	if e := m.install(ctx, release(s.URL, "2026.1.0", *b)); e != nil {
		t.Fatal(e)
	}
	official := NewRuntimeAdapter(m)
	var _ provider.BinaryManager = official
	got, e := official.Resolve(ctx, "cloudflared")
	if e != nil || got.Version != "2026.1.0" {
		t.Fatal(got, e)
	}
	if e = os.WriteFile(got.Path, []byte("tampered"), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e = official.Resolve(ctx, "cloudflared"); e == nil {
		t.Fatal("tampered candidate accepted")
	}
	if _, e = official.Resolve(ctx, "../tailscaled"); e == nil {
		t.Fatal("unsafe name accepted")
	}
}
func TestTailscaleAdapterResolvesOneVerifiedPair(t *testing.T) {
	s, _, _ := signedFixture(t)
	m := New(t.TempDir(), s)
	ctx := context.Background()
	if e := m.install(ctx, Release{Product: "tailscale", Version: "1.2.3", URL: "https://pkgs.tailscale.com/stable/test.tgz"}); e != nil {
		t.Fatal(e)
	}
	a := NewRuntimeAdapter(m)
	one, e := a.Resolve(ctx, "tailscale")
	if e != nil {
		t.Fatal(e)
	}
	two, e := a.Resolve(ctx, "tailscaled")
	if e != nil || filepath.Dir(one.Path) != filepath.Dir(two.Path) {
		t.Fatal(one, two, e)
	}
	v, e := a.Version(ctx, "tailscaled")
	if e != nil || v != "1.2.3" {
		t.Fatal(v, e)
	}
}
func TestRuntimePinsGenerationUntilProviderRefresh(t *testing.T) {
	m, server, b := fixture(t)
	ctx := context.Background()
	if e := m.install(ctx, release(server.URL, "1.0.0", *b)); e != nil {
		t.Fatal(e)
	}
	a := NewRuntimeAdapter(m)
	first, e := a.Resolve(ctx, "cloudflared")
	if e != nil {
		t.Fatal(e)
	}
	for _, v := range []string{"1.1.0", "1.2.0"} {
		*b = hostExecutable(v)
		if e = m.install(ctx, release(server.URL, v, *b)); e != nil {
			t.Fatal(e)
		}
	}
	old, e := a.Resolve(ctx, "cloudflared")
	if e != nil || old != first {
		t.Fatal("download silently changed provider generation", old, e)
	}
	if e = a.Refresh(ctx, "cloudflared"); e != nil {
		t.Fatal(e)
	}
	current, e := a.Resolve(ctx, "cloudflared")
	if e != nil || current.Version != "1.2.0" {
		t.Fatal(current, e)
	}
	if e = m.Rollback(ctx, "cloudflared"); e != nil {
		t.Fatal(e)
	}
	if e = a.Refresh(ctx, "cloudflared"); e != nil {
		t.Fatal(e)
	}
	current, e = a.Resolve(ctx, "cloudflared")
	if e != nil || current.Version != "1.1.0" {
		t.Fatal(current, e)
	}
}
func TestVersionDisplayDoesNotSelectNewDownload(t *testing.T) {
	m, server, b := fixture(t)
	if e := m.install(context.Background(), release(server.URL, "1.0.0", *b)); e != nil {
		t.Fatal(e)
	}
	plain := NewRuntimeAdapter(m)
	if plain.SelectedVersion("cloudflared") != "" {
		t.Fatal("status selected a candidate")
	}
	svc := NewService(m, nil)
	defer svc.Close()
	svc.CurrentVersion = plain.SelectedVersion
	s, e := svc.Call(context.Background(), "status", "cloudflared")
	if e != nil || s.RuntimeVersion != "" || s.Current != "1.0.0" {
		t.Fatal(s, e)
	}
}
func TestRuntimeSelectionSurvivesDaemonRestart(t *testing.T) {
	m, server, b := fixture(t)
	ctx := context.Background()
	if e := m.install(ctx, release(server.URL, "1.0.0", *b)); e != nil {
		t.Fatal(e)
	}
	a := NewRuntimeAdapter(m)
	first, e := a.Resolve(ctx, "cloudflared")
	if e != nil {
		t.Fatal(e)
	}
	*b = hostExecutable("next")
	if e = m.install(ctx, release(server.URL, "1.1.0", *b)); e != nil {
		t.Fatal(e)
	}
	restarted := NewRuntimeAdapter(m)
	got, e := restarted.Resolve(ctx, "cloudflared")
	if e != nil || got != first {
		t.Fatal("restart activated an unselected download", got, e)
	}
}
func TestProviderCanRestorePreviousSelectionAfterRestartFailure(t *testing.T) {
	m, server, b := fixture(t)
	ctx := context.Background()
	if e := m.install(ctx, release(server.URL, "1.0.0", *b)); e != nil {
		t.Fatal(e)
	}
	a := NewRuntimeAdapter(m)
	first, e := a.Resolve(ctx, "cloudflared")
	if e != nil {
		t.Fatal(e)
	}
	*b = hostExecutable("next")
	if e = m.install(ctx, release(server.URL, "1.1.0", *b)); e != nil {
		t.Fatal(e)
	}
	if e = a.Refresh(ctx, "cloudflared"); e != nil {
		t.Fatal(e)
	}
	if e = a.RestorePreviousSelection(ctx, "cloudflared"); e != nil {
		t.Fatal(e)
	}
	restart := NewRuntimeAdapter(m)
	got, e := restart.Resolve(ctx, "cloudflared")
	if e != nil || got != first {
		t.Fatal(got, e)
	}
	s, _ := m.Status("cloudflared")
	if s.Current != "1.1.0" {
		t.Fatal("recovery deleted verified candidate")
	}
}
