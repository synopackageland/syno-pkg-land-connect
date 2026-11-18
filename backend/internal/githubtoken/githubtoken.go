// Package githubtoken keeps the administrator's optional GitHub token and
// decides which requests carry it. Without a token every GitHub call stays
// anonymous (60 requests an hour per public address); with one, GitHub counts
// the calls against the token's owner instead. The token is never logged and
// never returned to a browser; only its last four characters are shown.
package githubtoken

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// FileName is the token file inside the package's private data directory.
const FileName = "github-token"

var (
	ErrMalformed   = errors.New("github token malformed")
	ErrInvalid     = errors.New("github token invalid or expired")
	ErrUnavailable = errors.New("github unavailable")
)

// Store is the 0600 token file owned by the package account.
type Store struct{ Dir string }

type Status struct {
	Saved bool   `json:"saved"`
	Last4 string `json:"last4,omitempty"`
}

func (s Store) path() string { return filepath.Join(s.Dir, FileName) }

// Load returns the saved token, or "" when none is saved or the file is not
// a private regular file of this account.
func (s Store) Load() string {
	if s.Dir == "" {
		return ""
	}
	f, e := os.OpenFile(s.path(), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return ""
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || st.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return ""
	}
	b, e := io.ReadAll(io.LimitReader(f, 512))
	if e != nil {
		return ""
	}
	t := strings.TrimSpace(string(b))
	if wellFormed(t) != nil {
		return ""
	}
	return t
}

func (s Store) Status() Status {
	t := s.Load()
	if t == "" {
		return Status{}
	}
	return Status{Saved: true, Last4: t[len(t)-4:]}
}

// wellFormed accepts the printable, space-free shapes GitHub issues
// (ghp_…, github_pat_…); GitHub itself decides whether the token is valid.
func wellFormed(t string) error {
	if len(t) < 20 || len(t) > 255 {
		return ErrMalformed
	}
	for _, c := range t {
		if c <= ' ' || c > '~' {
			return ErrMalformed
		}
	}
	return nil
}

// Save replaces the token atomically with a 0600 file.
func (s Store) Save(token string) error {
	if e := wellFormed(token); e != nil {
		return e
	}
	if e := os.MkdirAll(s.Dir, 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(s.Dir, ".github-token-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if e = f.Chmod(0600); e == nil {
		if _, e = f.WriteString(token); e == nil {
			e = f.Sync()
		}
	}
	if ce := f.Close(); e == nil {
		e = ce
	}
	if e != nil {
		return e
	}
	return os.Rename(f.Name(), s.path())
}

func (s Store) Remove() error {
	if e := os.Remove(s.path()); e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	return nil
}

// hosts are where GitHub accepts the token: the API, release downloads and
// repository archives. Signed asset hosts reached by redirect never get it.
var hosts = map[string]bool{"api.github.com": true, "github.com": true, "codeload.github.com": true}

func applies(r *http.Request) bool {
	return r.URL != nil && r.URL.Scheme == "https" && r.URL.User == nil && hosts[r.URL.Host]
}

// Authorize adds the token to r when r goes to GitHub over HTTPS.
func Authorize(r *http.Request, token string) {
	if token != "" && applies(r) {
		r.Header.Set("Authorization", "Bearer "+token)
	}
}

// StripOnRedirect is for a client's CheckRedirect: a redirect off GitHub
// never carries the token, whatever the client would otherwise forward.
func StripOnRedirect(r *http.Request, via []*http.Request) {
	if len(via) > 0 && via[0].URL.Host != r.URL.Host && !applies(r) {
		r.Header.Del("Authorization")
	}
}

type Limit struct {
	Limit     int       `json:"limit"`
	Remaining int       `json:"remaining"`
	Reset     time.Time `json:"reset"`
}

// Validate asks GitHub once for the token's core API limit.
func Validate(ctx context.Context, client *http.Client, token string) (Limit, error) {
	var out Limit
	if e := wellFormed(token); e != nil {
		return out, e
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/rate_limit", nil)
	if e != nil {
		return out, e
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	Authorize(req, token)
	res, e := client.Do(req)
	if e != nil {
		return out, ErrUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusUnauthorized {
		return out, ErrInvalid
	}
	if res.StatusCode != http.StatusOK {
		return out, ErrUnavailable
	}
	var body struct {
		Resources struct {
			Core struct {
				Limit, Remaining int
				Reset            int64
			}
		}
	}
	if json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&body) != nil || body.Resources.Core.Limit <= 0 {
		return out, ErrUnavailable
	}
	c := body.Resources.Core
	return Limit{Limit: c.Limit, Remaining: c.Remaining, Reset: time.Unix(c.Reset, 0)}, nil
}

// Kind names why GitHub refused a request.
type Kind int

const (
	None Kind = iota
	RateLimited
	TokenInvalid
	Refused
)

// Classify reads a GitHub answer: a spent quota (403/429 with no requests
// remaining), a secondary rate limit, a rejected token (401 when one was
// sent) or another 403 refusal. Reset is when to retry a limit; it peeks at most
// 4 KiB of a 403 body and leaves the body readable.
func Classify(res *http.Response, withToken bool) (Kind, time.Time) {
	return classifyAt(res, withToken, time.Now())
}

func classifyAt(res *http.Response, withToken bool, now time.Time) (Kind, time.Time) {
	h := res.Header
	switch res.StatusCode {
	case http.StatusUnauthorized:
		if withToken {
			return TokenInvalid, time.Time{}
		}
		return None, time.Time{}
	case http.StatusForbidden, http.StatusTooManyRequests:
	default:
		return None, time.Time{}
	}
	secondary := false
	if res.StatusCode == http.StatusForbidden && h.Get("X-RateLimit-Remaining") != "0" {
		peek, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		res.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(peek), res.Body), res.Body}
		if !strings.Contains(strings.ToLower(string(peek)), "secondary rate limit") {
			return Refused, time.Time{}
		}
		secondary = true
	}
	if after, e := strconv.Atoi(h.Get("Retry-After")); e == nil && after > 0 {
		return RateLimited, now.Add(time.Duration(after) * time.Second).Truncate(time.Second)
	}
	if !secondary {
		if reset, e := strconv.ParseInt(h.Get("X-RateLimit-Reset"), 10, 64); e == nil && reset > 0 {
			return RateLimited, time.Unix(reset, 0)
		}
		return RateLimited, now.Add(time.Hour).Truncate(time.Second)
	}
	// GitHub asks to wait at least a minute after a secondary limit.
	return RateLimited, now.Add(time.Minute).Truncate(time.Second)
}
