package binaries

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T) (*Manager, *httptest.Server, *[]byte) {
	t.Helper()
	data := hostExecutable("first binary")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write(data) }))
	t.Cleanup(server.Close)
	s := NewOfficialSource()
	s.client = server.Client()
	s.hosts = map[string]bool{server.Listener.Addr().String(): true}
	m := New(t.TempDir(), s)
	return m, server, &data
}
func release(url, version string, b []byte) Release {
	return Release{Product: "cloudflared", Version: version, URL: url, SHA256: fmt.Sprintf("%x", sha256.Sum256(b))}
}
func TestInstallFailureRollbackAndDowngrade(t *testing.T) {
	m, s, b := fixture(t)
	ctx := context.Background()
	if err := m.install(ctx, release(s.URL, "2026.1.0", *b)); err != nil {
		t.Fatal(err)
	}
	before, _ := m.Status("cloudflared")
	*b = hostExecutable("second binary")
	bad := release(s.URL, "2026.2.0", []byte("wrong"))
	if m.install(ctx, bad) == nil {
		t.Fatal("checksum accepted")
	}
	after, _ := m.Status("cloudflared")
	if after.Current != before.Current {
		t.Fatal("failed install changed current")
	}
	if err := m.install(ctx, release(s.URL, "2026.2.0", *b)); err != nil {
		t.Fatal(err)
	}
	if err := m.Rollback(ctx, "cloudflared"); err != nil {
		t.Fatal(err)
	}
	after, _ = m.Status("cloudflared")
	if after.Current != "2026.1.0" {
		t.Fatal(after)
	}
	if m.install(ctx, release(s.URL, "2026.1.1", *b)) == nil {
		t.Fatal("downgrade below high watermark accepted")
	}
}
func TestDownloadLimitsAndHosts(t *testing.T) {
	m, s, b := fixture(t)
	m.source.(*OfficialSource).maxBinary = 4
	if m.install(context.Background(), release(s.URL, "1.0.0", *b)) == nil {
		t.Fatal("oversize accepted")
	}
	for _, u := range []string{"http://pkgs.tailscale.com/a", "https://evil.invalid/a", "https://pkgs.tailscale.com:123/a"} {
		if _, e := m.source.(*OfficialSource).get(context.Background(), u, 20); e == nil {
			t.Fatal(u)
		}
	}
}
func archive(t *testing.T, name string, kind byte) []byte {
	t.Helper()
	var b bytes.Buffer
	z := gzip.NewWriter(&b)
	w := tar.NewWriter(z)
	h := &tar.Header{Name: name, Mode: 0755, Typeflag: kind}
	if kind == tar.TypeReg {
		h.Size = int64(len(hostExecutable("a")))
	}
	if err := w.WriteHeader(h); err != nil {
		t.Fatal(err)
	}
	if kind == tar.TypeReg {
		w.Write(hostExecutable("a"))
	}
	w.Close()
	z.Close()
	return b.Bytes()
}
func TestHostileTar(t *testing.T) {
	for _, x := range []struct {
		name string
		kind byte
	}{{"../tailscale", tar.TypeReg}, {"/tailscale", tar.TypeReg}, {"tailscale", tar.TypeSymlink}, {"dir/tailscaled", tar.TypeLink}} {
		t.Run(x.name, func(t *testing.T) {
			dir := t.TempDir()
			if extract(bytes.NewReader(archive(t, x.name, x.kind)), dir, 100) == nil {
				t.Fatal("unsafe tar accepted")
			}
			if _, e := os.Stat(filepath.Join(dir, "tailscale")); e == nil {
				t.Fatal("unexpected file")
			}
		})
	}
}
func TestVersion(t *testing.T) {
	for _, x := range []struct {
		a, b string
		want int
	}{{"1.10.0", "1.9.9", 1}, {"2026.1.0", "2026.1.0", 0}, {"1.0.0", "2.0.0", -1}} {
		got, e := compare(x.a, x.b)
		if e != nil || got != x.want {
			t.Fatal(x, got, e)
		}
	}
	for _, v := range []string{"../1", "1.0", "v1.2.3", "1.2.3-rc1", "01.2.3"} {
		if _, e := compare(v, "1.0.0"); e == nil {
			t.Fatal(v)
		}
	}
}

type fixedSource struct {
	Source
	r Release
}

func (s fixedSource) Latest(context.Context, string) (Release, error) { return s.r, nil }

func TestCheckedBinaryReportsTheSamePhaseAsAFreshInstall(t *testing.T) {
	m, s, b := fixture(t)
	ctx := context.Background()
	if err := m.install(ctx, release(s.URL, "2026.1.0", *b)); err != nil {
		t.Fatal(err)
	}
	installed, _ := m.Status("cloudflared")
	m.source = fixedSource{r: release(s.URL, "2026.2.0", *b)}
	if err := m.Check(ctx, "cloudflared"); err != nil {
		t.Fatal(err)
	}
	checked, _ := m.Status("cloudflared")
	if installed.Phase != "ready" || checked.Phase != installed.Phase {
		t.Fatalf("installed %q, checked %q", installed.Phase, checked.Phase)
	}
	if fresh, _ := m.Status("tailscale"); fresh.Phase != "idle" {
		t.Fatalf("nothing installed: %q", fresh.Phase)
	}
}

func TestStatusReadsAnInstalledIdleStateAsReady(t *testing.T) {
	m, _, _ := fixture(t)
	if err := m.save(saved{Status: Status{Product: "tailscale", Current: "1.0.0", Phase: "idle"}}); err != nil {
		t.Fatal(err)
	}
	if s, _ := m.Status("tailscale"); s.Phase != "ready" {
		t.Fatalf("phase %q", s.Phase)
	}
}
