package apps

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"dsmapp/internal/bigupload"
	"dsmapp/internal/localipc"
	"github.com/synopackageland/syno-pkg-land-sdk/ipc"
)

// PinState adapts the durable owner of spec section 7.2. Active must compare
// the complete target/scope/socket and pin binding, not just its package name. Suspend must persist
// first, without calling back into the proxy; projection/provider cleanup follows
// after this call returns. A persistence failure still closes traffic locally.
// Re-enabling always replaces the proxy with a newly confirmed immutable binding.
type PinState interface {
	Active(PinBinding) bool
	Suspend(context.Context, PinBinding, string, bool) error
}

// PinConfig describes exactly one target and provider scope. The state adapter
// must include Socket, Provider and the protection digest in its binding identity.
type PinConfig struct {
	Authorize        func(*http.Request) error
	Binding          PinBinding
	Provider         string // tailscale | cloudflared
	InfoPath         string
	GatewayJSON      string
	ProtectionDigest string
	State            PinState
	Verifier         *Verifier
	// Uploads carries large browser uploads (docs/large-uploads.md). Used only
	// on a public DSM binding; nil leaves pages and requests unchanged.
	Uploads *bigupload.Manager
}

// PinObservation is a fresh discovery snapshot, not authority supplied by a
// browser. Unknown duplicate-port scans refuse temporarily, never imply success.
type PinObservation struct {
	DSMPort          int
	MagicDNS         string
	ProtectionDigest string
	Paths            map[string]string
	Ports            map[string]int
	UnknownPorts     bool
}

type PinRuntime struct {
	Status        string
	Reason        string
	MismatchSince time.Time
}

// PinProxy forwards HTTP/1.1 through TLS with a connection-local pin and ping.
// All configuration is immutable; create a replacement after explicit approval.
type PinProxy struct {
	listenerMu      sync.Mutex
	server          *ipc.Server
	lastWaiting     atomic.Bool
	pendingComplete bool
	persistErr      error
	removed         bool
	waiting         atomic.Bool
	cfg             PinConfig
	tr              *http.Transport
	rp              *httputil.ReverseProxy
	mu              sync.Mutex
	runtime         PinRuntime
	missing         int
	blocked         atomic.Bool
	cmu             sync.Mutex
	conns           map[*pinConn]struct{}
	cacheMu         sync.Mutex
	gatewayInfo     os.FileInfo
	gatewayPort     int
	uploads         http.Handler
}

