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
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"
)

type NetBirdDriver interface {
	Open(context.Context) (NetBirdSession, error)
}
type NetBirdSession interface {
	Login(context.Context, string, string) error
	Inspect(context.Context) (NetBirdSnapshot, error)
	Stop() error
}
type NetBirdSnapshot struct {
	ManagementConnected, SignalConnected         bool
	IP                                           string
	DaemonStatus, FQDN, LoginURL, UserCode, Code string
	LoginOrigin                                  string
	Deadline                                     time.Time
	PeersOnline                                  *int
}
type netbirdSaved struct {
	Enabled  bool     `json:"enabled"`
	Settings Settings `json:"settings"`
}
type NetBird struct {
	OnSupervisionChange      func()
	verifiedFQDN, verifiedIP string
	verifiedPort             int
	healthPort               int
	mu                       sync.Mutex
	dir                      string
	driver                   NetBirdDriver
	saved                    netbirdSaved
	session                  NetBirdSession
	failed                   bool
	failure                  error
	attempt                  uint64
	started                  time.Time
	connectedAt              time.Time
	wasReady                 bool
	// restored marks a session opened from the saved identity: while it
	// connects it keeps waiting, since no new sign-in can make it faster.
	restored  bool
	restoring supervise.Restorer
	// RetryDelay is the first wait before a restore retries; default 5 s,
	// doubling to 2 min.
	RetryDelay time.Duration
	HTTPPort   func(context.Context) (int, error)
}

// readyWaiter is a session whose daemon may take minutes to answer, as on a
// swapping NAS. Restore waits for it without holding the provider lock.
type readyWaiter interface {
	WaitReady(context.Context) error
	Alive() bool
}

// A fresh sign-in that NetBird keeps "Connecting" fails after this long; a
// restored identity keeps waiting instead.
const netbirdConnectWindow = 5 * time.Minute

// ErrNetBirdStart names an accepted start that failed, for the status poll.
var ErrNetBirdStart = connector.Coded("netbird_start_failed", "NetBird did not start")

// ErrNetBirdStop names a stop that failed after bounded retries.
var ErrNetBirdStop = connector.Coded("netbird_stop_failed", "NetBird cleanup required")

func NewNetBird(dir string, driver NetBirdDriver) *NetBird { return &NetBird{dir: dir, driver: driver} }
func (*NetBird) ID() string                                { return "netbird" }
func (*NetBird) Schema() []Field {
	return []Field{{"managementUrl", "string", false, false}, {"token", "string", false, true}, {"loginOrigins", "array", false, false}, {"confirmManagementPolicy", "boolean", false, false}}
}
func (*NetBird) Binaries() []BinaryRequirement {
	return []BinaryRequirement{{Name: "netbird", VersionArgs: []string{"version"}}}
}
func (*NetBird) Endpoints() []Endpoint { return nil }
func (*NetBird) AcceptMappings(m []PortMapping) error {
	if len(m) != 0 {
		return errors.New("NetBird ports are controlled by management policy")
	}
	return nil
}
func netbirdManagementURL(raw string) (string, error) {
	if raw == "" {
		raw = "https://api.netbird.io:443"
	}
	u, e := url.Parse(raw)
	if e != nil || len(raw) > 2048 || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") {
		return "", ErrUnavailable
	}
	if u.Scheme != "https" || !nbClean(raw) || !nbHost.MatchString(u.Hostname()) || strings.Contains(raw, "#") {
		return "", ErrUnavailable
	}
	u.Path = ""
	return u.String(), nil
}
func (p *NetBird) Validate(s Settings) error {
	if len(s.Token) > 4096 || strings.ContainsAny(s.Token, "\r\n\x00") || s.Hostname != "" || s.Mode != "" || len(s.ServiceIDs) != 0 {
		return ErrUnavailable
	}
	if _, e := netbirdManagementURL(s.ManagementURL); e != nil {
		return e
	}
	if len(s.LoginOrigins) > 8 {
		return ErrUnavailable
	}
	for _, origin := range s.LoginOrigins {
		if !netbirdOrigin(origin) {
			return ErrUnavailable
		}
	}
	management, _ := netbirdManagementURL(s.ManagementURL)
	if s.Token != "" && management != "https://api.netbird.io:443" && management != "https://api.netbird.io" && !s.ConfirmManagementPolicy {
		return nbError("management_confirmation_required")
	}
	return p.AcceptMappings(s.Mappings)
}
func (p *NetBird) Preflight(ctx context.Context) error {
	if p.driver == nil {
		return connector.ErrBinaryRequired
	}
	if d, ok := p.driver.(interface{ CheckBinaries(context.Context) error }); ok {
		return d.CheckBinaries(ctx)
	}
	return nil
}
func (p *NetBird) persist(s netbirdSaved) error {
	if e := os.MkdirAll(p.dir, 0700); e != nil {
		return errors.New("private NetBird settings unavailable")
	}
	if e := os.Chmod(p.dir, 0700); e != nil {
		return errors.New("private NetBird settings unavailable")
	}
	s.Settings.Token = ""
	raw, _ := json.Marshal(s)
	if e := connector.WritePrivate(filepath.Join(p.dir, "settings.json"), raw); e != nil {
		return errors.New("private NetBird settings unavailable")
	}
	p.saved = s
	return nil
}
func (p *NetBird) open(ctx context.Context, key string) error {
	if _, e := p.openDaemon(ctx); e != nil {
		return e
	}
	return p.login(ctx, key)
}

