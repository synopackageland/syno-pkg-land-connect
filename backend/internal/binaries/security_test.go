package binaries

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"golang.org/x/crypto/blake2s"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

type rewriteTransport struct {
	base   http.RoundTripper
	target *url.URL
}

func (r rewriteTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	u := *req.URL
	u.Scheme = r.target.Scheme
	u.Host = r.target.Host
	copy.URL = &u
	return r.base.RoundTrip(copy)
}
func signedFixture(t *testing.T) (*OfficialSource, map[string][]byte, []byte) {
	t.Helper()
	root, rpriv, _ := ed25519.GenerateKey(rand.Reader)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	keys := pem.EncodeToMemory(&pem.Block{Type: "SIGNING PUBLIC KEY", Bytes: pub})
	var b bytes.Buffer
	gz := gzip.NewWriter(&b)
	tw := tar.NewWriter(gz)
	for _, n := range []string{"tailscale", "tailscaled"} {
		tw.WriteHeader(&tar.Header{Name: "tailscale_1.2.3_amd64/" + n, Mode: 0755, Size: int64(len(hostExecutable("elf")))})
		tw.Write(hostExecutable("elf"))
	}
	tw.Close()
	gz.Close()
	body := b.Bytes()
	h := blake2s.Sum256(body)
	files := map[string][]byte{"/distsign.pub": keys, "/distsign.pub.sig": ed25519.Sign(rpriv, keys), "/stable/test.tgz": body, "/stable/test.tgz.sig": ed25519.Sign(priv, binary.LittleEndian.AppendUint64(h[:], uint64(len(body))))}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if b, ok := files[r.URL.Path]; ok {
			w.Write(b)
		} else {
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	s := NewOfficialSource()
	u, _ := url.Parse(server.URL)
	s.client = server.Client()
	s.client.Transport = rewriteTransport{s.client.Transport, u}
	s.roots = pem.EncodeToMemory(&pem.Block{Type: "ROOT PUBLIC KEY", Bytes: root})
	return s, files, body
}
func TestSignedTailscale(t *testing.T) {
	for _, failure := range []string{"", "key", "signature", "payload"} {
		t.Run(failure, func(t *testing.T) {
			s, files, _ := signedFixture(t)
			switch failure {
			case "key":
				files["/distsign.pub.sig"][0] ^= 1
			case "signature":
				files["/stable/test.tgz.sig"][0] ^= 1
			case "payload":
				files["/stable/test.tgz"][0] ^= 1
			}
			m := New(t.TempDir(), s)
			err := m.install(context.Background(), Release{Product: "tailscale", Version: "1.2.3", URL: "https://pkgs.tailscale.com/stable/test.tgz"})
			if failure != "" {
				if err == nil {
					t.Fatal("invalid signature accepted")
				}
				status, _ := m.Status("tailscale")
				if status.Current != "" {
					t.Fatal(status)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			paths, e := m.Paths("tailscale")
			if e != nil || len(paths) != 2 {
				t.Fatal(paths, e)
			}
			for _, p := range paths {
				b, e := os.ReadFile(p)
				if e != nil || string(b) != string(hostExecutable("elf")) {
					t.Fatal(p, e)
				}
			}
		})
	}
}
func TestNetworkFailureAndRedirect(t *testing.T) {
	for _, mode := range []string{"interrupted", "redirect"} {
		t.Run(mode, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "redirect" {
					http.Redirect(w, r, "https://example.invalid/asset", 302)
					return
				}
				w.Header().Set("Content-Length", "500")
				w.Write([]byte("short"))
			}))
			defer srv.Close()
			s := NewOfficialSource()
			s.client = srv.Client()
			s.hosts = map[string]bool{srv.Listener.Addr().String(): true}
			m := New(t.TempDir(), s)
			if m.install(context.Background(), release(srv.URL, "1.0.0", []byte("short"))) == nil {
				t.Fatal(mode)
			}
			v, _ := m.Status("cloudflared")
			if v.Current != "" {
				t.Fatal(v)
			}
		})
	}
}
func TestSingleOperationAcrossManagers(t *testing.T) {
	m, server, b := fixture(t)
	other := New(m.root, m.source)
	e := m.locked(context.Background(), func() error {
		if !errors.Is(other.install(context.Background(), release(server.URL, "1.0.0", *b)), ErrBusy) {
			t.Fatal("parallel operation accepted")
		}
		return nil
	})
	if e != nil {
		t.Fatal(e)
	}
}
func TestCancellationAndActivation(t *testing.T) {
	m, server, b := fixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if m.install(ctx, release(server.URL, "1.0.0", *b)) == nil {
		t.Fatal("cancel accepted")
	}
	if e := m.install(context.Background(), release(server.URL, "1.0.0", *b)); e != nil {
		t.Fatal(e)
	}
	called := false
	e := m.Activate(context.Background(), "cloudflared", func(_ context.Context, p map[string]string) error {
		called = true
		if filepath.Base(p["cloudflared"]) != "cloudflared" {
			t.Fatal(p)
		}
		return errors.New("provider refused")
	})
	if !called || e == nil {
		t.Fatal("activation failure hidden")
	}
}
func TestOfficialMetadata(t *testing.T) {
	s, files, _ := signedFixture(t)
	files["/stable/"] = []byte(`{"TarballsVersion":"1.2.3","Tarballs":{"amd64":"tailscale_1.2.3_amd64.tgz"}}`)
	s.arch = "amd64"
	r, e := s.Latest(context.Background(), "tailscale")
	if e != nil || r.Version != "1.2.3" {
		t.Fatal(r, e)
	}
	files["/stable/"] = []byte(`{"TarballsVersion":"1.2.3","Tarballs":{"amd64":"../../evil"}}`)
	if _, e = s.Latest(context.Background(), "tailscale"); e == nil {
		t.Fatal("unsafe metadata")
	}
	files["/repos/cloudflare/cloudflared/releases/latest"] = []byte(`{"tag_name":"2026.1.0","body":"cloudflared-linux-amd64: aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","assets":[{"name":"cloudflared-linux-amd64","browser_download_url":"https://github.com/cloudflare/cloudflared/releases/download/2026.1.0/cloudflared-linux-amd64"}]}`)
	r, e = s.Latest(context.Background(), "cloudflared")
	if e != nil || len(r.SHA256) != 64 {
		t.Fatal(r, e)
	}
}
func TestInterruptedOperationStatus(t *testing.T) {
	m, _, _ := fixture(t)
	if e := os.MkdirAll(m.root, 0700); e != nil {
		t.Fatal(e)
	}
	if e := m.save(saved{Status: Status{Product: "cloudflared", Phase: "downloading"}}); e != nil {
		t.Fatal(e)
	}
	s, e := m.Status("cloudflared")
	if e != nil || s.Phase != "failed" || s.Error == "" {
		t.Fatal(s, e)
	}
}
