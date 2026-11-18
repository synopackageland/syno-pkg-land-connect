package binaries

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// GitHub's unauthenticated API allows 60 requests an hour per address; a spent
// quota or a refusal is named so the page does not blame the network.
func TestGitHubRateLimitAndRefusalAreNamed(t *testing.T) {
	for _, row := range []struct {
		name    string
		status  int
		header  map[string]string
		code    string
		retryAt time.Time
	}{
		{"spent quota", 403, map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "1792000000"}, "github_rate_limited", time.Unix(1792000000, 0)},
		{"too many requests", 429, map[string]string{"X-RateLimit-Reset": "1792000060"}, "github_rate_limited", time.Unix(1792000060, 0)},
		{"refused", 403, map[string]string{"X-RateLimit-Remaining": "12"}, "github_refused", time.Time{}},
		{"server error", 502, nil, "", time.Time{}},
	} {
		t.Run(row.name, func(t *testing.T) {
			s := NewOfficialSource()
			s.client = &http.Client{Transport: nbTransport(func(r *http.Request) (*http.Response, error) {
				h := http.Header{}
				for k, v := range row.header {
					h.Set(k, v)
				}
				return &http.Response{StatusCode: row.status, Header: h, Body: io.NopCloser(strings.NewReader(`{"message":"API rate limit exceeded"}`))}, nil
			})}
			_, e := s.Latest(context.Background(), "netbird")
			var se *SourceError
			if row.code == "" {
				if e == nil || errors.As(e, &se) {
					t.Fatal("unnamed failure", e)
				}
				return
			}
			if !errors.As(e, &se) || se.Code != row.code || !se.RetryAt.Equal(row.retryAt) {
				t.Fatal(e, se)
			}
		})
	}
}

type limitedSource struct{ err error }

func (s *limitedSource) Latest(context.Context, string) (Release, error) {
	if s.err != nil {
		return Release{}, s.err
	}
	return Release{Product: "netbird", Version: "0.80.0"}, nil
}
func (*limitedSource) Download(context.Context, Release, string) (string, error) {
	return "", errors.New("unused")
}

func TestCheckReportsTheRateLimitAndClearsItOnSuccess(t *testing.T) {
	source := &limitedSource{err: &SourceError{Code: "github_rate_limited", RetryAt: time.Unix(1792000000, 0)}}
	m := New(t.TempDir(), source)
	if e := m.Check(context.Background(), "netbird"); e == nil {
		t.Fatal("rate limit hidden")
	}
	s, e := m.Status("netbird")
	if e != nil || s.ErrorCode != "github_rate_limited" || s.RetryAt != "2026-10-14T17:46:40Z" || s.Error == "" {
		t.Fatal(s, e)
	}
	source.err = nil
	if e = m.Check(context.Background(), "netbird"); e != nil {
		t.Fatal(e)
	}
	if s, _ = m.Status("netbird"); s.ErrorCode != "" || s.RetryAt != "" || s.Error != "" {
		t.Fatal("stale rate limit", s)
	}
}

// The saved GitHub token goes to GitHub's API and release hosts only, and a
// rejected token is named apart from a spent quota.
func TestOfficialSourceSendsTheGitHubTokenAndNamesARejectedOne(t *testing.T) {
	const token = "not-a-real-token-for-tests-wxyz"
	seen := map[string]string{}
	s := NewOfficialSource().UseToken(func() string { return token })
	s.client = &http.Client{Transport: nbTransport(func(r *http.Request) (*http.Response, error) {
		seen[r.URL.Host] = r.Header.Get("Authorization")
		return &http.Response{StatusCode: 401, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"message":"Bad credentials"}`))}, nil
	})}
	_, e := s.Latest(context.Background(), "netbird")
	var se *SourceError
	if !errors.As(e, &se) || se.Code != "github_token_invalid" {
		t.Fatal(e)
	}
	if seen["api.github.com"] != "Bearer "+token {
		t.Fatal("token not sent to the API", seen)
	}
	_, _ = s.Latest(context.Background(), "tailscale")
	if seen["pkgs.tailscale.com"] != "" {
		t.Fatal("token sent off GitHub")
	}
}

func TestDownloadFailureKeepsTheRateLimitCode(t *testing.T) {
	source := &limitedSource{}
	m := New(t.TempDir(), &downloadLimited{limitedSource: source})
	if e := m.Update(context.Background(), "netbird"); e == nil {
		t.Fatal("rate limit hidden")
	}
	s, e := m.Status("netbird")
	if e != nil || s.Phase != "failed" || s.ErrorCode != "github_rate_limited" || s.RetryAt != "2026-10-14T17:46:40Z" {
		t.Fatal(s, e)
	}
}

type downloadLimited struct{ *limitedSource }

func (*downloadLimited) Download(context.Context, Release, string) (string, error) {
	return "", &SourceError{Code: "github_rate_limited", RetryAt: time.Unix(1792000000, 0)}
}

func TestStatusHintPointsToTheGitHubToken(t *testing.T) {
	m := New(t.TempDir(), &limitedSource{err: &SourceError{Code: "github_rate_limited", RetryAt: time.Unix(1792000000, 0)}})
	_ = m.Check(context.Background(), "netbird")
	s, _ := m.Status("netbird")
	if !strings.Contains(s.Hint, "2026-10-14T17:46:40Z") || !strings.Contains(s.Hint, "Settings > GitHub token") {
		t.Fatal(s.Hint)
	}
	m = New(t.TempDir(), &limitedSource{err: &SourceError{Code: "github_token_invalid"}})
	_ = m.Check(context.Background(), "netbird")
	if s, _ = m.Status("netbird"); !strings.Contains(s.Hint, "invalid or expired") {
		t.Fatal(s.Hint)
	}
}