// openDaemon starts the daemon process only; login brings it up.
func (p *NetBird) openDaemon(ctx context.Context) (NetBirdSession, error) {
	if p.driver == nil {
		return nil, ErrUnavailable
	}
	p.failure = nil
	p.attempt++
	if p.HTTPPort != nil {
		if port, e := p.HTTPPort(ctx); e == nil {
			p.healthPort = port
		}
	}
	p.started = time.Now()
	p.connectedAt = time.Time{}
	p.wasReady = false
	p.verifiedFQDN = ""
	s, e := p.driver.Open(ctx)
	if errors.Is(e, connector.ErrBinaryRequired) {
		return nil, e
	}
	if e != nil {
		connector.Logf("netbird: daemon not started: %v", e)
		p.failed = true
		p.failure = ErrNetBirdStart
		return nil, ErrNetBirdStart
	}
	p.session = s
	p.failed = false
	if configurable, ok := s.(interface{ ConfigureLogin([]string) }); ok {
		configurable.ConfigureLogin(p.saved.Settings.LoginOrigins)
	}
	return s, nil
}
func (p *NetBird) login(ctx context.Context, key string) error {
	if e := p.session.Login(ctx, p.saved.Settings.ManagementURL, key); e != nil {
		connector.Logf("netbird: start failed (attempt %d): %v", p.attempt, e)
		p.failed = true
		if connector.ErrorCode(e) == "" {
			e = ErrNetBirdStart
		}
		p.failure = e
		_ = p.stop()
		return e
	}
	return nil
}

// restoreSession starts the saved identity's daemon through the shared
// supervise.Restorer: a daemon that may take minutes to answer, as on a
// swapping NAS at boot, keeps starting in the background while status says
// connecting, until it answers or the connection is stopped or disabled. A
// restore never asks for a new sign-in.
func (p *NetBird) restoreSession(ctx context.Context) error {
	p.restored = true
	r := p.restorer()
	if e := r.Begin(ctx, &netbirdDaemon{p: p}); e != nil {
		return e
	}
	if r.Active() {
		p.failed, p.failure = false, nil
	}
	return nil
}