func NewPinProxy(cfg PinConfig) (*PinProxy, error) {
	b := cfg.Binding
	if b.EntryPath != "" && (!validIngressPath(b.Package, b.EntryPath) || b.Target != TargetApp) {
		return nil, ErrInvalid
	}
	if cfg.State == nil || cfg.Verifier == nil || b.Port < 1 || b.Port > 65535 || net.ParseIP(cfg.Verifier.o.Host) == nil || !net.ParseIP(cfg.Verifier.o.Host).IsLoopback() ||
		(b.Mode != ModeW && b.Mode != ModeS) || (b.Target != TargetApp && b.Target != TargetDSM) ||
		!validAuthority(b.Authority, cfg.Provider == "tailscale") ||
		(cfg.Provider != "tailscale" && cfg.Provider != "cloudflared") ||
		(b.Target == TargetApp && (b.Package == "" || cfg.InfoPath == "" || cfg.GatewayJSON == "")) {
		return nil, errors.New("invalid pin proxy configuration")
	}
	if b.Mode == ModeW && len(b.Names) == 0 || b.Mode == ModeS && !hexPattern.MatchString(b.SPKI) {
		return nil, errors.New("missing confirmed pin")
	}
	if cfg.ProtectionDigest == "" {
		cfg.ProtectionDigest = b.Summary
	}
	cfg.Binding.Names = append([]string(nil), b.Names...)
	p := &PinProxy{cfg: cfg, runtime: PinRuntime{Status: "active"}, conns: make(map[*pinConn]struct{})}
	target := &url.URL{Scheme: "https", Host: net.JoinHostPort(cfg.Verifier.o.Host, strconv.Itoa(b.Port))}
	p.tr = &http.Transport{Proxy: nil, DialTLSContext: p.dial, ForceAttemptHTTP2: false,
		MaxIdleConns: 128, MaxIdleConnsPerHost: 64, IdleConnTimeout: 30 * time.Second}
	p.rp = &httputil.ReverseProxy{
		Rewrite: func(r *httputil.ProxyRequest) {
			r.SetURL(target)
			if b.EntryPath != "" && (r.In.URL.Path == "/" || r.In.URL.Path == "/webapi/auth.cgi" || strings.HasPrefix(r.In.URL.Path, "/webapi/entry.cgi")) {
				r.Out.Header.Set("Accept-Encoding", "identity")
			}
			if p.uploads != nil && bigupload.Navigation(r.In) {
				r.Out.Header.Set("Accept-Encoding", "identity")
			}
			r.Out.Host = b.Authority
			if cfg.Provider == "cloudflared" {
				r.Out.Host = r.In.Host
			}
			for _, h := range []string{"Forwarded", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Forwarded-For"} {
				r.Out.Header.Del(h)
			}
		}, Transport: p.tr, FlushInterval: 0, BufferPool: pinBuffers{},
		// ReverseProxy flushes SSE and unknown-length responses immediately even
		// with a zero interval; WebSocket upgrades use its separate duplex path.
		ModifyResponse: func(res *http.Response) error {
			if b.EntryPath != "" {
				entry := PackageEntry{Package: b.Package, Port: b.Port, Path: b.EntryPath}
				if err := ingressLoginDocument(res, entry); err != nil {
					return err
				}
				if err := ingressLoginResponse(res, entry); err != nil {
					return err
				}
			}
			if p.uploads != nil {
				if err := bigupload.Inject(res); err != nil {
					return err
				}
			}
			return batchPinBody(res)
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, _ error) {
			p.lastWaiting.Store(true)
			writeEntryUnavailable(w, r, b.EntryPath, "unavailable")
		},
	}
	if cfg.Uploads != nil && cfg.Provider == "cloudflared" && b.Target == TargetDSM {
		p.uploads = cfg.Uploads.Handler(p.forwardDSM(target))
	}
	return p, nil
}

// forwardDSM sends an upload's request to DSM over the pinned transport, as
// Rewrite does for forwarded requests.
func (p *PinProxy) forwardDSM(target *url.URL) bigupload.Forwarder {
	return func(req *http.Request) (*http.Response, error) {
		out := req.Clone(req.Context())
		out.URL.Scheme, out.URL.Host = target.Scheme, target.Host
		out.RequestURI = ""
		if p.cfg.Provider != "cloudflared" {
			out.Host = p.cfg.Binding.Authority
		}
		for _, h := range []string{"Forwarded", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Forwarded-For"} {
			out.Header.Del(h)
		}
		return p.tr.RoundTrip(out)
	}
}

// StartPinProxy binds before publishing the approved route to any provider.
func StartPinProxy(ctx context.Context, cfg PinConfig) (*PinProxy, error) {
	p, err := NewPinProxy(cfg)
	if err != nil {
		return nil, err
	}
	path := PinSocket(cfg.Binding.Socket)
	if err := localipc.Prepare(path); err != nil {
		return nil, err
	}
	p.listenerMu.Lock()
	p.server, err = ipc.StartUnix(ctx, path, p)
	p.listenerMu.Unlock()
	if err != nil {
		p.Stop()
		return nil, err
	}
	return p, nil
}

func (p *PinProxy) Wait() error { return p.server.Wait() }

func (p *PinProxy) Runtime() PinRuntime { p.mu.Lock(); defer p.mu.Unlock(); return p.runtime }
func (p *PinProxy) active() bool        { return !p.blocked.Load() && p.cfg.State.Active(p.cfg.Binding) }

// Pause is shared by every permanent refusal. It never auto-resumes. Even if
// durable storage fails, all current streams and WebSockets close locally.
func (p *PinProxy) Pause(ctx context.Context, reason string, removed bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.blocked.Load() {
		return p.persistErr
	}
	p.blocked.Store(true)
	err := p.cfg.State.Suspend(ctx, p.cfg.Binding, reason, removed)
	p.persistErr, p.removed, p.pendingComplete = err, removed, true
	p.runtime.Status, p.runtime.Reason = "paused", reason
	if removed {
		p.runtime.Status = "removed"
	}
	p.stopListener()
	p.closeConnections()
	if err == nil {
		if complete, ok := p.cfg.State.(interface {
			AfterBlock(context.Context, PinBinding) error
		}); ok {
			err = complete.AfterBlock(ctx, p.cfg.Binding)
			p.pendingComplete = err != nil
		}
	}
	return err
}

