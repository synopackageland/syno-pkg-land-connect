package apps

import (
	"encoding/json"
	"io"
	"net/http"
	"path"
	"strings"
)

// The package's gateway/authorize.cgi remains the authority for DSM sessions
// and App privileges. This is only a network surface restriction. It exposes
// login resources, never DSM's desktop APIs, other Apps or general entry.cgi.
func allowIngressRequest(r *http.Request, e PackageEntry) bool {
	p := r.URL.Path
	if r.URL.RawPath != "" && r.URL.EscapedPath() != p {
		return false
	}
	if e.AllowsPath(p) {
		return true
	}
	if strings.ContainsAny(p, "%\\\x00") || strings.Contains(p, "..") || strings.Contains(p, "//") {
		return false
	}
	read := r.Method == http.MethodGet || r.Method == http.MethodHead
	if p == "/" {
		q := r.URL.Query()
		for k := range q {
			if k != "launchApp" && k != "launchParam" {
				return false
			}
		}
		return read && len(q) <= 2 && q.Get("launchApp") == "SYNO.SDS."+e.Package+".Instance" && len(q["launchApp"]) == 1 && len(q["launchParam"]) <= 1 && len(q.Get("launchParam")) <= 4096
	}
	if p == "/webman/login.cgi" {
		q := r.URL.Query()
		return read && len(q) == 1 && len(q["enable_syno_token"]) == 1 && q.Get("enable_syno_token") == "yes"
	}
	// DSM's sign-in document loads static resources from these namespaces.
	if read {
		for _, prefix := range []string{"/webman/login/dist/", "/webman/entry/dist/", "/webman/sds/dist/", "/webman/desktop/dist/", "/webman/taskbar/dist/", "/webman/unsupported-browsers/dist/", "/webman/3rdparty/SecureSignIn/login-dist/", "/webman/3rdparty/OAuthService/login-dist/", "/webman/resources/", "/webman/modules/", "/synoSDSjslib/", "/scripts/", "/synohdpack/"} {
			if strings.HasPrefix(p, prefix) && !strings.Contains(strings.ToLower(p), ".cgi") && staticLoginExtension(path.Ext(p)) {
				return true
			}
		}
	}
	if p != "/webapi/auth.cgi" && p != "/webapi/entry.cgi" && p != "/webapi/query.cgi" && p != "/webapi/encryption.cgi/SYNO.API.Encryption" && !strings.HasPrefix(p, "/webapi/entry.cgi/SYNO.API.Auth") {
		return false
	}
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		return false
	}
	// Parse a bounded form then restore it for the reverse proxy. No arbitrary
	// multi-request batching: each API must be an explicit authentication API.
	q := r.URL.Query()
	if r.Method == http.MethodPost {
		if r.ContentLength > 16<<10 {
			return false
		}
		r.Body = http.MaxBytesReader(nil, r.Body, 16<<10)
		if err := r.ParseForm(); err != nil {
			return false
		}
		q = r.Form
		body := r.PostForm.Encode()
		r.Body = http.NoBody
		if body != "" {
			r.Body = io.NopCloser(strings.NewReader(body))
		}
		r.ContentLength = int64(len(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	api, apiOK := sameAPIValue(q["api"])
	method, methodOK := sameAPIValue(q["method"])
	if !apiOK || !methodOK {
		return false
	}
	if strings.HasPrefix(p, "/webapi/encryption.cgi/") && api != "SYNO.API.Encryption" {
		return false
	}
	if strings.HasPrefix(p, "/webapi/entry.cgi/") && strings.TrimPrefix(p, "/webapi/entry.cgi/") != api {
		return false
	}
	if api == "SYNO.API.Info" {
		return method == "query"
	}
	if read && method == "getjs" {
		switch api {
		case "SYNO.Core.Desktop.SessionData", "SYNO.Core.Desktop.Defs", "SYNO.Core.Desktop.JSUIString", "SYNO.Core.Desktop.UIString":
			return true
		}
	}
	methods := map[string][]string{
		"SYNO.API.Auth":          {"login", "logout", "checkauth", "query", "getinfo"},
		"SYNO.API.Auth.UIConfig": {"get"}, "SYNO.API.Auth.Type": {"get"},
		"SYNO.API.Encryption": {"getinfo"}, "SYNO.API.Auth.Key": {"get"},
		"SYNO.API.Auth.FIDO": {"auth", "verify", "get"}, "SYNO.API.Auth.OTP": {"verify"},
		"SYNO.API.Auth.SecureSignIn": {"auth", "verify", "get"},
	}
	for _, m := range methods[api] {
		if method == m {
			return true
		}
	}
	return false
}

// DSM clients sometimes repeat a routing field in query and body or encode
// it as a JSON string. Accept identical values only, never ambiguous dispatch.
func sameAPIValue(values []string) (string, bool) {
	if len(values) == 0 || len(values) > 2 {
		return "", false
	}
	expected := ""
	for i, raw := range values {
		v := raw
		if strings.HasPrefix(raw, "\"") {
			if json.Unmarshal([]byte(raw), &v) != nil {
				return "", false
			}
		}
		if i > 0 && v != expected {
			return "", false
		}
		expected = v
	}
	return expected, expected != ""
}

func staticLoginExtension(ext string) bool {
	switch ext {
	case ".wasm", ".js", ".css", ".png", ".jpg", ".jpeg", ".gif", ".svg", ".ico", ".woff", ".woff2", ".ttf", ".otf", ".map":
		return true
	}
	return false
}
