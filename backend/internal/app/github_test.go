package app

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type githubStub func(*http.Request) (*http.Response, error)

func (f githubStub) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const testGitHubToken = "not-a-real-token-for-tests-wxyz"

func TestGitHubTokenRoutesAreForAdministrators(t *testing.T) {
	for _, method := range []string{"github.token.status", "github.token.set", "github.token.remove"} {
		if code, _ := call(t, testConfig(t), bob, method, `{}`); code != "admin_required" {
			t.Fatal(method, code)
		}
	}
}

func TestGitHubTokenIsCheckedSavedPrivatelyAndNeverReturned(t *testing.T) {
	cfg := testConfig(t)
	status := 200
	cfg.GitHubHTTP = &http.Client{Transport: githubStub(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != "https://api.github.com/rate_limit" || r.Header.Get("Authorization") != "Bearer "+testGitHubToken {
			t.Fatal(r.URL, r.Header)
		}
		return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`{"resources":{"core":{"limit":5000,"remaining":5000,"reset":1792000000}}}`))}, nil
	})}
	status = 401
	if code, _ := call(t, cfg, alice, "github.token.set", `{"token":"`+testGitHubToken+`"}`); code != "github_token_invalid" {
		t.Fatal(code)
	}
	if _, e := os.Stat(filepath.Join(cfg.DataDir, "github-token")); e == nil {
		t.Fatal("a rejected token was saved")
	}
	status = 200
	code, out := call(t, cfg, alice, "github.token.set", `{"token":"`+testGitHubToken+`"}`)
	data, _ := out["data"].(map[string]any)
	if code != "" || data["saved"] != true || data["last4"] != "wxyz" || data["limit"] != float64(5000) || data["reset"] != "2026-10-14T17:46:40Z" {
		t.Fatal(code, out)
	}
	st, e := os.Stat(filepath.Join(cfg.DataDir, "github-token"))
	if e != nil || st.Mode().Perm() != 0600 {
		t.Fatal(st, e)
	}
	for _, method := range []string{"github.token.status", "github.token.set"} {
		_, out = call(t, cfg, alice, method, `{}`)
		if raw := toJSON(t, out); strings.Contains(raw, testGitHubToken) {
			t.Fatal("token returned by", method)
		}
	}
	code, out = call(t, cfg, alice, "github.token.remove", `{}`)
	if data, _ = out["data"].(map[string]any); code != "" || data["saved"] != false {
		t.Fatal(code, out)
	}
}

func toJSON(t *testing.T, v any) string {
	b, e := json.Marshal(v)
	if e != nil {
		t.Fatal(e)
	}
	return string(b)
}