// Stop is called immediately after the owner durably revokes/pauses a binding.
// It also closes hijacked downstreams and in-progress handshakes, not just idle HTTP.
func (p *PinProxy) Stop() {
	p.blocked.Store(true)
	p.stopListener()
	p.closeConnections()
}
func (p *PinProxy) stopListener() {
	p.listenerMu.Lock()
	l := p.server
	p.listenerMu.Unlock()
	if l != nil {
		_ = l.Close()
	}
}
func (p *PinProxy) closeConnections() {
	p.cmu.Lock()
	all := make([]*pinConn, 0, len(p.conns))
	for c := range p.conns {
		all = append(all, c)
	}
	p.cmu.Unlock()
	for _, c := range all {
		_ = c.Close()
	}
	p.tr.CloseIdleConnections()
}

type pinConn struct {
	net.Conn
	p    *PinProxy
	once sync.Once
}

func (c *pinConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { c.p.cmu.Lock(); delete(c.p.conns, c); c.p.cmu.Unlock() })
	return err
}
func (p *PinProxy) track(c net.Conn) (net.Conn, error) {
	p.cmu.Lock()
	defer p.cmu.Unlock()
	if p.blocked.Load() {
		c.Close()
		return nil, &Reject{ReasonPaused}
	}
	pc := &pinConn{Conn: c, p: p}
	p.conns[pc] = struct{}{}
	return pc, nil
}

type bufferedPinConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *bufferedPinConn) Read(b []byte) (int, error) { return c.r.Read(b) }

func (p *PinProxy) observe(ctx context.Context, reason string) error {
	p.mu.Lock()
	now := p.cfg.Verifier.Now()
	if reason == "" || !pinMismatch(reason) {
		p.runtime.MismatchSince = time.Time{}
	} else if p.runtime.MismatchSince.IsZero() {
		p.runtime.MismatchSince = now
	}
	expired := !p.runtime.MismatchSince.IsZero() && now.Sub(p.runtime.MismatchSince) >= p.cfg.Verifier.o.PauseAfter
	p.mu.Unlock()
	if expired {
		return p.Pause(ctx, "cert_mismatch", false)
	}
	return nil
}

func (p *PinProxy) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	if !p.active() || p.waiting.Load() {
		return nil, &Reject{ReasonPaused}
	}
	v, b := p.cfg.Verifier, p.cfg.Binding
	raw, err := (&net.Dialer{Timeout: v.o.HandshakeTimeout}).DialContext(ctx, "tcp", net.JoinHostPort(v.o.Host, strconv.Itoa(b.Port)))
	if err != nil {
		return nil, err
	}
	raw, err = p.track(raw)
	if err != nil {
		return nil, err
	}
	// InsecureSkipVerify disables Go's hostname default only: VerifyConnection
	// below enforces the explicit W/S policy. No SNI, HTTP/2 or session cache.
	c := tls.Client(raw, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"},
		VerifyConnection: func(cs tls.ConnectionState) error {
			reason := v.Check(ctx, b, cs.PeerCertificates)
			_ = p.observe(ctx, reason)
			if !p.active() {
				return &Reject{ReasonPaused}
			}
			if reason != "" {
				return &Reject{reason}
			}
			return nil
		}})
	hctx, cancel := context.WithTimeout(ctx, v.o.HandshakeTimeout)
	defer cancel()
	if err = c.HandshakeContext(hctx); err != nil {
		raw.Close()
		return nil, err
	}
	br := bufio.NewReader(c)
	if b.Target == TargetApp {
		if reason := ping(c, br, b, v.o.HandshakeTimeout); reason != "" {
			raw.Close()
			if reason == ReasonRouteBad {
				_ = p.Pause(ctx, "route_mismatch", false)
			}
			return nil, &Reject{reason}
		}
	}
	if !p.active() {
		raw.Close()
		return nil, &Reject{ReasonPaused}
	}
	return &bufferedPinConn{Conn: c, r: br}, nil
}

