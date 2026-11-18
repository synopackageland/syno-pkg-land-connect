package apps

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// These short-lived navigation markers carry no session, credential or grant.
// Only the installed App's authorize.cgi decides whether the DSM sign-in and
// App privilege checks passed. Returning directly to its launcher avoids
// exposing DSM's desktop after the standard DSM login document completes.
func ingressNavigation(w http.ResponseWriter, r *http.Request, e PackageEntry) bool {
	if r.URL.Path != "/" {
		return false
	}
	resumeName, doneName := "spl_connect_resume_"+e.Package, "spl_connect_login_"+e.Package
	if done, err := r.Cookie(doneName); err == nil && done.Value == "1" {
		if resume, err := r.Cookie(resumeName); err == nil {
			param, err := url.QueryUnescape(resume.Value)
			if err == nil && len(param) <= 2048 {
				if r.Method != http.MethodGet && r.Method != http.MethodHead {
					return false
				}
				http.SetCookie(w, &http.Cookie{Name: resumeName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
				script := ingressResumeScript
				if param != "" {
					script += "?" + url.Values{"launchParam": {param}}.Encode()
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Header().Set("Cache-Control", "no-store")
				w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'self'; frame-ancestors 'none'")
				fmt.Fprintf(w, `<!doctype html><meta charset="utf-8"><script src="%s"></script>`, html.EscapeString(script))
				return true
			}
		}
	}
	if r.URL.Query().Get("launchApp") == "SYNO.SDS."+e.Package+".Instance" {
		param := r.URL.Query().Get("launchParam")
		if len(param) <= 2048 {
			http.SetCookie(w, &http.Cookie{Name: resumeName, Value: url.QueryEscape(param), Path: "/", MaxAge: 300, HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode})
		}
	}
	return false
}

func ingressLoginResponse(res *http.Response, e PackageEntry) error {
	r := res.Request
	if r == nil || r.URL.Path != "/webapi/auth.cgi" && r.URL.Path != "/webapi/entry.cgi" && r.URL.Path != "/webapi/entry.cgi/SYNO.API.Auth" {
		return nil
	}
	// Never inspect arbitrary App responses or retain a DSM session identifier.
	if r.Form.Get("api") != "SYNO.API.Auth" && r.URL.Query().Get("api") != "SYNO.API.Auth" {
		return nil
	}
	if res.StatusCode != http.StatusOK || res.Header.Get("Content-Encoding") != "" {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 64<<10+1))
	if err != nil {
		return err
	}
	_ = res.Body.Close()
	if len(body) > 64<<10 {
		return ErrInvalid
	}
	res.Body = io.NopCloser(bytes.NewReader(body))
	res.ContentLength = int64(len(body))
	var payload struct {
		Success bool `json:"success"`
		Data    struct {
			SID string `json:"sid"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &payload) == nil && payload.Success && payload.Data.SID != "" {
		res.Header.Set("X-Connect-Login-Complete", "1")
		res.Header.Set("Cache-Control", "no-store")
		res.Header.Add("Set-Cookie", (&http.Cookie{Name: "spl_connect_login_" + e.Package, Value: "1", Path: "/", MaxAge: 60, HttpOnly: false, Secure: true, SameSite: http.SameSiteLaxMode}).String())
	}
	return nil
}

const ingressResumeScript = "/_connect/login-resume.js"

func serveIngressResumeScript(w http.ResponseWriter, r *http.Request, e PackageEntry) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	param := r.URL.Query().Get("launchParam")
	if len(param) > 2048 {
		http.Error(w, "invalid navigation", 400)
		return
	}
	target := "/webman/3rdparty/" + e.Package + "/sso/open.cgi"
	if param != "" {
		target += "?" + url.Values{"launchParam": {param}}.Encode()
	}
	quoted, _ := json.Marshal(target)
	marker, _ := json.Marshal("spl_connect_login_" + e.Package + "=1")
	w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// The marker only asks for navigation; the App still authenticates the user.
	// No session or credential is read, written, copied or passed to the App.
	storageKey, _ := json.Marshal("spl_connect_fragment_" + e.Package)
	fmt.Fprintf(w, ingressLoginScript, marker, quoted, storageKey)

}

func ingressLoginDocument(res *http.Response, e PackageEntry) error {
	if res.Request == nil || res.Request.URL.Path != "/" || res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/html") {
		return nil
	}
	if res.Header.Get("Content-Encoding") != "" {
		return ErrInvalid
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 512<<10+1))
	if err != nil {
		return err
	}
	_ = res.Body.Close()
	if len(body) > 512<<10 {
		return ErrInvalid
	}
	script := ingressResumeScript
	if param := res.Request.URL.Query().Get("launchParam"); param != "" && len(param) <= 2048 {
		script += "?" + url.Values{"launchParam": {param}}.Encode()
	}
	tag := []byte(`<script src="` + html.EscapeString(script) + `"></script>`)
	// Install the navigation hook before DSM's bundles capture fetch/XHR.
	at := bytes.Index(bytes.ToLower(body), []byte("<script"))
	if at < 0 {
		at = len(body)
	}
	updated := make([]byte, 0, len(body)+len(tag))
	updated = append(updated, body[:at]...)
	updated = append(updated, tag...)
	updated = append(updated, body[at:]...)
	body = updated
	res.Body = io.NopCloser(bytes.NewReader(body))
	res.ContentLength = int64(len(body))
	res.Header.Set("Content-Length", strconv.Itoa(len(body)))
	res.Header.Set("Cache-Control", "no-store")
	res.Header.Del("ETag")
	return nil
}

//go:embed ingress_login.js
var ingressLoginScript string
