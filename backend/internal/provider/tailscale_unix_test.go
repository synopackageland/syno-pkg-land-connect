package provider

import (
	"context"
	"dsmapp/internal/apps"
	"dsmapp/internal/connector"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type unixBinaries struct {
	fakeBinaries
	version string
}

func (b unixBinaries) Resolve(ctx context.Context, name string) (Binary, error) {
	v, e := b.fakeBinaries.Resolve(ctx, name)
	v.Version = b.version
	return v, e
}

type privateTargetFixture struct {
	socket, authority string
	calls             int
}

func (f *privateTargetFixture) ResolvePrivateTargets(_ context.Context, m []PortMapping, name string) ([]PortMapping, error) {
	f.calls++
	for i := range m {
		if m[i].Protocol != "tcp" {
			m[i].Protocol = "unix"
			m[i].Socket = f.socket
			f.authority = name + ":" + strconv.Itoa(m[i].PublicPort)
		}
	}
	return m, nil
}

func TestTailscaleUnixTargetVersionAuthorityAndPrerequisite(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	socket := apps.PinSocket("dsm")
	commands := &commandFixture{status: []byte(`{"Self":{"DNSName":"nas.example.ts.net."},"CertDomains":["nas.example.ts.net"]}`)}
	session, e := (TailscaleExec{Dir: filepath.Join(dir, "tailscale"), Binaries: unixBinaries{version: "1.104.1"}, Commands: commands}).Open(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer session.Stop()
	s := session.(*tailscaleExecSession)
	targets := &privateTargetFixture{socket: socket}
	s.targets = targets
	route := []PortMapping{{LocalPort: 5001, PublicPort: 5001, Protocol: "https"}}
	if e = s.Configure(ctx, route); e != nil {
		t.Fatal(e)
	}
	if targets.authority != "nas.example.ts.net:5001" {
		t.Fatal(targets.authority)
	}
	last := strings.Join(commands.args[len(commands.args)-1], " ")
	if !strings.Contains(last, "serve --yes --https=5001 unix:"+socket) || strings.Contains(last, "insecure") || strings.Contains(last, "funnel") {
		t.Fatal(last)
	}
	count := len(commands.procs)
	if e = s.Configure(ctx, route); e != nil || len(commands.procs) != count || targets.calls != 2 {
		t.Fatal("idempotent target skipped authority refresh", e)
	}
	raw := []byte(`{"TCP":{"5001":{"HTTPS":true}},"Web":{"nas.example.ts.net:5001":{"Handlers":{"/":{"Proxy":"unix:` + socket + `"}}}}}`)
	if ok, e := checkServeMode(raw, "nas.example.ts.net", s.mappings); !ok || e != nil {
		t.Fatal("Unix readback", e)
	}
	public := []byte(`{"TCP":{"5001":{"HTTPS":true}},"Web":{"nas.example.ts.net:5001":{"Handlers":{"/":{"Proxy":"unix:` + socket + `"}}}},"AllowFunnel":{"nas.example.ts.net:5001":true}}`)
	if ok, e := checkServeMode(public, "nas.example.ts.net", s.mappings); ok || e == nil {
		t.Fatal("a public route read back as private")
	}
	current := s.forwards[0].(*execFixtureProcess)
	commands.status = []byte(`{"Self":{"DNSName":"nas.example.ts.net."},"CertDomains":[]}`)
	if e = s.Configure(ctx, s.mappings); connector.ErrorCode(e) != "serve_prerequisite_missing" || !current.stopped {
		t.Fatal("existing route bypassed HTTPS prerequisite", e)
	}
}

func TestUnixServeVersionFloorAndInvalidSocket(t *testing.T) {
	for version, want := range map[string]bool{"1.104.0": false, "1.104.1": true, "1.104.1-pre": false, "1.105.0": true, "v2.0.0": true, "unknown": false} {
		if unixServeVersion(version) != want {
			t.Fatal(version)
		}
	}
	for _, m := range []PortMapping{{LocalPort: 7443, PublicPort: 7443, Protocol: "unix", Socket: "relative.sock"}, {LocalPort: 7443, PublicPort: 7443, Protocol: "unix", Socket: "/tmp/a.sock"}, {LocalPort: 7443, PublicPort: 7443, Protocol: "unix", Socket: "/tmp/pinproxy/../a.sock"}, {LocalPort: 7443, PublicPort: 7443, Protocol: "unix", Socket: "/tmp/pinproxy/a.sock/extra"}, {LocalPort: 5000, PublicPort: 5000, Protocol: "http"}, {LocalPort: 22, PublicPort: 22, Protocol: "tcp"}} {
		if e := validServeRoutes([]PortMapping{m}, false); e == nil {
			t.Fatal(m)
		}
	}
	ctx := context.Background()
	commands := &commandFixture{status: []byte(`{"Self":{"DNSName":"nas.example.ts.net."},"CertDomains":["nas.example.ts.net"]}`)}
	s, e := (TailscaleExec{Dir: t.TempDir(), Binaries: unixBinaries{version: "1.104.0"}, Commands: commands}).Open(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer s.Stop()
	if e = s.Configure(ctx, []PortMapping{{LocalPort: 7443, PublicPort: 7443, Protocol: "unix", Socket: apps.PinSocket("a")}}); connector.ErrorCode(e) != "tailscale_update_required" || len(commands.procs) != 1 {
		t.Fatal(e)
	}
}
