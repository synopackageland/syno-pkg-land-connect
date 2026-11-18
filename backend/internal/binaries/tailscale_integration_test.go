package binaries

import (
	"context"
	"dsmapp/internal/connector"
	"dsmapp/internal/provider"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

type commandProbe struct {
	paths   []string
	procs   []*probeProcess
	failDir string
}
type probeProcess struct{ stopped bool }

func (p *probeProcess) Stop() error { p.stopped = true; return nil }
func (p *probeProcess) Alive() bool { return !p.stopped }
func (p *probeProcess) Ready() bool { return p.Alive() }
func (p *commandProbe) Start(path string, _ ...string) (connector.Process, error) {
	p.paths = append(p.paths, path)
	if p.failDir != "" && filepath.Dir(path) == p.failDir {
		return nil, errors.New("fixture replacement failed")
	}
	process := &probeProcess{}
	p.procs = append(p.procs, process)
	return process, nil
}
func (p *commandProbe) Run(_ context.Context, path string, args ...string) ([]byte, error) {
	p.paths = append(p.paths, path)
	if strings.Contains(strings.Join(args, " "), "serve status") {
		return []byte(`{"TCP":{"443":{"HTTPS":true}},"Web":{"fixture.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:5000"}}}}}`), nil
	}
	return []byte(`{"BackendState":"Running","TailscaleIPs":["100.64.0.1"],"CertDomains":["nas.fixture.ts.net","fixture.ts.net"],"Self":{"DNSName":"fixture.ts.net."}}`), nil
}
func TestVerifiedAdapterDrivesTailscaleRuntime(t *testing.T) {
	source, _, _ := signedFixture(t)
	m := New(t.TempDir(), source)
	ctx := context.Background()
	if e := m.install(ctx, Release{Product: "tailscale", Version: "1.2.3", URL: "https://pkgs.tailscale.com/stable/test.tgz"}); e != nil {
		t.Fatal(e)
	}
	adapter := NewRuntimeAdapter(m)
	commands := &commandProbe{}
	dir := t.TempDir()
	runtime := provider.NewTailscale(dir, provider.TailscaleExec{Dir: dir, Binaries: adapter, Commands: commands})
	defer runtime.Stop()
	_, e := runtime.Start(ctx, provider.Settings{})
	if e != nil {
		t.Fatal(e)
	}
	if s := runtime.Status(); s.State != "connected" {
		t.Fatal(s)
	}
	paths, e := m.Paths("tailscale")
	if e != nil {
		t.Fatal(e)
	}
	for _, path := range commands.paths {
		if paths[filepath.Base(path)] != path {
			t.Fatal("unverified executable supplied", path)
		}
	}
}
func TestInstalledHookRestartsOrRecoversTailscale(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "upgrade", true: "recover"}[fail], func(t *testing.T) {
			source, _, _ := signedFixture(t)
			m := New(t.TempDir(), source)
			ctx := context.Background()
			install := func(v string) {
				t.Helper()
				if e := m.install(ctx, Release{Product: "tailscale", Version: v, URL: "https://pkgs.tailscale.com/stable/test.tgz"}); e != nil {
					t.Fatal(e)
				}
			}
			install("1.2.3")
			adapter := NewRuntimeAdapter(m)
			commands := &commandProbe{}
			dir := t.TempDir()
			ts := provider.NewTailscale(filepath.Join(dir, "tailscale"), provider.TailscaleExec{Dir: dir, Binaries: adapter, Commands: commands})
			selector := provider.NewSelection(dir, ts)
			defer selector.Stop()
			if _, e := selector.Connect(ctx, connector.Request{Provider: "tailscale"}); e != nil {
				t.Fatal(e)
			}
			oldCount := len(commands.procs)
			install("1.2.4")
			paths, e := m.Paths("tailscale")
			if e != nil {
				t.Fatal(e)
			}
			if fail {
				commands.failDir = filepath.Dir(paths["tailscaled"])
			}
			hook := RuntimeInstalledHook(selector, adapter)
			e = m.Activate(ctx, "tailscale", func(ctx context.Context, paths map[string]string) error { return hook(ctx, "tailscale", paths) })
			if (e != nil) != fail {
				t.Fatal(e)
			}
			for _, p := range commands.procs[:oldCount] {
				if !p.stopped {
					t.Fatal("old child survived restart")
				}
			}
			if status := selector.Status(); status.State != "connected" {
				t.Fatal(status)
			}
			if !fail {
				count := len(commands.procs)
				if err := hook(ctx, "tailscale", paths); err != nil {
					t.Fatal(err)
				}
				if len(commands.procs) != count {
					t.Fatal("same-version update restarted runtime")
				}
			}

			want := "1.2.4"
			if fail {
				want = "1.2.3"
			}
			v, e := NewRuntimeAdapter(m).Version(ctx, "tailscaled")
			if e != nil || v != want {
				t.Fatal(v, e)
			}
			current, _ := m.Status("tailscale")
			if current.Current != "1.2.4" {
				t.Fatal("verified candidate was lost")
			}
		})
	}
}
func TestInstalledHookDoesNotStartInactiveTailscale(t *testing.T) {
	source, _, _ := signedFixture(t)
	m := New(t.TempDir(), source)
	ctx := context.Background()
	if e := m.install(ctx, Release{Product: "tailscale", Version: "1.2.3", URL: "https://pkgs.tailscale.com/stable/test.tgz"}); e != nil {
		t.Fatal(e)
	}
	adapter := NewRuntimeAdapter(m)
	commands := &commandProbe{}
	dir := t.TempDir()
	ts := provider.NewTailscale(dir, provider.TailscaleExec{Dir: dir, Binaries: adapter, Commands: commands})
	selector := provider.NewSelection(dir, ts)
	hook := RuntimeInstalledHook(selector, adapter)
	if e := hook(ctx, "tailscale", nil); e != nil {
		t.Fatal(e)
	}
	if len(commands.paths) != 0 || adapter.SelectedVersion("tailscale") != "1.2.3" {
		t.Fatal(commands.paths)
	}
}
