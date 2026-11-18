package binaries

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"testing"
)

func serverLayer(t *testing.T) []byte {
	t.Helper()
	var b bytes.Buffer
	z := gzip.NewWriter(&b)
	w := tar.NewWriter(z)
	body := hostExecutable("a")
	for _, h := range []*tar.Header{{Name: "go/", Typeflag: tar.TypeDir, Mode: 0755}, {Name: "go/bin/", Typeflag: tar.TypeDir, Mode: 0755}, {Name: "go/bin/netbird-server", Typeflag: tar.TypeReg, Mode: 0755, Size: int64(len(body))}} {
		if e := w.WriteHeader(h); e != nil {
			t.Fatal(e)
		}
	}
	w.Write(body)
	w.Close()
	z.Close()
	return b.Bytes()
}

func digestOf(b []byte) string { return fmt.Sprintf("sha256:%x", sha256.Sum256(b)) }

// registry serves the release feed, then the image index, the platform's
// manifest and its top layer, as GitHub and ghcr.io do.
type registry struct {
	t              *testing.T
	layer          []byte
	tamperManifest bool
	wrongPlatform  bool
	sawAuth        map[string]string
}

func (g *registry) RoundTrip(r *http.Request) (*http.Response, error) {
	layerDigest := digestOf(g.layer)
	manifest := []byte(fmt.Sprintf(`{"mediaType":%q,"layers":[{"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip","digest":"sha256:%s","size":10},{"mediaType":"application/vnd.oci.image.layer.v1.tar+gzip","digest":%q,"size":%d}]}`, ociManifest, strings.Repeat("b", 64), layerDigest, len(g.layer)))
	manifestDigest := digestOf(manifest)
	if g.tamperManifest {
		manifest = append(manifest, ' ')
	}
	arch := runtime.GOARCH
	if g.wrongPlatform {
		arch = "s390x"
	}
	index := fmt.Sprintf(`{"mediaType":%q,"manifests":[{"mediaType":%q,"digest":%q,"platform":{"architecture":%q,"os":"linux","variant":%q}},{"mediaType":%q,"digest":"sha256:%s","platform":{"architecture":"unknown","os":"unknown"}}]}`, ociIndex, ociManifest, manifestDigest, arch, serverPlatforms[runtime.GOARCH].variant, ociManifest, strings.Repeat("c", 64))
	g.sawAuth[r.URL.Path] = r.Header.Get("Authorization")
	body := []byte{}
	switch r.URL.String() {
	case "https://api.github.com/repos/netbirdio/netbird/releases/latest":
		body = []byte(`{"tag_name":"v0.81.5"}`)
	case "https://ghcr.io/token?scope=repository:netbirdio/netbird-server:pull":
		body = []byte(`{"token":"anon"}`)
	case "https://ghcr.io/v2/netbirdio/netbird-server/manifests/0.81.5":
		body = []byte(index)
	case "https://ghcr.io/v2/netbirdio/netbird-server/manifests/" + manifestDigest:
		body = manifest
	case "https://ghcr.io/v2/netbirdio/netbird-server/blobs/" + layerDigest:
		body = g.layer
	default:
		g.t.Fatal("unexpected URL", r.URL)
	}
	return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body))}, nil
}

func TestNetBirdServerImageLayerPinnedByDigestChain(t *testing.T) {
	g := &registry{t: t, layer: serverLayer(t), sawAuth: map[string]string{}}
	s := NewOfficialSource()
	s.arch = runtime.GOARCH
	s.client = &http.Client{Transport: g}
	r, e := s.Latest(context.Background(), "netbird-server")
	if e != nil || r.Version != "0.81.5" || r.SHA256 != strings.TrimPrefix(digestOf(g.layer), "sha256:") {
		t.Fatal("server layer pin", r, e)
	}
	if g.sawAuth["/v2/netbirdio/netbird-server/manifests/0.81.5"] != "Bearer anon" {
		t.Fatal("registry token not sent")
	}
	m := New(t.TempDir(), s)
	if e = m.install(context.Background(), r); e != nil {
		t.Fatal(e)
	}
	paths, e := m.Paths("netbird-server")
	if e != nil || !strings.HasSuffix(paths["netbird-server"], "/netbird-server") {
		t.Fatal("installed server", paths, e)
	}
	a := NewRuntimeAdapter(m)
	if b, e := a.Resolve(context.Background(), "netbird-server"); e != nil || b.Version != "0.81.5" {
		t.Fatal("runtime pin", b, e)
	}

	g.tamperManifest = true
	if _, e = s.Latest(context.Background(), "netbird-server"); e == nil || !strings.Contains(e.Error(), "digest") {
		t.Fatal("tampered manifest accepted", e)
	}
	g.tamperManifest, g.wrongPlatform = false, true
	if _, e = s.Latest(context.Background(), "netbird-server"); e == nil {
		t.Fatal("missing platform accepted")
	}
}

func TestNetBirdServerLayerBytesMustMatchDigest(t *testing.T) {
	g := &registry{t: t, layer: serverLayer(t), sawAuth: map[string]string{}}
	s := NewOfficialSource()
	s.arch = runtime.GOARCH
	s.client = &http.Client{Transport: g}
	r, e := s.Latest(context.Background(), "netbird-server")
	if e != nil {
		t.Fatal(e)
	}
	r.SHA256 = strings.Repeat("0", 64)
	if e = New(t.TempDir(), s).install(context.Background(), r); e == nil {
		t.Fatal("layer with another digest accepted")
	}
}

func TestNetBirdServerPlatforms(t *testing.T) {
	for arch, want := range map[string]string{"amd64": "amd64/", "arm64": "arm64/", "arm": "arm/v7"} {
		p := serverPlatforms[arch]
		if p.arch+"/"+p.variant != want {
			t.Fatal(arch, p)
		}
	}
	if _, ok := serverPlatforms["386"]; ok {
		t.Fatal("unsupported architecture mapped")
	}
}