// ping diagnoses routing on the very connection later carrying user traffic.
func ping(c net.Conn, br *bufio.Reader, b PinBinding, timeout time.Duration) string {
	_ = c.SetDeadline(time.Now().Add(timeout))
	defer c.SetDeadline(time.Time{})
	req := &http.Request{Method: "GET", URL: &url.URL{Path: (PackageEntry{Path: b.EntryPath}).PingPath()}, Host: b.Authority, Header: http.Header{"User-Agent": []string{"spl-pinproxy"}}}
	if err := req.Write(c); err != nil {
		return ReasonPingMissing
	}
	res, err := http.ReadResponse(br, req)
	if err != nil {
		return ReasonPingMissing
	}
	// On errors the caller closes the connection; never drain an unbounded body.
	if res.StatusCode == 502 || res.StatusCode == 503 || res.StatusCode == 504 {
		return ReasonPingMissing
	}
	if res.StatusCode != 200 {
		return ReasonRouteBad
	}
	want, _ := json.Marshal(struct {
		Package string `json:"package"`
	}{b.Package})
	body, err := io.ReadAll(io.LimitReader(res.Body, int64(len(want)+1)))
	if err != nil {
		return ReasonPingMissing
	}
	if string(body) != string(want) {
		return ReasonRouteBad
	}
	if res.Close {
		return ReasonPingMissing
	}
	_ = res.Body.Close()
	return ""
}

func (v *Verifier) probeReady(ctx context.Context, target PinTarget, port int, pkg, authority string, entries ...string) ([]*x509.Certificate, error) {
	if port < 1 || port > 65535 || net.ParseIP(v.o.Host) == nil || !net.ParseIP(v.o.Host).IsLoopback() {
		return nil, errors.New("invalid loopback target")
	}
	dctx, cancel := context.WithTimeout(ctx, v.o.HandshakeTimeout)
	defer cancel()
	raw, err := (&net.Dialer{}).DialContext(dctx, "tcp", net.JoinHostPort(v.o.Host, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	defer raw.Close()
	c := tls.Client(raw, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}})
	if err = c.HandshakeContext(dctx); err != nil {
		return nil, err
	}
	if target == TargetApp {
		if !SameAuthority(authority, authority) || pkg == "" {
			return nil, errors.New("invalid approval target")
		}
		entryPath := ""
		if len(entries) > 0 {
			entryPath = entries[0]
		}
		if r := ping(c, bufio.NewReader(c), PinBinding{EntryPath: entryPath, Package: pkg, Authority: authority}, v.o.HandshakeTimeout); r != "" {
			return nil, &Reject{r}
		}
	}
	return c.ConnectionState().PeerCertificates, nil
}

func (p *PinProxy) gateway() (int, error) {
	st, err := os.Stat(p.cfg.GatewayJSON)
	if err != nil {
		return 0, err
	}
	p.cacheMu.Lock()
	defer p.cacheMu.Unlock()
	if p.gatewayInfo != nil && os.SameFile(st, p.gatewayInfo) && st.ModTime() == p.gatewayInfo.ModTime() && st.Size() == p.gatewayInfo.Size() {
		return p.gatewayPort, nil
	}
	port, err := GatewayPort(p.cfg.GatewayJSON)
	if err == nil {
		p.gatewayInfo, p.gatewayPort = st, port
	}
	return port, err
}

func GatewayPort(path string) (int, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > 65536 {
		return 0, errors.New("invalid gateway metadata")
	}
	var v struct {
		Listen struct {
			HTTPS int `json:"https"`
		} `json:"listen"`
	}
	dec := json.NewDecoder(io.LimitReader(f, 65537))
	var extra any
	if dec.Decode(&v) != nil || dec.Decode(&extra) != io.EOF || v.Listen.HTTPS < 1 || v.Listen.HTTPS > 65535 {
		return 0, errors.New("invalid gateway port")
	}
	return v.Listen.HTTPS, nil
}

