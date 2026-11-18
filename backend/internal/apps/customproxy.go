package apps

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dsmapp/internal/localipc"
	"github.com/synopackageland/syno-pkg-land-sdk/ipc"
)

// CustomConfig is one approved public scope of a custom App. Like a pin
// proxy it is immutable: an edit stops it and starts a replacement.
type CustomConfig struct {
	Authorize func(*http.Request) error
	Binding   PinBinding
	App       CustomApp
	// State re-reads the durable approval on every request.
	State interface{ Active(PinBinding) bool }
	Guard TargetGuard
	// Gate is DSM sign-in; nil only when the App handles sign-in itself.
	Gate *Gate
	// Roots verify https targets; nil uses Connect's bundled roots.
	Roots *x509.CertPool
	// IngressDial verifies the established server socket owner before HTTP bytes.
	IngressDial func(context.Context, string, string) (net.Conn, error)
}

// CustomProxy forwards one provider scope to exactly one configured origin.
// The target never comes from the request: not from Host, the path or a
// redirect. Every dial goes through the TargetGuard.
type CustomProxy struct {
	cfg     CustomConfig
	target  *url.URL
	rp      *httputil.ReverseProxy
	tr      *http.Transport
	blocked atomic.Bool
	mu      sync.Mutex
	server  *ipc.Server
}

var errCustomConfig = errors.New("invalid custom App proxy configuration")

func NewCustomProxy(cfg CustomConfig) (*CustomProxy, error) {
	b := cfg.Binding
	target, err := cfg.App.target()
	if err != nil || !cfg.App.valid() || cfg.State == nil || !IsCustom(b.Package) || b.Scope != Public || b.Mode != ModeCustom ||
		b.Summary != cfg.App.Digest() || b.Port != cfg.App.Port() || !validAuthority(b.Authority, false) || cfg.Gate == nil && !cfg.App.AppSignIn {
		return nil, errCustomConfig
	}
	if cfg.App.Ingress != nil && (cfg.IngressDial == nil || b.Provider == Workers || b.Authority != cfg.App.Ingress.Hostname) {
		return nil, errCustomConfig
	}
	dial := cfg.Guard.DialContext
	if cfg.App.Ingress != nil {
		dial = cfg.IngressDial
	}
	roots := cfg.Roots
	if roots == nil && target.Scheme == "https" && !cfg.App.InsecureTLS {
		roots = ProductRoots()
	}
	p := &CustomProxy{cfg: cfg, target: target}
	p.tr = &http.Transport{
		Proxy: nil, DialContext: dial, ForceAttemptHTTP2: false,
		TLSClientConfig: &tls.Config{ServerName: target.Hostname(), RootCAs: roots, InsecureSkipVerify: cfg.App.InsecureTLS, MinVersion: tls.VersionTLS12},
		MaxIdleConns:    64, MaxIdleConnsPerHost: 32, IdleConnTimeout: 30 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 0,
	}
	public := "https://" + b.Authority
	p.rp = &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			origin := *target
			origin.Path = ""
			r.SetURL(&origin)
			r.Out.Host = target.Host
			for _, h := range []string{"Forwarded", "X-Forwarded-For", "X-Real-Ip", "Cf-Connecting-Ip", "True-Client-Ip"} {
				r.Out.Header.Del(h)
			}
			// The App builds its own links from these; it sees no client address.
			r.Out.Header.Set("X-Forwarded-Host", b.Authority)
			r.Out.Header.Set("X-Forwarded-Proto", "https")
		},
		Transport: p.tr, FlushInterval: 0, BufferPool: pinBuffers{},
		ModifyResponse: func(res *http.Response) error {
			rewriteLocation(res, target, public)
			dropGateCookie(res)
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			if code := ErrorCode(err); code == "custom_target_refused" {
				http.Error(w, "refused target", http.StatusForbidden)
				return
			}
			http.Error(w, "App unreachable", http.StatusBadGateway)
		},
	}
	return p, nil
}

func StartCustomProxy(ctx context.Context, cfg CustomConfig) (*CustomProxy, error) {
	p, err := NewCustomProxy(cfg)
	if err != nil {
		return nil, err
	}
	path := PinSocket(cfg.Binding.Socket)
	if err := localipc.Prepare(path); err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.server, err = ipc.StartUnix(ctx, path, p)
	p.mu.Unlock()
	if err != nil {
		p.Stop()
		return nil, err
	}
	return p, nil
}

func (p *CustomProxy) Wait() error { return p.server.Wait() }

// rewriteLocation maps a redirect to the App's own origin onto the public
// address. Redirects elsewhere pass to the browser unchanged; the proxy itself
// never follows a redirect.
func rewriteLocation(res *http.Response, target *url.URL, public string) {
	loc := res.Header.Get("Location")
	if loc == "" {
		return
	}
	u, err := url.Parse(loc)
	if err != nil || !u.IsAbs() || !strings.EqualFold(u.Scheme, target.Scheme) || !strings.EqualFold(u.Host, target.Host) {
		return
	}
	pub, _ := url.Parse(public)
	u.Scheme, u.Host = pub.Scheme, pub.Host
	res.Header.Set("Location", u.String())
}

// dropGateCookie keeps an App from setting or clearing Connect's cookie.
func dropGateCookie(res *http.Response) {
	values := res.Header.Values("Set-Cookie")
	if len(values) == 0 {
		return
	}
	res.Header.Del("Set-Cookie")
	for _, v := range values {
		name, _, _ := strings.Cut(v, "=")
		if strings.TrimSpace(name) != gateCookie {
			res.Header.Add("Set-Cookie", v)
		}
	}
}

func (p *CustomProxy) active() bool { return !p.blocked.Load() && p.cfg.State.Active(p.cfg.Binding) }

func (p *CustomProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !p.active() {
		p.Stop()
		http.Error(w, "paused", http.StatusServiceUnavailable)
		return
	}
	if !SameAuthority(r.Host, p.cfg.Binding.Authority) {
		http.Error(w, "misdirected request", http.StatusMisdirectedRequest)
		return
	}
	if grant := p.cfg.App.Ingress; grant != nil && !grant.Accepts(r) {
		http.NotFound(w, r)
		return
	}
	if p.cfg.Authorize != nil {
		if p.cfg.Authorize(r) != nil {
			http.Error(w, "Access sign-in required", http.StatusForbidden)
			return
		}
	}
	if !p.cfg.App.AppSignIn && !p.cfg.Gate.handle(w, r, p.cfg.Binding.Authority, p.cfg.App.Name) {
		return
	}
	if p.target.Path != "" && r.URL.Path == "/" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		http.Redirect(w, r, p.target.Path, http.StatusFound)
		return
	}
	p.rp.ServeHTTP(w, r)
}

// Stop refuses new requests and closes every connection, WebSockets and
// streams included.
func (p *CustomProxy) Stop() {
	p.blocked.Store(true)
	p.mu.Lock()
	l := p.server
	p.mu.Unlock()
	if l != nil {
		_ = l.Close()
	}
	p.tr.CloseIdleConnections()
}
