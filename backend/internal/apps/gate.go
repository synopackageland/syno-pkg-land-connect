package apps

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"
)

// The DSM sign-in gate in front of a custom App. A visitor signs in with a
// DSM account on the App's own address; Connect asks DSM, discards DSM's
// temporary session at once and keeps only its own host-only cookie. The App
// never sees the password, the DSM session or Connect's cookie.
const (
	GatePrefix     = "/_spl/connect/"
	gateSignIn     = GatePrefix + "signin"
	gateSignOut    = GatePrefix + "signout"
	gateCookie     = "__Host-spl-connect"
	gateLifetime   = 8 * time.Hour
	gateIdle       = time.Hour
	gateMaxSession = 1024
)

// Login checks DSM credentials. It returns the DSM user name, or "" and a
// code: invalid_credentials, otp_required, otp_invalid, account_disabled,
// blocked, not_permitted or unavailable. It never keeps a DSM session.
type Login func(ctx context.Context, account, password, otp string) (string, string)

type gateSession struct {
	user          string
	created, seen time.Time
}

// Gate is one App's sessions. They live in memory only: a restart, an edit or
// a pause of the App signs everyone out.
type Gate struct {
	Login Login
	Now   func() time.Time
	mu    sync.Mutex
	sess  map[string]gateSession
	tries map[string][]time.Time
}

func (g *Gate) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

func tokenKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// session reports the signed-in user for the request's Connect cookie.
func (g *Gate) session(r *http.Request) (string, bool) {
	c, err := r.Cookie(gateCookie)
	if err != nil || len(c.Value) != 64 {
		return "", false
	}
	key := tokenKey(c.Value)
	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	s, ok := g.sess[key]
	if !ok || !now.Before(s.created.Add(gateLifetime)) || !now.Before(s.seen.Add(gateIdle)) {
		delete(g.sess, key)
		return "", false
	}
	s.seen = now
	g.sess[key] = s
	return s.user, true
}

func (g *Gate) start(user string) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.sess == nil {
		g.sess = map[string]gateSession{}
	}
	for key, s := range g.sess {
		if !now.Before(s.created.Add(gateLifetime)) || !now.Before(s.seen.Add(gateIdle)) {
			delete(g.sess, key)
		}
	}
	if len(g.sess) >= gateMaxSession {
		var oldest string
		for key, s := range g.sess {
			if oldest == "" || s.seen.Before(g.sess[oldest].seen) {
				oldest = key
			}
		}
		delete(g.sess, oldest)
	}
	g.sess[tokenKey(token)] = gateSession{user: user, created: now, seen: now}
	return token, nil
}

func (g *Gate) end(r *http.Request) {
	if c, err := r.Cookie(gateCookie); err == nil {
		g.mu.Lock()
		delete(g.sess, tokenKey(c.Value))
		g.mu.Unlock()
	}
}

// limited allows 20 sign-in attempts a minute for the App and 10 per account
// in 15 minutes. DSM sees every attempt from the NAS itself, so Connect must
// stop guessing before DSM's own auto block would lock out the loopback.
func (g *Gate) limited(account string) bool {
	now := g.now()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.tries == nil {
		g.tries = map[string][]time.Time{}
	}
	keep := func(key string, window time.Duration) []time.Time {
		out := g.tries[key][:0]
		for _, t := range g.tries[key] {
			if now.Sub(t) < window {
				out = append(out, t)
			}
		}
		return out
	}
	all, user := keep("", time.Minute), keep("a:"+account, 15*time.Minute)
	if len(all) >= 20 || len(user) >= 10 {
		g.tries[""], g.tries["a:"+account] = all, user
		return true
	}
	g.tries[""], g.tries["a:"+account] = append(all, now), append(user, now)
	if len(g.tries) > 4096 {
		for key := range g.tries {
			if key != "" {
				delete(g.tries, key)
			}
		}
	}
	return false
}

func (g *Gate) setCookie(w http.ResponseWriter, token string) {
	c := &http.Cookie{Name: gateCookie, Value: token, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: int(gateLifetime / time.Second)}
	if token == "" {
		c.MaxAge = -1
	}
	http.SetCookie(w, c)
}

// safeNext keeps the return path on this origin.
func safeNext(next string) string {
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") || strings.HasPrefix(next, "/\\") || strings.HasPrefix(next, GatePrefix) || strings.ContainsFunc(next, unicode.IsControl) || len(next) > 2048 {
		return "/"
	}
	return next
}

// sameOrigin refuses a sign-in form posted from another site.
func sameOrigin(r *http.Request, authority string) bool {
	if origin := r.Header.Get("Origin"); origin != "" {
		return origin == "https://"+authority
	}
	return r.Header.Get("Sec-Fetch-Site") == "same-origin"
}

