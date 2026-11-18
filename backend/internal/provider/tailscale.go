package provider

import (
	"bytes"
	"context"
	"dsmapp/internal/connector"
	"dsmapp/internal/supervise"
	"encoding/json"
	"errors"
	"github.com/synopackageland/syno-pkg-land-sdk/supervision"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type TailscaleDriver interface {
	Open(context.Context) (TailscaleSession, error)
}
type TailscaleSession interface {
	Login(context.Context, string) error
	Inspect(context.Context) (TailscaleSnapshot, error)
	Configure(context.Context, []PortMapping) error
	Stop() error
}
type TailscaleSnapshot struct {
	Version                                 string
	BackendState, AuthURL, DNSName, Tailnet string
	Serving                                 bool
	IPs, CertDomains                        []string
	Code, LoginDeadline                     string
	PeersOnline                             *int
}
type tailscaleSaved struct {
	Enabled  bool     `json:"enabled"`
	Settings Settings `json:"settings"`
}

// ErrTailscaleStart names an accepted start that failed, for the status poll.
var ErrTailscaleStart = connector.Coded("tailscale_start_failed", "tailscale did not start")

// ErrTailscaleStop names a stop that failed after bounded retries.
var ErrTailscaleStop = connector.Coded("tailscale_stop_failed", "tailscale cleanup required")

type Tailscale struct {
	OnSupervisionChange func()
	// AppMappings is a trusted startup-only source: the approved private Apps,
	// the only routes Serve carries. Everything else is reached at the NAS's
	// own Tailscale address, which userspace tailscaled forwards to loopback.
	AppMappings func(context.Context) ([]PortMapping, error)
	// HTTPPort detects DSM's HTTP port for the address shown once connected.
	HTTPPort    func(context.Context) (int, error)
	mu          sync.Mutex
	dir         string
	driver      TailscaleDriver
	saved       tailscaleSaved
	loaded      bool
	session     TailscaleSession
	configured  bool
	failed      bool
	failure     error
	attempt     uint64
	restoring   supervise.Restorer
	watchCancel context.CancelFunc
	stage       string
	stageSince  time.Time
	now         func() time.Time
	// RetryDelay is the first wait before a restore retries; default 5 s,
	// doubling to 2 min.
	RetryDelay time.Duration
}

func NewTailscale(dir string, d TailscaleDriver) *Tailscale {
	return &Tailscale{dir: dir, driver: d, now: time.Now}
}
func (*Tailscale) ID() string { return "tailscale" }
func (*Tailscale) Schema() []Field {
	return []Field{{"token", "string", false, true}}
}
func (*Tailscale) Binaries() []BinaryRequirement {
	return []BinaryRequirement{{Name: "tailscale", VersionArgs: []string{"version"}}, {Name: "tailscaled", VersionArgs: []string{"--version"}}}
}
func (*Tailscale) AcceptMappings(m []PortMapping) error {
	if len(m) != 0 {
		return errors.New("Tailscale serves only approved private Apps")
	}
	return nil
}
func (p *Tailscale) Validate(s Settings) error {
	if s.Hostname != "" || len(s.Token) > 4096 || s.ConfirmExposure || s.Mode != "" || len(s.ServiceIDs) != 0 {
		return errors.New("invalid tailscale settings")
	}
	if _, e := exactOrigin(s.LoginServer); s.LoginServer != "" && e != nil {
		return errors.New("invalid Headscale login server")
	}
	if _, e := exactOrigin(s.LoginOrigin); s.LoginOrigin != "" && (s.LoginServer == "" || e != nil) {
		return errors.New("invalid trusted login origin")
	}
	return p.AcceptMappings(s.Mappings)
}
func (p *Tailscale) persist(s tailscaleSaved) error {
	if err := os.MkdirAll(p.dir, 0700); err != nil {
		return errors.New("private tailscale settings unavailable")
	}
	if err := os.Chmod(p.dir, 0700); err != nil {
		return errors.New("private tailscale settings unavailable")
	}
	s.Settings.Token = ""
	raw, _ := json.Marshal(s)
	if err := connector.WritePrivate(filepath.Join(p.dir, "settings.json"), raw); err != nil {
		return errors.New("private tailscale settings unavailable")
	}
	p.saved = s
	p.loaded = true
	return nil
}
func (p *Tailscale) Start(ctx context.Context, s Settings) (connector.Status, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.Validate(s); err != nil {
		return p.status(), err
	}
	if err := p.Preflight(ctx); err != nil {
		return p.status(), err
	}
	p.restoring.Cancel()
	if err := p.stop(); err != nil {
		return p.status(), err
	}
	if err := p.persist(tailscaleSaved{Enabled: true, Settings: s}); err != nil {
		return p.status(), err
	}
	if err := p.open(ctx, s.Token); err != nil {
		state := connector.Status{}
		code := connector.ErrorCode(err)
		if code == "" {
			code = "tailscale_start_failed"
		}
		p.fail(&state, code)
		return p.status(), err
	}
	if _, ok := p.session.(readyWaiter); ok {
		r := p.restorer()
		r.Adopt(&tailscaleDaemon{p: p, session: p.session})
	}
	return p.status(), nil
}
func (p *Tailscale) open(ctx context.Context, key string) error {
	if _, err := p.openDaemon(ctx); err != nil {
		return err
	}
	return p.signIn(ctx, key)
}

// openDaemon starts tailscaled only; signIn brings it up.
func (p *Tailscale) openDaemon(ctx context.Context) (TailscaleSession, error) {
	if p.driver == nil {
		return nil, ErrUnavailable
	}
	p.failure = nil
	p.attempt++
	session, err := p.driver.Open(ctx)
	if errors.Is(err, connector.ErrBinaryRequired) {
		return nil, err
	}
	if err != nil {
		connector.Logf("tailscale: daemon not started: %v", err)
		p.failure = ErrTailscaleStart
		return nil, ErrTailscaleStart
	}
	p.session = session
	p.configured = false
	p.stage = ""
	p.failed = false
	return session, nil
}
func (p *Tailscale) signIn(ctx context.Context, key string) error {
	session := p.session
	var err error
	if custom, ok := session.(interface {
		LoginWithServer(context.Context, string, string, string) error
	}); ok {
		err = custom.LoginWithServer(ctx, key, p.saved.Settings.LoginServer, p.saved.Settings.LoginOrigin)
	} else if p.saved.Settings.LoginServer != "" {
		err = ErrUnavailable
	} else {
		err = session.Login(ctx, key)
	}
	if err != nil {
		connector.Logf("tailscale: start failed: %v", err)
		p.failed = true
		p.failure = ErrTailscaleStart
		_ = p.stop()
		return ErrTailscaleStart
	}

	if execSession, ok := session.(*tailscaleExecSession); ok && execSession.background {
		watchCtx, cancel := context.WithCancel(context.Background())
		p.watchCancel = cancel
		go func() {
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-watchCtx.Done():
					return
				case <-ticker.C:
					p.mu.Lock()
					if p.session == session {
						p.status()
					}
					p.mu.Unlock()
				}
			}
		}()
	}
	return nil
}

