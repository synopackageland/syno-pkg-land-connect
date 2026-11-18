// Package binaries manages verified, package-owned connector candidates. It never
// executes downloads; a provider must explicitly activate a candidate.
package binaries

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"dsmapp/internal/githubtoken"
)

type Release struct {
	Product string `json:"product"`
	Version string `json:"version"`
	URL     string `json:"-"`
	SHA256  string `json:"-"`
	// bearer is the registry's anonymous pull token for URL, never saved.
	bearer string
}

// Source is injectable for tests. A source must authenticate all
// bytes before returning from Download. Request parameters never select a source.
type Source interface {
	Latest(context.Context, string) (Release, error)
	Download(context.Context, Release, string) (string, error)
}
type OfficialSource struct {
	client    *http.Client
	hosts     map[string]bool
	maxBinary int64
	arch      string
	roots     []byte
	// token reads the administrator's optional GitHub token on every call, so a
	// saved or removed token applies without restarting the daemon.
	token func() string
}

// SourceError names a release feed refusal the page explains on its own:
// github_rate_limited (the quota is spent or a secondary limit applies;
// RetryAt is when to retry), github_token_invalid (GitHub rejected the saved
// token) or github_refused.
type SourceError struct {
	Code    string
	RetryAt time.Time
}

func (e *SourceError) Error() string { return "release feed: " + e.Code }

// refusal names GitHub's rate limits and a rejected token.
func refusal(res *http.Response, withToken bool) error {
	switch kind, reset := githubtoken.Classify(res, withToken); kind {
	case githubtoken.RateLimited:
		return &SourceError{Code: "github_rate_limited", RetryAt: reset}
	case githubtoken.TokenInvalid:
		return &SourceError{Code: "github_token_invalid"}
	case githubtoken.Refused:
		return &SourceError{Code: "github_refused"}
	}
	return nil
}

func NewOfficialSource() *OfficialSource {
	return &OfficialSource{client: &http.Client{Timeout: 5 * time.Minute}, hosts: map[string]bool{"api.github.com": true, "github.com": true, "release-assets.githubusercontent.com": true, "pkgs.tailscale.com": true, "ghcr.io": true, "pkg-containers.githubusercontent.com": true}, maxBinary: 256 << 20, arch: runtime.GOARCH, roots: rootPEM}
}

// UseToken sends the token load returns to GitHub hosts; "" stays anonymous.
func (s *OfficialSource) UseToken(load func() string) *OfficialSource {
	s.token = load
	return s
}
func (s *OfficialSource) allowed(u *url.URL) bool {
	return u.Scheme == "https" && u.User == nil && s.hosts[u.Host] && u.Fragment == ""
}
func (s *OfficialSource) fetch(ctx context.Context, addr string, limit int64, w io.Writer) error {
	return s.fetchWith(ctx, addr, limit, w, nil)
}

// fetchWith sends headers to addr only: the client drops Authorization on a
// redirect to another host, such as the registry's blob storage.
func (s *OfficialSource) fetchWith(ctx context.Context, addr string, limit int64, w io.Writer, headers map[string]string) error {
	u, e := url.Parse(addr)
	if e != nil || !s.allowed(u) {
		return errors.New("download host rejected")
	}
	c := *s.client
	c.CheckRedirect = func(r *http.Request, via []*http.Request) error {
		if len(via) >= 5 || !s.allowed(r.URL) {
			return errors.New("redirect rejected")
		}
		githubtoken.StripOnRedirect(r, via)
		return nil
	}
	req, e := http.NewRequestWithContext(ctx, "GET", addr, nil)
	if e != nil {
		return e
	}
	req.Header.Set("User-Agent", "SynoPkgConnect-binaries")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	token := ""
	if s.token != nil {
		token = s.token()
	}
	githubtoken.Authorize(req, token)
	res, e := c.Do(req)
	if e != nil {
		// url.Error includes the URL, which may carry signed query credentials.
		var requestError *url.Error
		if errors.As(e, &requestError) {
			e = requestError.Err
		}
		return fmt.Errorf("download from %s failed: %w", u.Hostname(), e)
	}
	defer res.Body.Close()
	if u.Hostname() == "api.github.com" || u.Hostname() == "github.com" {
		if e = refusal(res, token != "" && req.Header.Get("Authorization") == "Bearer "+token); e != nil {
			return e
		}
	}
	if res.StatusCode != 200 {
		reason := http.StatusText(res.StatusCode)
		if res.StatusCode == http.StatusTooManyRequests {
			reason = "rate limited; retry later"
		}
		return fmt.Errorf("%s returned HTTP %d (%s)", u.Hostname(), res.StatusCode, reason)
	}
	if res.ContentLength > limit {
		return errors.New("download too large")
	}
	n, e := io.Copy(w, io.LimitReader(res.Body, limit+1))
	if e != nil {
		return fmt.Errorf("download from %s interrupted: %w", u.Hostname(), e)
	}
	if n > limit {
		return errors.New("download too large")
	}
	return nil
}
func (s *OfficialSource) get(ctx context.Context, u string, limit int64) ([]byte, error) {
	var b strings.Builder
	e := s.fetch(ctx, u, limit, &b)
	return []byte(b.String()), e
}

