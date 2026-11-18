package apps

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIngressLoginNavigationIsNotAnAuthGrant(t *testing.T) {
	e := PackageEntry{Package: "ExampleMusic", Port: 5001, Path: "/music/"}
	r := httptest.NewRequest("GET", "https://music.example.com/?launchApp=SYNO.SDS.ExampleMusic.Instance&launchParam=%7B%22ssoResume%22%3A%22example%3Ap%22%7D", nil)
	w := httptest.NewRecorder()
	if ingressNavigation(w, r, e) {
		t.Fatal("fresh login skipped DSM")
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || !cookies[0].Secure {
		t.Fatal(cookies)
	}
	// A marker can only navigate to the selected package's existing launcher;
	// that launcher still performs DSM authentication and App privilege checks.
	r = httptest.NewRequest("GET", "https://music.example.com/", nil)
	r.AddCookie(cookies[0])
	r.AddCookie(&http.Cookie{Name: "spl_connect_login_ExampleMusic", Value: "1"})
	w = httptest.NewRecorder()
	if !ingressNavigation(w, r, e) || !strings.Contains(w.Body.String(), ingressResumeScript) {
		t.Fatal(w.Header())
	}
	for _, success := range []bool{false, true} {
		body := `{"success":false}`
		if success {
			body = `{"success":true,"data":{"sid":"fixture-session"}}`
		}
		req := httptest.NewRequest("POST", "https://music.example.com/webapi/entry.cgi", strings.NewReader("api=SYNO.API.Auth&method=login"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.ParseForm()
		res := &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: req}
		if err := ingressLoginResponse(res, e); err != nil {
			t.Fatal(err)
		}
		if (res.Header.Get("X-Connect-Login-Complete") == "1") != success {
			t.Fatal("incorrect navigation marker")
		}
		if (len(res.Cookies()) == 1) != success {
			t.Fatal("failed sign-in was treated as completed")
		}
		raw, _ := io.ReadAll(res.Body)
		if string(raw) != body {
			t.Fatal("DSM auth response changed")
		}
	}
}

func TestIngressLoginDocumentAddsOnlySameOriginNavigation(t *testing.T) {
	e := PackageEntry{Package: "ExampleMusic", Path: "/music/", Port: 5001}
	req := httptest.NewRequest("GET", "https://music.example.com/?launchApp=SYNO.SDS.ExampleMusic.Instance&launchParam=%7B%22ssoResume%22%3A%22example%3Ap%22%7D", nil)
	res := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/html"}}, Body: io.NopCloser(strings.NewReader(`<html><head><script src="/scripts/dsm.js"></script></head><body>DSM login</body></html>`)), Request: req}
	if err := ingressLoginDocument(res, e); err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(res.Body)
	if strings.Index(string(raw), ingressResumeScript) > strings.Index(string(raw), "/scripts/dsm.js") {
		t.Fatal("navigation hook installed too late")
	}
	if !strings.Contains(string(raw), ingressResumeScript) || strings.Contains(string(raw), "http://") || strings.Contains(string(raw), "document.cookie") {
		t.Fatal("unexpected login document modification")
	}
	r := httptest.NewRequest("GET", "https://music.example.com"+ingressResumeScript+"?launchParam=%7B%22ssoResume%22%3A%22example%3Ap%22%7D", nil)
	w := httptest.NewRecorder()
	serveIngressResumeScript(w, r, e)
	if !strings.Contains(w.Body.String(), "/webman/3rdparty/ExampleMusic/sso/open.cgi") || strings.Contains(w.Body.String(), "sid") || strings.Contains(w.Body.String(), "SynoToken") {
		t.Fatal("navigation script contains auth logic")
	}
}