// restoreSession starts the saved identity's tailscaled through the shared
// supervise.Restorer: a daemon that may take minutes to answer keeps starting
// in the background while status says connecting, until it answers or the
// connection is stopped or turned off. A restore never asks for a new sign-in.
func (p *Tailscale) restoreSession(ctx context.Context) error {
	r := p.restorer()
	if e := r.Begin(ctx, &tailscaleDaemon{p: p}); e != nil {
		p.failed = true
		return e
	}
	if r.Active() {
		p.failed, p.failure = false, nil
	}
	return nil
}

// tailscaleDaemon is Tailscale's part of a restore; it runs under p.mu except
// WaitReady.
type tailscaleDaemon struct {
	p       *Tailscale
	session TailscaleSession
}

func (d *tailscaleDaemon) Open(ctx context.Context) (e error) {
	d.session, e = d.p.openDaemon(ctx)
	if restored, ok := d.session.(interface{ KeepWaiting() }); ok {
		restored.KeepWaiting()
	}
	return e
}
func (d *tailscaleDaemon) Waits() bool {
	_, ok := d.session.(readyWaiter)
	return ok
}
func (d *tailscaleDaemon) WaitReady(ctx context.Context) error {
	return d.session.(readyWaiter).WaitReady(ctx)
}
func (d *tailscaleDaemon) Alive() bool {
	waiter, ok := d.session.(readyWaiter)
	return ok && d.p.session == d.session && waiter.Alive()
}
func (d *tailscaleDaemon) SignIn(ctx context.Context) error {
	if d.p.session != d.session {
		return ErrTailscaleStart
	}
	return d.p.signIn(ctx, "")
}
func (d *tailscaleDaemon) Close() error { return d.p.stop() }

