package bigupload

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func page(body, ctype string, status int, method string) *http.Response {
	return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {ctype}, "Etag": {`"x"`}},
		Body: io.NopCloser(strings.NewReader(body)), ContentLength: -1, Request: httptest.NewRequest(method, "/", nil)}
}

func read(t *testing.T, res *http.Response) string {
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestInjectAfterHead(t *testing.T) {
	res := page(`<!DOCTYPE html><html><HEAD lang="en"><title>DSM</title><script src="a.js"></script></head></html>`, "text/html; charset=utf-8", 200, "GET")
	if err := Inject(res); err != nil {
		t.Fatal(err)
	}
	got := read(t, res)
	want := `<HEAD lang="en">` + string(scriptTag) + `<title>`
	if !strings.Contains(got, want) || strings.Count(got, ScriptPath) != 1 {
		t.Fatalf("%s", got)
	}
	if res.ContentLength != int64(len(got)) || res.Header.Get("Content-Length") == "" || res.Header.Get("ETag") != "" {
		t.Fatalf("headers %v %d", res.Header, res.ContentLength)
	}
}

func TestInjectSkips(t *testing.T) {
	cases := map[string]*http.Response{
		"json":       page(`<head>`, "application/json", 200, "GET"),
		"not found":  page(`<head>`, "text/html", 404, "GET"),
		"post":       page(`<head>`, "text/html", 200, "POST"),
		"no head":    page(`<html><header>x</header></html>`, "text/html", 200, "GET"),
		"compressed": page(`<head>`, "text/html", 200, "GET"),
	}
	cases["compressed"].Header.Set("Content-Encoding", "gzip")
	for name, res := range cases {
		before := res.Header.Get("Content-Encoding")
		if err := Inject(res); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(read(t, res), ScriptPath) || res.Header.Get("Content-Encoding") != before {
			t.Errorf("%s injected", name)
		}
	}
}

func TestInjectPassesLargePagesUnchanged(t *testing.T) {
	big := "<head>" + strings.Repeat("x", maxPage+10)
	res := page(big, "text/html", 200, "GET")
	if err := Inject(res); err != nil {
		t.Fatal(err)
	}
	if got := read(t, res); got != big {
		t.Fatalf("large page changed (%d bytes)", len(got))
	}
}

func TestScriptServed(t *testing.T) {
	m, _ := New(Config{Dir: t.TempDir() + "/u"})
	defer m.Close()
	h := m.Handler(nil)
	for v, cache := range map[string]string{scriptVersion: "immutable", "old": "no-cache"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", ScriptPath+"?v="+v, nil))
		if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), script) || !strings.Contains(w.Header().Get("Cache-Control"), cache) ||
			!strings.HasPrefix(w.Header().Get("Content-Type"), "text/javascript") {
			t.Fatalf("%s: %d %v", v, w.Code, w.Header())
		}
	}
}

func TestNavigation(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Sec-Fetch-Dest", "document")
	if !Navigation(r) {
		t.Fatal("document")
	}
	r.Header.Set("Sec-Fetch-Dest", "iframe")
	if !Navigation(r) {
		t.Fatal("iframe")
	}
	r.Header.Set("Sec-Fetch-Dest", "script")
	if Navigation(r) {
		t.Fatal("script")
	}
	r = httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept", "text/html,*/*")
	if !Navigation(r) {
		t.Fatal("old browser")
	}
	if Navigation(httptest.NewRequest("POST", "/", nil)) {
		t.Fatal("post")
	}
}