// stripCookie removes Connect's cookie before a request reaches the App.
func stripCookie(r *http.Request) {
	cookies := r.Cookies()
	r.Header.Del("Cookie")
	kept := []string{}
	for _, c := range cookies {
		if c.Name != gateCookie {
			kept = append(kept, c.Name+"="+c.Value)
		}
	}
	if len(kept) > 0 {
		r.Header.Set("Cookie", strings.Join(kept, "; "))
	}
}

// wantsPage: a top-level navigation gets the sign-in page; an API call,
// fetch or WebSocket gets 401.
func wantsPage(r *http.Request) bool {
	return (r.Method == http.MethodGet || r.Method == http.MethodHead) && r.Header.Get("Upgrade") == "" && strings.Contains(r.Header.Get("Accept"), "text/html")
}

// handle serves the gate's own paths and refuses requests without a session.
// It returns true when the request may go on to the App.
func (g *Gate) handle(w http.ResponseWriter, r *http.Request, authority, name string) bool {
	switch r.URL.Path {
	case gateSignIn:
		g.signIn(w, r, authority, name)
		return false
	case gateSignOut:
		g.end(r)
		g.setCookie(w, "")
		http.Redirect(w, r, gateSignIn, http.StatusSeeOther)
		return false
	}
	if _, ok := g.session(r); ok {
		stripCookie(r)
		return true
	}
	if wantsPage(r) {
		http.Redirect(w, r, gateSignIn+"?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
		return false
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, "DSM sign-in required", http.StatusUnauthorized)
	return false
}

func (g *Gate) signIn(w http.ResponseWriter, r *http.Request, authority, name string) {
	locale := pageLocale(r.Header.Get("Accept-Language"))
	view := gateView{Locale: locale, App: name, Next: safeNext(r.URL.Query().Get("next"))}
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		if _, ok := g.session(r); ok {
			http.Redirect(w, r, view.Next, http.StatusSeeOther)
			return
		}
		renderGate(w, http.StatusOK, view)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "GET, POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !sameOrigin(r, authority) {
		http.Error(w, "cross-origin sign-in refused", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
	if err := r.ParseForm(); err != nil {
		renderGate(w, http.StatusBadRequest, view.with("unavailable"))
		return
	}
	account, password, otp := strings.TrimSpace(r.PostForm.Get("account")), r.PostForm.Get("password"), strings.TrimSpace(r.PostForm.Get("otp"))
	view.Next = safeNext(r.PostForm.Get("next"))
	view.Account = account
	if account == "" || password == "" || len(account) > 256 || len(password) > 1024 || len(otp) > 16 || strings.ContainsFunc(account, unicode.IsControl) {
		renderGate(w, http.StatusBadRequest, view.with("invalid_credentials"))
		return
	}
	if g.limited(strings.ToLower(account)) {
		renderGate(w, http.StatusTooManyRequests, view.with("too_many"))
		return
	}
	login := g.Login
	if login == nil {
		renderGate(w, http.StatusServiceUnavailable, view.with("unavailable"))
		return
	}
	user, code := login(r.Context(), account, password, otp)
	if code != "" || user == "" {
		if code == "" {
			code = "unavailable"
		}
		view.OTP = code == "otp_required" || code == "otp_invalid" || otp != ""
		status := http.StatusUnauthorized
		if code == "unavailable" {
			status = http.StatusServiceUnavailable
		}
		renderGate(w, status, view.with(code))
		return
	}
	token, err := g.start(user)
	if err != nil {
		renderGate(w, http.StatusServiceUnavailable, view.with("unavailable"))
		return
	}
	g.setCookie(w, token)
	http.Redirect(w, r, view.Next, http.StatusSeeOther)
}

type gateView struct {
	Locale, App, Next, Account, Error string
	OTP                               bool
}

func (v gateView) with(code string) gateView { v.Error = code; return v }

func renderGate(w http.ResponseWriter, status int, v gateView) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; frame-ancestors 'none'; base-uri 'none'")
	h.Set("X-Frame-Options", "DENY")
	// A navigation POST under no-referrer sends Origin: null, even to this
	// same host. Keep the browser's origin for the approved-authority CSRF
	// check, while still withholding referrers from other origins.
	h.Set("Referrer-Policy", "same-origin")
	h.Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	text := gateText(v.Locale)
	errText := ""
	if v.Error != "" {
		errText = text["error."+v.Error]
		if errText == "" {
			errText = text["error.not_permitted"]
		}
	}
	_ = gatePage.Execute(w, map[string]any{"V": v, "T": text, "Title": strings.ReplaceAll(text["title"], "{app}", v.App), "Err": errText})
}

var gatePage = template.Must(template.New("gate").Parse(`<!doctype html>
<html lang="{{.V.Locale}}"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta name="color-scheme" content="light dark"><meta name="robots" content="noindex">
<title>{{.Title}}</title>
<style>
:root{--bg:#f3f5f8;--card:#fff;--fg:#1f2329;--muted:#5d6672;--line:#d5dbe3;--accent:#0b6bcb;--danger:#b42318;--field:#fff}
@media (prefers-color-scheme:dark){:root{--bg:#16191d;--card:#20242a;--fg:#e8ebef;--muted:#a3acb8;--line:#3a414b;--accent:#5aa2ef;--danger:#f97066;--field:#16191d}}
*{box-sizing:border-box}body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;background:var(--bg);color:var(--fg);font:15px/1.45 system-ui,-apple-system,"Segoe UI",Roboto,"Noto Sans",sans-serif;padding:16px}
main{width:100%;max-width:360px;background:var(--card);border:1px solid var(--line);border-radius:10px;padding:24px}
h1{font-size:19px;margin:0 0 6px}p{margin:0 0 16px;color:var(--muted)}
label{display:block;font-weight:600;margin:12px 0 4px}input{width:100%;padding:9px 10px;border:1px solid var(--line);border-radius:6px;background:var(--field);color:var(--fg);font:inherit}
input:focus{outline:2px solid var(--accent);outline-offset:1px}
button{margin-top:18px;width:100%;padding:10px;border:0;border-radius:6px;background:var(--accent);color:#fff;font:inherit;font-weight:600;cursor:pointer}
.err{color:var(--danger);margin:12px 0 0}.foot{margin:16px 0 0;font-size:13px}
</style></head><body><main>
<h1>{{.Title}}</h1><p>{{index .T "lead"}}</p>
<form method="post" action="/_spl/connect/signin">
<input type="hidden" name="next" value="{{.V.Next}}">
<label for="account">{{index .T "account"}}</label><input id="account" name="account" autocomplete="username" required value="{{.V.Account}}"{{if not .V.Account}} autofocus{{end}}>
<label for="password">{{index .T "password"}}</label><input id="password" name="password" type="password" autocomplete="current-password" required{{if .V.Account}} autofocus{{end}}>
{{if .V.OTP}}<label for="otp">{{index .T "otp"}}</label><input id="otp" name="otp" inputmode="numeric" autocomplete="one-time-code">{{end}}
{{if .Err}}<p class="err" role="alert">{{.Err}}</p>{{end}}
<button type="submit">{{index .T "submit"}}</button>
</form>
<p class="foot">{{index .T "footer"}}</p>
</main></body></html>
`))

// DSMLogin checks credentials with DSM's own Auth API on the NAS, as the
// SDK's standalone identity does, and logs the temporary session out at once.
type DSMLogin struct {
	Endpoint func(context.Context) (string, error)
	Client   *http.Client
}

type dsmAuthReply struct {
	Success bool `json:"success"`
	Data    struct {
		SID string `json:"sid"`
	} `json:"data"`
	Error struct {
		Code int `json:"code"`
	} `json:"error"`
}

func (d DSMLogin) call(ctx context.Context, endpoint string, form url.Values) (dsmAuthReply, error) {
	var out dsmAuthReply
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := d.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	res, err := client.Do(req)
	if err != nil {
		return out, err
	}
	defer res.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 64<<10+1))
	if err != nil || len(raw) > 64<<10 || res.StatusCode != http.StatusOK || json.Unmarshal(raw, &out) != nil {
		return out, errors.New("DSM unavailable")
	}
	return out, nil
}

