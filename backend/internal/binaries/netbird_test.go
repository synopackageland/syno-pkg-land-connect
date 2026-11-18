package binaries

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type nbTransport func(*http.Request) (*http.Response, error)

func (f nbTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestNetBirdLatestStableOfficialAssetsForEveryArchitecture(t *testing.T) {
	for arch, asset := range map[string]string{"amd64": "amd64", "arm64": "arm64", "arm": "armv6"} {
		t.Run(arch, func(t *testing.T) {
			name := "netbird_0.80.0_linux_" + asset + ".tar.gz"
			base := "https://github.com/netbirdio/netbird/releases/download/v0.80.0/"
			bad := false
			draft := false
			s := NewOfficialSource()
			s.arch = arch
			s.client = &http.Client{Transport: nbTransport(func(r *http.Request) (*http.Response, error) {
				body := ""
				switch r.URL.String() {
				case "https://api.github.com/repos/netbirdio/netbird/releases/latest":
					body = fmt.Sprintf(`{"tag_name":"v0.80.0","draft":%t,"assets":[{"name":%q,"browser_download_url":%q},{"name":"netbird_0.80.0_checksums.txt","browser_download_url":%q}]}`, draft, name, base+name, base+"netbird_0.80.0_checksums.txt")
				case base + "netbird_0.80.0_checksums.txt":
					if !bad {
						body = strings.Repeat("a", 64) + "  " + name + "\n"
					}
				default:
					t.Fatal("unexpected download URL", r.URL)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			r, e := s.Latest(context.Background(), "netbird")
			if e != nil || r.Version != "0.80.0" || r.URL != base+name || r.SHA256 != strings.Repeat("a", 64) {
				t.Fatal("official pin", r, e)
			}
			bad = true
			if _, e = s.Latest(context.Background(), "netbird"); e == nil {
				t.Fatal("missing official checksum accepted")
			}
			bad = false
			draft = true
			if _, e = s.Latest(context.Background(), "netbird"); e == nil {
				t.Fatal("draft accepted")
			}
		})
	}
}

func TestNetBirdVerifiedArchiveAndRuntimePin(t *testing.T) {
	payload := archive(t, "netbird", '0')
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.Write(payload) }))
	defer server.Close()
	source := NewOfficialSource()
	source.client = server.Client()
	source.hosts = map[string]bool{server.Listener.Addr().String(): true}
	m := New(t.TempDir(), source)
	r := Release{Product: "netbird", Version: "0.80.0", URL: server.URL, SHA256: fmt.Sprintf("%x", sha256.Sum256(payload))}
	if e := m.install(context.Background(), r); e != nil {
		t.Fatal(e)
	}
	a := NewRuntimeAdapter(m)
	b, e := a.Resolve(context.Background(), "netbird")
	if e != nil || b.Version != "0.80.0" || filepath.Base(b.Path) != "netbird" {
		t.Fatal("verified client unavailable", e)
	}
	if e = os.WriteFile(b.Path, []byte("tampered"), 0700); e != nil {
		t.Fatal(e)
	}
	if _, e = a.Resolve(context.Background(), "netbird"); e == nil {
		t.Fatal("tampered client executable accepted")
	}
}
func TestNetBirdChecksumMismatchNeverBecomesCandidate(t *testing.T) {
	m, server, _ := fixture(t)
	r := Release{Product: "netbird", Version: "0.80.0", URL: server.URL, SHA256: fmt.Sprintf("%064d", 0)}
	if e := m.install(context.Background(), r); e == nil {
		t.Fatal("wrong archive digest accepted")
	}
	s, e := m.Status("netbird")
	if e != nil || s.Current != "" {
		t.Fatal("failed candidate selected", e)
	}
}