// assetArchitectures names each product's official Linux asset per GOARCH, one
// row per SPK architecture (x86_64, aarch64, armv7). NetBird publishes no
// armv7 build; its armv6 build runs on DSM armv7.
var assetArchitectures = map[string]map[string]string{
	"amd64": {"cloudflared": "amd64", "tailscale": "amd64", "netbird": "amd64", "netbird-server": "amd64"},
	"arm64": {"cloudflared": "arm64", "tailscale": "arm64", "netbird": "arm64", "netbird-server": "arm64"},
	"arm":   {"cloudflared": "arm", "tailscale": "arm", "netbird": "armv6", "netbird-server": "arm"},
}

func assetArch(product, goarch string) (string, error) {
	products, ok := assetArchitectures[goarch]
	if !ok {
		return "", errors.New("unsupported architecture")
	}
	asset, ok := products[product]
	if !ok {
		return "", errors.New("unknown binary")
	}
	return asset, nil
}

// netbirdMeta is the latest stable NetBird release on GitHub; the server image
// carries the same version as its tag.
type netbirdMeta struct {
	Tag               string `json:"tag_name"`
	Draft, Prerelease bool
	Assets            []struct {
		Name string
		URL  string `json:"browser_download_url"`
	}
}

func (s *OfficialSource) netbirdRelease(ctx context.Context) (netbirdMeta, error) {
	var meta netbirdMeta
	raw, e := s.get(ctx, "https://api.github.com/repos/netbirdio/netbird/releases/latest", 2<<20)
	if e != nil {
		return meta, e
	}
	if json.Unmarshal(raw, &meta) != nil || meta.Draft || meta.Prerelease || !strings.HasPrefix(meta.Tag, "v") {
		return meta, errors.New("invalid release metadata")
	}
	if _, e = compare(strings.TrimPrefix(meta.Tag, "v"), strings.TrimPrefix(meta.Tag, "v")); e != nil {
		return meta, e
	}
	return meta, nil
}