func (p *NetBird) restorer() *supervise.Restorer {
	r := &p.restoring
	r.Name, r.Lock, r.First = "netbird", &p.mu, p.RetryDelay
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

// netbirdDaemon is NetBird's part of a restore; it runs under p.mu except
// WaitReady.
type netbirdDaemon struct {
	p       *NetBird
	session NetBirdSession
}

func (d *netbirdDaemon) Open(ctx context.Context) (e error) {
	d.session, e = d.p.openDaemon(ctx)
	return e
}
func (d *netbirdDaemon) Waits() bool {
	_, ok := d.session.(readyWaiter)
	return ok
}
func (d *netbirdDaemon) WaitReady(ctx context.Context) error {
	return d.session.(readyWaiter).WaitReady(ctx)
}
func (d *netbirdDaemon) Alive() bool {
	waiter, ok := d.session.(readyWaiter)
	return ok && d.p.session == d.session && waiter.Alive()
}
func (d *netbirdDaemon) SignIn(ctx context.Context) error {
	if d.p.session != d.session {
		return ErrNetBirdStart
	}
	// NetBird reconnects a registered identity by itself when its daemon
	// starts. netbird login over that connection cancels it and, on a NAS,
	// parked the daemon on a new SSO sign-in.
	if r, ok := d.session.(interface{ Resumes(context.Context) bool }); ok && r.Resumes(ctx) {
		return nil
	}
	return d.p.login(ctx, "")
}
func (d *netbirdDaemon) Close() error { return d.p.stop() }

// cancelRestore ends a background restore; the caller holds p.mu and stops
// the session itself.
func (p *NetBird) cancelRestore() { p.restoring.Cancel() }

// ReuseAttempt joins a repeated SSO click with identical trusted settings.
func (p *NetBird) ReuseAttempt(settings Settings) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	management, e := netbirdManagementURL(settings.ManagementURL)
	if e != nil || settings.Token != "" || !p.saved.Enabled || p.session == nil || p.failed || p.saved.Settings.ManagementURL != management || !reflect.DeepEqual(settings.LoginOrigins, p.saved.Settings.LoginOrigins) {
		return false
	}
	s := p.status()
	return s.State == "needs-login" || s.State == "connecting"
}
func (p *NetBird) Start(ctx context.Context, s Settings) (connector.Status, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e := p.Validate(s); e != nil {
		return p.status(), e
	}
	if e := p.Preflight(ctx); e != nil {
		return p.status(), e
	}
	p.cancelRestore()
	if e := p.stop(); e != nil {
		return p.status(), e
	}
	p.restored = false
	p.started = time.Now()
	p.connectedAt = time.Time{}
	p.wasReady = false
	key := s.Token
	s.ManagementURL, _ = netbirdManagementURL(s.ManagementURL)
	if e := p.selectIdentity(s.ManagementURL); e != nil {
		return p.status(), e
	}
	if e := p.persist(netbirdSaved{Enabled: true, Settings: s}); e != nil {
		return p.status(), e
	}
	if _, e := p.openDaemon(ctx); e != nil {
		return p.status(), e
	}
	// A kept identity reconnects by itself; netbird login over it would ask
	// for a new sign-in. A setup key or an identity that does not come up signs in.
	resumed := false
	if r, ok := p.session.(interface{ Resumes(context.Context) bool }); ok && key == "" && p.hasIdentity() {
		resumed = r.Resumes(ctx)
	}
	if !resumed {
		if e := p.login(ctx, key); e != nil {
			return p.status(), e
		}
	}
	if _, ok := p.session.(readyWaiter); ok {
		r := p.restorer()
		r.Adopt(&netbirdDaemon{p: p, session: p.session})
	}
	return p.status(), nil
}
func (p *NetBird) Restore(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	raw, e := connector.ReadPrivate(filepath.Join(p.dir, "settings.json"))
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return ErrUnavailable
	}
	var saved netbirdSaved
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var extra any
	if d.Decode(&saved) != nil || d.Decode(&extra) != io.EOF || saved.Settings.Token != "" {
		p.failed = true
		return ErrUnavailable
	}
	p.saved = saved
	if !saved.Enabled {
		return nil
	}
	if e = p.Validate(saved.Settings); e != nil {
		p.failed = true
		return e
	}
	p.cancelRestore()
	if e = p.stop(); e != nil {
		return e
	}
	return p.restoreSession(ctx)
}
func (p *NetBird) stop() error {
	if p.session != nil {
		if e := retryStop("netbird", p.session.Stop); e != nil {
			return ErrNetBirdStop
		}
		p.session = nil
		p.attempt++
	}
	return nil
}
func (p *NetBird) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cancelRestore()
	return p.stop()
}
func (p *NetBird) Disable() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	cancelled := p.saved.Enabled && (p.session != nil || p.restoring.Active()) && !p.wasReady
	p.cancelRestore()
	s := p.saved
	s.Enabled = false
	if e := p.persist(s); e != nil {
		return e
	}
	if e := p.stop(); e != nil {
		return e
	}
	if cancelled {
		p.failure = nbError("cancelled")
	}
	// Off keeps the peer identity, like Tailscale: turning on again resumes the
	// same peer instead of registering a new one. Forget deletes it.
	return nil
}