func (p *Tailscale) Restore(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	saved, err := p.read()
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		p.failed = true
		return err
	}
	p.saved = saved
	p.loaded = true
	if !saved.Enabled {
		return nil
	}
	if err = p.Validate(saved.Settings); err != nil {
		p.failed = true
		return err
	}
	p.restoring.Cancel()
	if err = p.stop(); err != nil {
		return err
	}
	return p.restoreSession(ctx)
}

// read decodes the saved settings strictly; a token on disk is invalid.
func (p *Tailscale) read() (tailscaleSaved, error) {
	var saved tailscaleSaved
	raw, err := connector.ReadPrivate(filepath.Join(p.dir, "settings.json"))
	if os.IsNotExist(err) {
		return saved, err
	}
	if err != nil {
		return saved, ErrUnavailable
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var extra any
	if dec.Decode(&saved) != nil || dec.Decode(&extra) != io.EOF || saved.Settings.Token != "" {
		return tailscaleSaved{}, errors.New("invalid tailscale settings")
	}
	return saved, nil
}
func (p *Tailscale) stop() error {
	if p.watchCancel != nil {
		p.watchCancel()
		p.watchCancel = nil
	}
	p.stage = ""
	if p.session != nil {
		if err := retryStop("tailscale", p.session.Stop); err != nil {
			return ErrTailscaleStop
		}
		p.session = nil
	}
	p.configured = false
	return nil
}
func (p *Tailscale) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.restoring.Cancel()
	return p.stop()
}

// Forget deletes this NAS's Tailscale node key while the connection is off,
// so the next sign-in registers a new device. The old device stays in the
// tailnet until removed in the admin console.
func (p *Tailscale) Forget() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.saved.Enabled || p.session != nil {
		return ErrConnectionEnabled
	}
	for _, name := range []string{"tailscaled.state", "auth-key", registeredMarker} {
		if e := os.Remove(filepath.Join(p.dir, name)); e != nil && !os.IsNotExist(e) {
			return errors.New("Tailscale identity cleanup required")
		}
	}
	return nil
}

func (p *Tailscale) Disable() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.saved
	// A disabled connection is never restored; keep what an earlier process saved.
	if !p.loaded {
		if saved, err := p.read(); err == nil {
			s = saved
		}
	}
	s.Enabled = false
	p.restoring.Cancel()
	if err := p.persist(s); err != nil {
		return err
	}
	p.failed = false
	p.failure = nil
	return p.stop()
}
func (p *Tailscale) Status() connector.Status { p.mu.Lock(); defer p.mu.Unlock(); return p.status() }