func (s *OfficialSource) Latest(ctx context.Context, product string) (Release, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	arch, e := assetArch(product, s.arch)
	if e != nil {
		return Release{}, e
	}
	r := Release{Product: product}
	switch product {
	case "netbird-server":
		meta, e := s.netbirdRelease(ctx)
		if e != nil {
			return r, e
		}
		return s.serverRelease(ctx, strings.TrimPrefix(meta.Tag, "v"))
	case "netbird":
		meta, e := s.netbirdRelease(ctx)
		if e != nil {
			return r, e
		}
		r.Version = strings.TrimPrefix(meta.Tag, "v")
		name := "netbird_" + r.Version + "_linux_" + arch + ".tar.gz"
		base := "https://github.com/netbirdio/netbird/releases/download/" + meta.Tag + "/"
		checksumName := "netbird_" + r.Version + "_checksums.txt"
		checksumURL := ""
		for _, a := range meta.Assets {
			if a.Name == name && a.URL == base+name {
				r.URL = a.URL
			}
			if a.Name == checksumName && a.URL == base+checksumName {
				checksumURL = a.URL
			}
		}
		if r.URL == "" || checksumURL == "" {
			return r, errors.New("official NetBird release asset missing")
		}
		checksums, e := s.get(ctx, checksumURL, 256<<10)
		if e != nil {
			return r, e
		}
		for _, line := range strings.Split(string(checksums), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
				if r.SHA256 != "" {
					return r, errors.New("ambiguous official checksum")
				}
				r.SHA256 = strings.ToLower(fields[0])
			}
		}
		if h, e := hex.DecodeString(r.SHA256); e != nil || len(h) != 32 {
			return r, errors.New("official checksum missing")
		}
	case "cloudflared":
		raw, e := s.get(ctx, "https://api.github.com/repos/cloudflare/cloudflared/releases/latest", 2<<20)
		if e != nil {
			return r, e
		}
		var meta struct {
			Tag        string `json:"tag_name"`
			Body       string `json:"body"`
			Draft      bool
			Prerelease bool
			Assets     []struct {
				Name   string
				URL    string `json:"browser_download_url"`
				Digest string
			}
		}
		if json.Unmarshal(raw, &meta) != nil || meta.Draft || meta.Prerelease {
			return r, errors.New("invalid release metadata")
		}
		r.Version = meta.Tag
		name := "cloudflared-linux-" + arch
		for _, a := range meta.Assets {
			if a.Name == name {
				r.URL = a.URL
				if strings.HasPrefix(a.Digest, "sha256:") {
					r.SHA256 = strings.TrimPrefix(a.Digest, "sha256:")
				}
			}
		}
		if r.SHA256 == "" {
			re := regexp.MustCompile(`(?m)^` + regexp.QuoteMeta(name) + `:\s*([a-fA-F0-9]{64})\s*$`)
			m := re.FindStringSubmatch(meta.Body)
			if len(m) == 2 {
				r.SHA256 = strings.ToLower(m[1])
			}
		}
		if r.URL != "https://github.com/cloudflare/cloudflared/releases/download/"+r.Version+"/"+name {
			return r, errors.New("unexpected release asset")
		}
		if h, e := hex.DecodeString(r.SHA256); e != nil || len(h) != 32 {
			return r, errors.New("official checksum missing")
		}
	case "tailscale":
		raw, e := s.get(ctx, "https://pkgs.tailscale.com/stable/?mode=json", 2<<20)
		if e != nil {
			return r, e
		}
		var meta struct {
			TarballsVersion string
			Tarballs        map[string]string
		}
		if json.Unmarshal(raw, &meta) != nil {
			return r, errors.New("invalid release metadata")
		}
		r.Version = meta.TarballsVersion
		name := "tailscale_" + r.Version + "_" + arch + ".tgz"
		if meta.Tarballs[arch] != name {
			return r, errors.New("unexpected release asset")
		}
		r.URL = "https://pkgs.tailscale.com/stable/" + name
	default:
		return r, errors.New("unknown binary")
	}
	if _, e := compare(r.Version, r.Version); e != nil {
		return r, e
	}
	return r, nil
}
func (s *OfficialSource) Download(ctx context.Context, r Release, path string) (string, error) {
	f, e := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return "", e
	}
	h := sha256.New()
	var headers map[string]string
	if r.bearer != "" {
		headers = map[string]string{"Authorization": "Bearer " + r.bearer}
	}
	e = s.fetchWith(ctx, r.URL, s.maxBinary, io.MultiWriter(f, h), headers)
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e != nil {
		return "", e
	}
	if closeErr != nil {
		return "", closeErr
	}
	switch r.Product {
	case "cloudflared", "netbird", "netbird-server":
		if !strings.EqualFold(hex.EncodeToString(h.Sum(nil)), r.SHA256) {
			return "", errors.New("checksum verification failed")
		}
		return "sha256", nil
	case "tailscale":
		if e = s.verifyTailscale(ctx, r.URL, path); e != nil {
			return "", e
		}
		return "distsign", nil
	}
	return "", errors.New("unknown binary")
}

var versionRE = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func compare(a, b string) (int, error) {
	if !versionRE.MatchString(a) || !versionRE.MatchString(b) {
		return 0, errors.New("invalid stable version")
	}
	aa, bb := strings.Split(a, "."), strings.Split(b, ".")
	for i := range aa {
		x, e := strconv.ParseUint(aa[i], 10, 32)
		if e != nil {
			return 0, e
		}
		y, e := strconv.ParseUint(bb[i], 10, 32)
		if e != nil {
			return 0, e
		}
		if x < y {
			return -1, nil
		}
		if x > y {
			return 1, nil
		}
	}
	return 0, nil
}
func validProduct(p string) error {
	if p != "tailscale" && p != "cloudflared" && p != "netbird" && p != "netbird-server" {
		return fmt.Errorf("unknown binary")
	}
	return nil
}