func (d DSMLogin) Login(ctx context.Context, account, password, otp string) (string, string) {
	if d.Endpoint == nil {
		return "", "unavailable"
	}
	endpoint, err := d.Endpoint(ctx)
	if err != nil {
		return "", "unavailable"
	}
	form := url.Values{"api": {"SYNO.API.Auth"}, "method": {"login"}, "version": {"7"}, "account": {account}, "passwd": {password}, "format": {"sid"}}
	if otp != "" {
		form.Set("otp_code", otp)
	}
	out, err := d.call(ctx, endpoint, form)
	if err != nil {
		return "", "unavailable"
	}
	if !out.Success {
		code, ok := map[int]string{400: "invalid_credentials", 401: "account_disabled", 403: "otp_required", 404: "otp_invalid", 407: "blocked"}[out.Error.Code]
		if !ok {
			code = "not_permitted"
		}
		return "", code
	}
	if out.Data.SID == "" {
		return "", "unavailable"
	}
	// The temporary DSM session only proves the account; a failed logout
	// refuses the sign-in rather than leave a session behind.
	done, err := d.call(context.WithoutCancel(ctx), endpoint, url.Values{"api": {"SYNO.API.Auth"}, "method": {"logout"}, "version": {"7"}, "_sid": {out.Data.SID}})
	if err != nil || !done.Success {
		return "", "unavailable"
	}
	return account, ""
}
