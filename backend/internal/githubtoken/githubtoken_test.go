package githubtoken

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const sample = "not-a-real-token-for-tests-abcd"

func TestStoreKeepsThePrivateTokenAndOnlyShowsItsEnd(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if s.Load() != "" || s.Status().Saved {
		t.Fatal("empty store reports a token")
	}
	if e := s.Save(sample); e != nil {
		t.Fatal(e)
	}
	st, e := os.Stat(filepath.Join(s.Dir, FileName))
	if e != nil || st.Mode().Perm() != 0600 {
		t.Fatal("token file is not private", st, e)
	}
	if s.Load() != sample {
		t.Fatal("token not read back")
	}
	if got := s.Status(); !got.Saved || got.Last4 != "abcd" {
		t.Fatal(got)
	}
	if e := s.Remove(); e != nil || s.Load() != "" {
		t.Fatal("token kept after remove", e)
	}
	if e := s.Remove(); e != nil {
		t.Fatal("removing twice fails", e)
	}
}

func TestStoreRefusesAWiderFile(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if e := os.WriteFile(filepath.Join(s.Dir, FileName), []byte(sample), 0644); e != nil {
		t.Fatal(e)
	}
	if s.Load() != "" {
		t.Fatal("a world-readable token is used")
	}
}

func TestSaveRejectsMalformedTokens(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	for _, bad := range []string{"", "short", "has space inside token", strings.Repeat("a", 300), "line\nbreak-token-value"} {
		if e := s.Save(bad); !errors.Is(e, ErrMalformed) {
			t.Fatal(bad, e)
		}
	}
}

func TestBearerGoesOnlyToGitHubOverHTTPS(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://api.github.com/repos/a/b/releases/latest":     true,
		"https://github.com/a/b/releases/download/v1/x":        true,
		"https://codeload.github.com/a/b/tar.gz/abc":           true,
		"http://api.github.com/rate_limit":                     false,
		"https://release-assets.githubusercontent.com/x?sig=1": false,
		"https://objects.githubusercontent.com/x":              false,
		"https://ghcr.io/v2/":                                  false,
		"https://pkgs.tailscale.com/stable/":                   false,
		"https://api.github.com.example.org/":                  false,
		"https://user@api.github.com/":                         false,
	} {
		u, _ := url.Parse(raw)
		r := &http.Request{URL: u, Header: http.Header{}}
		Authorize(r, sample)
		if got := r.Header.Get("Authorization") == "Bearer "+sample; got != want {
			t.Fatal(raw, got)
		}
	}
	u, _ := url.Parse("https://api.github.com/")
	r := &http.Request{URL: u, Header: http.Header{}}
	Authorize(r, "")
	if r.Header.Get("Authorization") != "" {
		t.Fatal("anonymous request carries a header")
	}
}

func TestRedirectToAnotherHostDropsTheToken(t *testing.T) {
	from, _ := url.Parse("https://github.com/a/b/releases/download/v1/x")
	to, _ := url.Parse("https://release-assets.githubusercontent.com/x?sig=1")
	r := &http.Request{URL: to, Header: http.Header{"Authorization": {"Bearer " + sample}}}
	StripOnRedirect(r, []*http.Request{{URL: from}})
	if r.Header.Get("Authorization") != "" {
		t.Fatal("token followed a redirect off GitHub")
	}
}

func TestValidateReadsTheNewLimit(t *testing.T) {
	var seen string
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		seen = r.URL.String() + " " + r.Header.Get("Authorization")
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"resources":{"core":{"limit":5000,"remaining":4999,"reset":1792000000}}}`))}, nil
	})}
	l, e := Validate(context.Background(), client, sample)
	if e != nil || l.Limit != 5000 || l.Remaining != 4999 || !l.Reset.Equal(time.Unix(1792000000, 0)) {
		t.Fatal(l, e)
	}
	if seen != "https://api.github.com/rate_limit Bearer "+sample {
		t.Fatal(seen)
	}
}

func TestValidateNamesARejectedToken(t *testing.T) {
	for status, want := range map[int]error{401: ErrInvalid, 500: ErrUnavailable} {
		client := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
		})}
		if _, e := Validate(context.Background(), client, sample); !errors.Is(e, want) {
			t.Fatal(status, e)
		}
	}
}

func TestClassifyNamesLimitsAndRejectedTokens(t *testing.T) {
	now := time.Unix(1791990000, 0)
	for _, row := range []struct {
		name   string
		status int
		header map[string]string
		body   string
		token  bool
		kind   Kind
		reset  time.Time
	}{
		{"primary limit", 403, map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "1792000000"}, `{"message":"API rate limit exceeded"}`, false, RateLimited, time.Unix(1792000000, 0)},
		{"too many", 429, map[string]string{"Retry-After": "60"}, ``, false, RateLimited, now.Add(time.Minute)},
		{"secondary", 403, map[string]string{"X-RateLimit-Remaining": "4000"}, `{"message":"You have exceeded a secondary rate limit"}`, true, RateLimited, now.Add(time.Minute)},
		{"secondary with retry", 403, map[string]string{"Retry-After": "120"}, `{"message":"You have exceeded a secondary rate limit"}`, false, RateLimited, now.Add(2 * time.Minute)},
		{"bad token", 401, nil, `{"message":"Bad credentials"}`, true, TokenInvalid, time.Time{}},
		{"anonymous 401", 401, nil, ``, false, None, time.Time{}},
		{"refused", 403, map[string]string{"X-RateLimit-Remaining": "12"}, `{"message":"Forbidden"}`, false, Refused, time.Time{}},
		{"ok", 200, nil, ``, false, None, time.Time{}},
		{"server", 502, nil, ``, false, None, time.Time{}},
	} {
		t.Run(row.name, func(t *testing.T) {
			h := http.Header{}
			for k, v := range row.header {
				h.Set(k, v)
			}
			res := &http.Response{StatusCode: row.status, Header: h, Body: io.NopCloser(strings.NewReader(row.body))}
			kind, reset := classifyAt(res, row.token, now)
			if kind != row.kind || !reset.Equal(row.reset) {
				t.Fatal(kind, reset)
			}
		})
	}
}