func (p *Tailscale) status() connector.Status {
	s := connector.Status{Provider: "tailscale", LoginServer: p.saved.Settings.LoginServer, LoginOrigin: p.saved.Settings.LoginOrigin, State: "disconnected"}
	if !p.saved.Enabled {
		if p.failed {
			s.State = "failed"
			s.Code = connector.ErrorCode(p.failure)
			return s
		}
		if p.session != nil {
			s.State = "stopping"
		} else {
			// tailscaled saves a machine key before any sign-in; only a node
			// that signed in is a device in the tailnet.
			s.KeptIdentity = registered(p.dir) && nonEmptyFile(filepath.Join(p.dir, "tailscaled.state"))
		}
		return s
	}
	snapshot := p.restoring.Snapshot()
	if snapshot.State != "stopped" {
		s.Supervision = &snapshot
	}
	if snapshot.State == "failed" {
		s.State = "failed"
		s.Code = snapshot.Reason
		return s
	}
	if p.restoring.Active() {
		s.State = "connecting"
		s.Phase = string(snapshot.State)
		if snapshot.State == "waiting" || snapshot.State == "backoff" {
			s.State = string(snapshot.State)
		}
		return s
	}

	s.State = "failed"
	if p.session == nil || p.failed {
		s.Code = connector.ErrorCode(p.failure)
		return s
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	observed, err := p.session.Inspect(ctx)
	if err != nil {
		// A swapping NAS can stall reads for minutes. A read that did not
		// answer is unknown, and tailscaled keeps running with its sign-in.
		if errors.Is(err, ErrDaemonExited) {
			// The connection stays on: a repair or the next boot starts the
			// daemon again with its saved sign-in.
			connector.Logf("tailscale: daemon exited (attempt %d)", p.attempt)
			p.failed = true
			p.failure = ErrTailscaleStart
			s.Code = connector.ErrorCode(p.failure)
			_ = p.stop()
			return s
		}
		if code := connector.ErrorCode(err); code != "" {
			// Tailscale itself reported it, such as a sign-in that expired.
			p.fail(&s, code)
			return s
		}
		s.State = "unknown"
		s.Phase = "degraded"
		return s
	}

	s.State = "connecting"
	s.LoggedIn = observed.BackendState == "Running"
	if s.LoggedIn {
		markRegistered(p.dir)
	}
	s.LoginDeadline = observed.LoginDeadline
	if observed.Code != "" {
		s.Code = observed.Code
	}
	switch observed.BackendState {
	case "NeedsLogin":
		if observed.AuthURL == "" {
			s.Code = "control_unreachable"
			return s
		}
		s.LoginURL = trustedTailscaleURL(observed.AuthURL, p.saved.Settings.LoginServer, p.saved.Settings.LoginOrigin)
		if s.LoginURL == "" {
			p.fail(&s, "login_url_rejected")
			return s
		}
		s.State = "needs-login"
		return s
	case "NeedsMachineAuth":
		s.State = "needs-machine-auth"
		s.Code = "machine_approval_required"
		return s
	case "NoState", "Starting", "Stopped":
		return s
	case "InUseOtherUser":
		p.fail(&s, "daemon_identity_conflict")
		return s
	case "Running":
	default:
		p.fail(&s, "provider_status_invalid")
		return s
	}
	name := strings.TrimSuffix(observed.DNSName, ".")
	if len(name) > 253 || !dnsName.MatchString(name) {
		name = ""
	}
	s.MagicDNS = name
	s.Tailnet = observed.Tailnet
	for _, raw := range observed.IPs {
		if ip, e := netip.ParseAddr(raw); e == nil && !ip.IsUnspecified() && !ip.IsLoopback() && !ip.IsMulticast() {
			s.TailscaleIPs = append(s.TailscaleIPs, ip.String())
		}
	}
	if len(s.TailscaleIPs) == 0 {
		p.fail(&s, "provider_status_invalid")
		return s
	}
	// Peers reach DSM at the NAS's own Tailscale address, as on NetBird.
	if p.HTTPPort != nil {
		if port, e := p.HTTPPort(ctx); e == nil && port > 0 && port <= 65535 {
			for _, ip := range s.TailscaleIPs {
				if netip.MustParseAddr(ip).Is4() {
					s.Endpoints = append(s.Endpoints, Endpoint{URL: "http://" + net.JoinHostPort(ip, strconv.Itoa(port)) + "/", LocalPort: port})
				}
			}
			if name != "" {
				s.Endpoints = append(s.Endpoints, Endpoint{URL: "http://" + net.JoinHostPort(name, strconv.Itoa(port)) + "/", LocalPort: port})
			}
		}
	}
	// Serve carries only the approved private Apps; with none the tailnet is
	// all there is to wait for.
	apps := []PortMapping{}
	if p.AppMappings != nil {
		if extra, e := p.AppMappings(ctx); e == nil {
			apps = extra
		}
	}
	if len(apps) == 0 && !p.configured {
		p.configured = true
		if e := p.configureApps(ctx); e != nil {
			p.configured = false
		}
	}
	if len(apps) == 0 {
		s.State = "connected"
		s.PeersOnline = observed.PeersOnline
		return s
	}
	if !p.configured {
		if p.stage != "serve" && p.stage != "review" {
			p.stage = "serve"
			p.stageSince = p.now()
		}
		if p.stage == "serve" && p.now().Sub(p.stageSince) >= 30*time.Second {
			p.fail(&s, "feature_approval_required")
			return s
		}
		if err = p.configureApps(ctx); err != nil {
			s.Code = connector.ErrorCode(err)
			if s.Code == "pin_proxy_required" {
				// Routes are withdrawn and the tailnet stays up: this waits for
				// the administrator's review, not for a feature approval.
				p.stage = "review"
				s.State = "failed"
				return s
			}
			if s.Code == "" {
				s.Code = "serve_prerequisite_missing"
			}
			return s
		}
		p.configured = true
		p.stage = "serve"
		p.stageSince = p.now()
	}
	if !observed.Serving {
		if p.stage == "serve" && p.now().Sub(p.stageSince) >= 30*time.Second {
			p.fail(&s, "feature_approval_required")
		}
		return s
	}
	s.State = "connected"
	s.PeersOnline = observed.PeersOnline
	return s
}
func (p *Tailscale) fail(s *connector.Status, code string) {
	p.restoring.Refuse(code)
	p.failed = true
	p.failure = connector.Coded(code, "tailscale attempt failed")
	disabled := p.saved
	disabled.Enabled = false
	if e := p.persist(disabled); e != nil {
		connector.Logf("tailscale: failed attempt gate not saved")
	}
	s.State = "failed"
	s.Code = code
	s.LoginURL = ""
	_ = p.stop()
}
func (p *Tailscale) Endpoints() []Endpoint { return p.Status().Endpoints }

func (d *tailscaleDaemon) LastExit() supervision.Exit {
	if s, ok := d.session.(interface{ LastExit() supervision.Exit }); ok {
		return s.LastExit()
	}
	return supervision.Exit{Code: -1, Reason: "daemon_exit"}
}

func (p *Tailscale) restorer() *supervise.Restorer {
	r := &p.restoring
	r.Name, r.Lock, r.First = "tailscale", &p.mu, p.RetryDelay
	r.Changed = p.OnSupervisionChange
	r.Preflight = p.Preflight
	r.Monitor = true
	if dep, ok := p.driver.(interface {
		Dependency(context.Context, Settings) error
	}); ok {
		settings := p.saved.Settings
		r.Dependency = func(ctx context.Context) error { return dep.Dependency(ctx, settings) }
	}

	r.Attempt = func() uint64 { return p.attempt }
	r.Retrying = func() { p.failed, p.failure = false, nil }
	return r
}

func (p *Tailscale) RecoveryTerminal() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.restoring.Snapshot()
	return s.State == supervision.Failed || (s.State == supervision.Stopped && p.restoring.Policy() != nil)
}