func (p *PinProxy) metadata(ctx context.Context) string {
	if p.cfg.Binding.Target == TargetDSM {
		return ""
	}
	if _, err := os.Stat(p.cfg.InfoPath); err != nil {
		return "waiting"
	}
	port, err := p.gateway()
	if p.cfg.Binding.EntryPath != "" {
		raw, e := readFile(p.cfg.GatewayJSON, 16<<10, false)
		var g struct {
			Package     string `json:"package"`
			IngressPath string `json:"ingress_path"`
			Listen      struct {
				HTTPS int `json:"https"`
			} `json:"listen"`
		}
		if e == nil && json.Unmarshal(raw, &g) == nil && g.Package == p.cfg.Binding.Package && g.IngressPath == p.cfg.Binding.EntryPath && validIngressPath(g.Package, g.IngressPath) && g.Listen.HTTPS == 0 {
			port, err = p.cfg.Binding.Port, nil
		}
	}
	if err != nil {
		raw, e := readFile(p.cfg.GatewayJSON, 16<<10, false)
		var g struct {
			Package     string `json:"package"`
			IngressPath string `json:"ingress_path"`
			Listen      struct {
				HTTPS int `json:"https"`
			} `json:"listen"`
		}
		if e == nil && json.Unmarshal(raw, &g) == nil && g.Package == p.cfg.Binding.Package && g.IngressPath != "" {
			_ = p.Pause(ctx, "entry_changed", false)
			return "entry_changed"
		}
		return "waiting"
	}
	if port != p.cfg.Binding.Port {
		_ = p.Pause(ctx, "port_changed", false)
		return "paused"
	}
	return ""
}
func (p *PinProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !p.active() {
		p.Stop()
		writeEntryUnavailable(w, r, p.cfg.Binding.EntryPath, p.Runtime().Reason)
		return
	}
	if p.cfg.Provider == "cloudflared" && !SameAuthority(r.Host, p.cfg.Binding.Authority) {
		http.Error(w, "misdirected request", 421)
		return
	}
	if p.cfg.Authorize != nil {
		if p.cfg.Authorize(r) != nil {
			http.Error(w, "Access sign-in required", http.StatusForbidden)
			return
		}
	}
	if reason := p.metadata(r.Context()); reason != "" {
		p.lastWaiting.Store(true)
		writeEntryUnavailable(w, r, p.cfg.Binding.EntryPath, reason)
		return
	}
	if p.waiting.Load() {
		writeEntryUnavailable(w, r, p.cfg.Binding.EntryPath, "unavailable")
		return
	}
	if p.uploads != nil && bigupload.Handles(r.URL.Path) {
		p.uploads.ServeHTTP(w, r)
		return
	}
	if b := p.cfg.Binding; b.EntryPath != "" {
		if r.URL.Path == ingressResumeScript {
			serveIngressResumeScript(w, r, PackageEntry{Package: b.Package, Port: b.Port, Path: b.EntryPath})
			return
		}
		if ingressNavigation(w, r, PackageEntry{Package: b.Package, Port: b.Port, Path: b.EntryPath}) {
			return
		}
		if r.URL.Path == "/" && r.URL.RawQuery == "" {
			http.Redirect(w, r, b.EntryPath, http.StatusTemporaryRedirect)
			return
		}
		if !allowIngressRequest(r, PackageEntry{Package: b.Package, Path: b.EntryPath, Port: b.Port}) {
			http.NotFound(w, r)
			return
		}
	}
	p.lastWaiting.Store(false)
	p.rp.ServeHTTP(w, r)
}