// Forget deletes this NAS's NetBird identities, the active one and those kept
// for other management servers, while the connection is off. The peers stay
// in their management servers until removed there.
func (p *NetBird) Forget() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.saved.Enabled || p.session != nil {
		return ErrConnectionEnabled
	}
	for _, name := range []string{"client", "identities", "identity.json"} {
		if e := os.RemoveAll(filepath.Join(p.dir, name)); e != nil {
			return errors.New("NetBird identity cleanup required")
		}
	}
	return nil
}
func (p *NetBird) Status() connector.Status { p.mu.Lock(); defer p.mu.Unlock(); return p.status() }
func (p *NetBird) status() connector.Status {
	s := connector.Status{Provider: "netbird", State: "disconnected", KnownLimitation: "netbird_management_policy", ManagementURL: p.saved.Settings.ManagementURL, LoginOrigins: p.saved.Settings.LoginOrigins, AttemptID: p.attempt, StartedAt: p.started}
	if !p.saved.Enabled {
		if connector.ErrorCode(p.failure) == "cancelled" {
			s.Phase = "cancelled"
			s.Code = "cancelled"
		}
		if p.session != nil {
			s.State = "stopping"
		}
		// The client saves its key before it registers; only a peer that
		// connected is a peer of its management server.
		s.KeptIdentity = s.State == "disconnected" && p.hasIdentity() && registered(filepath.Join(p.dir, "client"))
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
	observed, e := p.session.Inspect(ctx)
	if e != nil {
		// A swapping NAS can stall reads for minutes. Only a daemon that
		// exited fails; a read that did not answer is unknown, and NetBird
		// keeps running with its sign-in.
		if !errors.Is(e, ErrDaemonExited) {
			s.State = "unknown"
			s.Phase = "degraded"
			return s
		}
		connector.Logf("netbird: daemon exited (attempt %d)", p.attempt)
		p.failed = true
		p.failure = nbError("control_unreachable")
		s.Code = "control_unreachable"
		_ = p.stop()
		return s
	}
	s.State = "connecting"
	s.Phase = "connecting"
	s.Deadline = observed.Deadline
	if observed.Code == "login_origin_required" && netbirdOrigin(observed.LoginOrigin) && !strings.Contains(observed.LoginOrigin, "netbird.io") {
		s.State = "needs-login"
		s.Phase = "trust-login-origin"
		s.Code = observed.Code
		s.LoginOrigin = observed.LoginOrigin
		return s
	}
	if observed.Code != "" {
		s.State = "failed"
		s.Phase = "failed"
		s.Code = observed.Code
		p.failure = nbError(observed.Code)
		p.failed = true
		_ = p.stop()
		return s
	}
	switch observed.DaemonStatus {
	case "NeedsLogin":
		s.Phase = "obtaining-login-url"
		if observed.LoginURL != "" {
			u, code, err := netbirdLoginURL(observed.LoginURL, observed.UserCode, netbirdLoginOrigins(p.saved.Settings.ManagementURL, p.saved.Settings.LoginOrigins))
			if connector.ErrorCode(err) == "login_origin_required" {
				s.State = "needs-login"
				s.Phase = "trust-login-origin"
				s.Code = "login_origin_required"
				s.LoginOrigin = u
				return s
			}
			if err != nil {
				s.State = "failed"
				s.Phase = "failed"
				return p.failStatus(s, connector.ErrorCode(err))
			}
			s.State = "needs-login"
			s.Phase = "awaiting-login"
			s.LoginURL = u
			s.UserCode = code
		}
		return s
	case "SessionExpired":
		s.State = "needs-login"
		s.Phase = "obtaining-login-url"
		return s
	case "LoginFailed":
		s.State = "failed"
		s.Phase = "failed"
		return p.failStatus(s, "login_denied")
	case "Idle", "Connecting":
		if p.connectedAt.IsZero() {
			p.connectedAt = time.Now()
		}
		if !p.restored && !p.wasReady && time.Since(p.connectedAt) > netbirdConnectWindow {
			s.State = "failed"
			s.Phase = "timed-out"
			return p.failStatus(s, "network_connect_timeout")
		}
		return s
	case "Connected":
	default:
		s.State = "failed"
		s.Phase = "failed"
		return p.failStatus(s, "provider_status_invalid")
	}
	if !observed.ManagementConnected || !observed.SignalConnected {
		if p.wasReady {
			s.State = "unknown"
		}
		s.Phase = "degraded"
		return s
	}
	ip, e := netip.ParsePrefix(observed.IP)
	if e != nil || !ip.Addr().Is4() || ip.Addr().IsLoopback() || ip.Addr().IsUnspecified() || ip.Addr().IsMulticast() {
		s.State = "failed"
		s.Phase = "failed"
		return p.failStatus(s, "provider_status_invalid")
	}
	cloud := p.saved.Settings.ManagementURL == "https://api.netbird.io:443" || p.saved.Settings.ManagementURL == "https://api.netbird.io"
	if cloud && !netip.MustParsePrefix("100.64.0.0/10").Contains(ip.Addr()) {
		s.State = "failed"
		s.Phase = "failed"
		return p.failStatus(s, "provider_status_invalid")
	}
	s.State = "connected"
	s.Phase = "ready"
	s.NetworkReady = true
	s.LoggedIn = true
	markRegistered(filepath.Join(p.dir, "client"))
	s.NetBirdIP = ip.Addr().String()
	s.PeersOnline = observed.PeersOnline
	s.Deadline = time.Time{}
	p.wasReady = true
	if nbHost.MatchString(observed.FQDN) && strings.Contains(observed.FQDN, ".") && net.ParseIP(observed.FQDN) == nil {
		s.NetBirdFQDN = observed.FQDN
	}
	if p.HTTPPort != nil {
		port, err := p.HTTPPort(ctx)
		if err == nil && port > 0 && port <= 65535 {
			s.Endpoints = append(s.Endpoints, Endpoint{URL: "http://" + net.JoinHostPort(s.NetBirdIP, strconv.Itoa(port)) + "/", LocalPort: port})
			if s.NetBirdFQDN != "" && p.verifiedFQDN == s.NetBirdFQDN && p.verifiedIP == s.NetBirdIP && p.verifiedPort == port {
				s.Endpoints = append(s.Endpoints, Endpoint{URL: "http://" + net.JoinHostPort(s.NetBirdFQDN, strconv.Itoa(port)) + "/", LocalPort: port})
			}
		}
	}
	return s
}

func (p *NetBird) failStatus(s connector.Status, code string) connector.Status {
	p.restoring.Refuse(code)
	p.failed = true
	p.failure = nbError(code)
	s.Code = code
	_ = p.stop()
	return s
}

func (d *netbirdDaemon) LastExit() supervision.Exit {
	if s, ok := d.session.(interface{ LastExit() supervision.Exit }); ok {
		return s.LastExit()
	}
	return supervision.Exit{Code: -1, Reason: "daemon_exit"}
}

func (p *NetBird) RecoveryTerminal() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.restoring.Snapshot()
	return s.State == supervision.Failed || (s.State == supervision.Stopped && p.restoring.Policy() != nil)
}
