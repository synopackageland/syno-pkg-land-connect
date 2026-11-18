package apps

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNativeIngressSignInSurface(t *testing.T) {
	e := PackageEntry{Package: "ExampleMusic", Path: "/music/", Port: 5001}
	for _, c := range []struct {
		method, url, body string
		allowed           bool
	}{
		{"GET", "https://music.example.com/?launchApp=SYNO.SDS.ExampleMusic.Instance&launchParam=%7B%22ssoResume%22%3A%22example%3Ap%22%7D", "", true},
		{"GET", "https://music.example.com/?launchApp=SYNO.SDS.Other.Instance", "", false},
		{"GET", "https://music.example.com/webman/login/dist/dsm.login.bundle.js", "", true},
		{"GET", "https://music.example.com/webapi/entry.cgi?api=SYNO.Core.Desktop.SessionData&version=1&method=getjs", "", true},
		{"POST", "https://music.example.com/webapi/entry.cgi/SYNO.API.Auth.UIConfig", "api=SYNO.API.Auth.UIConfig&version=1&method=get", true},
		{"POST", "https://music.example.com/webapi/entry.cgi/SYNO.API.Auth", "api=SYNO.API.Auth&version=7&method=login&account=example&passwd=example", true},
		{"POST", "https://music.example.com/webapi/entry.cgi", "api=SYNO.Core.Package&method=uninstall", false},
		{"POST", "https://music.example.com/webapi/entry.cgi", "api=SYNO.API.Auth&method=set", false},
		{"GET", "https://music.example.com/webapi/entry.cgi?api=SYNO.Core.Desktop.Initdata&method=get", "", false},
		{"POST", "https://music.example.com/webapi/entry.cgi/SYNO.Core.User", "api=SYNO.API.Auth&method=login", false},
		{"POST", "https://music.example.com/webapi/entry.cgi", "api=SYNO.API.Auth&method=login&api=SYNO.Core.Package", false},
		{"GET", "https://music.example.com/webman/3rdparty/Other/cgi/api.cgi", "", false},
	} {
		r := httptest.NewRequest(c.method, c.url, strings.NewReader(c.body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if got := allowIngressRequest(r, e); got != c.allowed {
			t.Errorf("%s %s got %v", c.method, c.url, got)
		}
	}
}

func TestNativeIngressNormalizesOnlyIdenticalDSMDispatch(t *testing.T) {
	for _, values := range [][]string{{"SYNO.API.Auth", "SYNO.Core.Package"}, {"\"SYNO.API.Auth\"", "SYNO.Core.Package"}, {"a", "a", "a"}} {
		if _, ok := sameAPIValue(values); ok {
			t.Fatal("ambiguous API allowed")
		}
	}
	if got, ok := sameAPIValue([]string{"\"SYNO.API.Auth.Type\""}); !ok || got != "SYNO.API.Auth.Type" {
		t.Fatal(got, ok)
	}
	if _, ok := sameAPIValue([]string{"SYNO.API.Auth", "SYNO.API.Auth"}); !ok {
		t.Fatal("identical query/body dispatch refused")
	}
}