// Check is the 30-second safety check. Missing INFO closes streams on the first
// observation, but removal requires two consecutive polls and an absent package
// directory. Missing upgrade metadata alone never authorizes cloud deletion.
func (p *PinProxy) Check(ctx context.Context, o PinObservation) error {
	if !p.active() {
		p.Stop()
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.persistErr != nil {
			p.persistErr = p.cfg.State.Suspend(ctx, p.cfg.Binding, p.runtime.Reason, p.removed)
		}
		if p.persistErr != nil {
			return p.persistErr
		}
		if p.pendingComplete {
			if complete, ok := p.cfg.State.(interface {
				AfterBlock(context.Context, PinBinding) error
			}); ok {
				err := complete.AfterBlock(ctx, p.cfg.Binding)
				p.pendingComplete = err != nil
				return err
			}
		}
		return nil
	}
	b := p.cfg.Binding
	if b.Target == TargetApp {
		_, err := os.Stat(p.cfg.InfoPath)
		p.mu.Lock()
		// Lstat distinguishes a removed package from a retained/dangling entry
		// during upgrade. Permission and I/O errors are not removal evidence.
		_, packageErr := os.Lstat(filepath.Dir(p.cfg.InfoPath))
		if os.IsNotExist(err) && os.IsNotExist(packageErr) {
			p.missing++
		} else {
			p.missing = 0
		}
		missing := p.missing
		p.mu.Unlock()
		if err != nil {
			p.closeConnections()
			if missing >= 2 && os.IsNotExist(err) {
				return p.Pause(ctx, "removed", true)
			}
			return nil
		}
		if p.metadata(ctx) != "" {
			p.closeConnections()
			return nil
		}
		if b.EntryPath != "" && (o.Paths[b.Package] != b.EntryPath || o.DSMPort != b.Port) {
			return p.Pause(ctx, "entry_changed", false)
		}
		if o.ProtectionDigest == "" || o.UnknownPorts || o.DSMPort < 1 || o.Ports[b.Package] != b.Port {
			p.waiting.Store(true)
			p.closeConnections()
			return errors.New("waiting for discovery")
		}
		if o.ProtectionDigest != p.cfg.ProtectionDigest {
			return p.Pause(ctx, "protection_changed", false)
		}
		if b.EntryPath == "" && o.DSMPort == b.Port {
			return p.Pause(ctx, "duplicate_port", false)
		}
		for pkg, port := range o.Ports {
			if b.EntryPath == "" && pkg != b.Package && port == b.Port && o.Paths[pkg] == "" {
				return p.Pause(ctx, "duplicate_port", false)
			}
		}
	} else if o.DSMPort != b.Port {
		if o.DSMPort == 0 {
			p.waiting.Store(true)
			p.closeConnections()
			return errors.New("waiting for DSM port")
		}
		return p.Pause(ctx, "dsm_port_changed", false)
	}
	if p.cfg.Provider == "tailscale" {
		if o.MagicDNS == "" {
			p.waiting.Store(true)
			p.closeConnections()
			return errors.New("waiting for private authority")
		}
		h, _, err := net.SplitHostPort(b.Authority)
		if err != nil || !strings.EqualFold(strings.TrimSuffix(o.MagicDNS, "."), h) {
			return p.Pause(ctx, "authority_changed", false)
		}
	}
	p.waiting.Store(false)
	chain, err := p.cfg.Verifier.Probe(ctx, b.Port)
	if err != nil {
		return p.observe(ctx, ReasonRefMissing)
	}
	return p.observe(ctx, p.cfg.Verifier.Check(ctx, b, chain))
}

// SameAuthority validates host[:port], normalizing only case and default :443.
func SameAuthority(a, b string) bool {
	norm := func(s string) string {
		if s == "" || strings.ContainsAny(s, " \t\r\n/@?#%\\") {
			return ""
		}
		u, err := url.Parse("https://" + s)
		if err != nil || u.Host != s || u.Hostname() == "" || strings.HasSuffix(s, ":") {
			return ""
		}
		host := u.Hostname()
		if strings.Contains(host, ":") && net.ParseIP(host) == nil {
			return ""
		}
		port := u.Port()
		if port == "" {
			port = "443"
		}
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return ""
		}
		return strings.ToLower(net.JoinHostPort(host, strconv.Itoa(n)))
	}
	x, y := norm(a), norm(b)
	return x != "" && x == y
}

// TLS reads can return one small record at a time. For finite, non-streaming
// responses, fill the copy buffer before writing to the Unix socket. This bounds
// buffering per response and avoids a socket write/flush for every TLS record.
// SSE (including a fixed-length SSE response), unknown lengths and protocol
// upgrades retain the reverse proxy's immediate streaming behavior.
func batchPinBody(res *http.Response) error {
	contentType, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type"))
	if res.StatusCode != http.StatusSwitchingProtocols && res.ContentLength > 0 && contentType != "text/event-stream" && res.Body != http.NoBody {
		res.Body = &pinBulkBody{ReadCloser: res.Body, remaining: res.ContentLength}
	}
	return nil
}

type pinBulkBody struct {
	io.ReadCloser
	remaining int64
}

func (b *pinBulkBody) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	if b.remaining == 0 {
		return 0, io.EOF
	}
	n, err := io.ReadFull(b.ReadCloser, p[:min(int64(len(p)), b.remaining)])
	b.remaining -= int64(n)
	return n, err
}

const pinCopyBufferSize = 128 << 10

// Reuse bounded copy buffers; buffering is only enabled for finite bulk bodies.
var pinBufferPool = sync.Pool{New: func() any { return make([]byte, pinCopyBufferSize) }}

type pinBuffers struct{}

func (pinBuffers) Get() []byte  { return pinBufferPool.Get().([]byte) }
func (pinBuffers) Put(b []byte) { pinBufferPool.Put(b) }
