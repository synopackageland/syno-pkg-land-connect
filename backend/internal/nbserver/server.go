// Package nbserver runs a self-hosted NetBird server on the NAS: the official
// netbird-server executable as the package's own user, reached through the
// user's Cloudflare own domain. One setup job takes it from nothing to this
// NAS joined, step by step; the server then restarts with Connect.
package nbserver

import (
	"context"
	"errors"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"dsmapp/internal/cloudflare"
	"dsmapp/internal/connector"
	"dsmapp/internal/dependency"
	"dsmapp/internal/supervise"
	"github.com/synopackageland/syno-pkg-land-sdk/supervision"

	"github.com/synopackageland/syno-pkg-land-sdk/ipc"
)

var (
	errBusy          = connector.Coded("netbird_server_busy", "the NetBird server is changing; try again when it finishes")
	errBinary        = connector.Coded("netbird_server_binary_failed", "the official NetBird server could not be downloaded or verified")
	errPortInUse     = connector.Coded("netbird_server_port_in_use", "another program uses the NetBird server's port")
	errStart         = connector.Coded("netbird_server_start_failed", "the NetBird server stopped while starting")
	errNotReady      = connector.Coded("netbird_server_not_ready", "the NetBird server did not answer yet")
	errOwnerExists   = connector.Coded("netbird_server_owner_exists", "the server already has an owner Syno Connect Center did not make")
	errTokenExpired  = connector.Coded("netbird_server_token_expired", "the server's management token expired while the server was off")
	errAccount       = connector.Coded("netbird_server_account_failed", "the server's owner could not be made")
	errSetupKey      = connector.Coded("netbird_server_setup_key_failed", "the server did not make a setup key")
	errJoin          = connector.Coded("netbird_server_join_failed", "this NAS could not join the server")
	errLeave         = connector.Coded("netbird_server_leave_failed", "the server is off, but this NAS's NetBird did not disconnect from it")
	errSave          = connector.Coded("netbird_server_save_failed", "the server settings could not be saved")
	errDisabled      = connector.Coded("netbird_server_disabled", "the NetBird server was turned off")
	errNotConfigured = connector.Coded("netbird_server_off", "the NetBird server is off")
)

// Setup steps, in order.
const (
	StepPrerequisites = "prerequisites"
	StepBinary        = "binary"
	StepConfig        = "config"
	StepRoute         = "route"
	StepBridge        = "bridge"
	StepStart         = "start"
	StepAccount       = "account"
	StepSetupKey      = "setupkey"
	StepCheck         = "check"
	StepJoin          = "join"
)

var stepOrder = []string{StepPrerequisites, StepBinary, StepConfig, StepRoute, StepBridge, StepStart, StepAccount, StepSetupKey, StepCheck, StepJoin}

// Deps are the parts of Connect the server uses. Each is owned elsewhere.
type Deps struct {
	Preflight    func(context.Context) error
	Dependencies func(context.Context, string, bool) dependency.Report
	Dir          string
	// Install downloads, verifies and selects the official server.
	Install func(context.Context) error
	// Binary resolves the selected, verified server.
	Binary func(context.Context) (path, version string, e error)
	// Zone is the Cloudflare own domain, or why there is none.
	Zone func() (string, error)
	// Name proposes a free name under Zone; Route points it at the tunnel
	// and serves it; Unroute withdraws it and deletes its record.
	Name    func(context.Context) (string, error)
	Route   func(context.Context, string) error
	Unroute func(context.Context, string) error
	// Ready tells, without changing anything, whether the Cloudflare token
	// can deploy that Worker on zone; only a refusal the user must fix by
	// changing the token comes back.
	Ready func(ctx context.Context, zone string) error
	// Bridge deploys the Worker that carries Signal's stream for the name
	// on zone, or brings it up to date; Unbridge removes it.
	Bridge   func(ctx context.Context, zone, host string) error
	Unbridge func(context.Context) error
	// Check proves the public address carries NetBird's gRPC.
	Check func(context.Context, string) error
	// Join switches this NAS's NetBird to the server with a one-off key.
	// Member reports whether it already uses that server; Leave disconnects
	// it when it does.
	Join   func(ctx context.Context, managementURL, setupKey string) error
	Member func(managementURL string) bool
	Leave  func(ctx context.Context, managementURL string) error

	Launcher Launcher
	// Port is the management port; tests use a free one.
	Port int
	// StartWindow bounds one wait for a new server to answer; the server
	// keeps being supervised after it. CheckWindow bounds the public check;
	// GuideWindow bounds the wait for the zone's gRPC setting to be turned on.
	StartWindow, CheckWindow, GuideWindow, Poll time.Duration
	First                                       time.Duration
}

type Step struct {
	Conflict *connector.NameConflict `json:"conflict,omitempty"`
	ID       string                  `json:"id"`
	State    string                  `json:"state"`
	Code     string                  `json:"code,omitempty"`
}

// Status is everything the page shows. It never carries a secret.
type Status struct {
	Job             *JobStatus            `json:"job,omitempty"`
	ResumeRequired  bool                  `json:"resumeRequired,omitempty"`
	Supervision     *supervision.Snapshot `json:"supervision,omitempty"`
	Dependencies    *dependency.Report    `json:"dependencies,omitempty"`
	State           string                `json:"state"`
	Prerequisite    string                `json:"prerequisite,omitempty"`
	Zone            string                `json:"zone,omitempty"`
	Steps           []Step                `json:"steps,omitempty"`
	Code            string                `json:"code,omitempty"`
	Hostname        string                `json:"hostname,omitempty"`
	ManagementURL   string                `json:"managementUrl,omitempty"`
	AdminEmail      string                `json:"adminEmail,omitempty"`
	Joined          bool                  `json:"joined,omitempty"`
	Version         string                `json:"version,omitempty"`
	Peers           *int                  `json:"peers,omitempty"`
	PeersOnline     *int                  `json:"peersOnline,omitempty"`
	UptimeSeconds   int64                 `json:"uptimeSeconds,omitempty"`
	MemoryBytes     int64                 `json:"memoryBytes,omitempty"`
	DataBytes       int64                 `json:"dataBytes,omitempty"`
	CleanupHostname string                `json:"cleanupHostname,omitempty"`
}

type Server struct {
	d Deps
	p paths

	// mu owns the process, the files and supervision.
	mu        sync.Mutex
	proc      Process
	version   string
	started   time.Time
	healthy   bool
	exits     int
	restoring supervise.Restorer
	watching  context.CancelFunc
	watchDone chan struct{}
	attempt   uint64
	renewAt   time.Time
	bridgeAt  time.Time
	// bridgeZone: the last bridge refresh had no Workers token; the first
	// status read that finds one again refreshes at once.
	bridgeZone bool

	// origin is read by Cloudflare while it holds its own locks, so it is
	// never behind mu.
	origin atomic.Pointer[cloudflare.ServerOrigin]

	// smu owns the setup job and its progress.
	smu        sync.Mutex
	jobCancel  context.CancelFunc
	jobDone    chan struct{}
	steps      []Step
	code       string
	jobStarted time.Time
}

// JobStatus describes live work, never a persisted lock or an expiring lease.
type JobStatus struct {
	Step      string    `json:"step"`
	StartedAt time.Time `json:"startedAt"`
}

func New(d Deps) *Server {
	if d.Launcher == nil {
		d.Launcher = ExecLauncher{}
	}
	if d.Port == 0 {
		d.Port = ManagementPort
	}
	s := &Server{d: d, p: paths{d.Dir}}
	s.restoring.Name, s.restoring.Lock, s.restoring.First = "netbird-server", &s.mu, d.First
	s.restoring.Monitor = true
	s.restoring.Transition = func(snapshot supervision.Snapshot) {
		if snapshot.State == supervision.Failed {
			s.healthy = false
			if s.watching != nil {
				s.watching()
			}
		}
	}
	s.restoring.Attempt = func() uint64 { return s.attempt }
	return s
}

func (s *Server) window(d, fallback time.Duration) time.Duration {
	if d > 0 {
		return d
	}
	return fallback
}

func (s *Server) loadState() state {
	var st state
	_ = readJSON(s.p.state(), &st)
	return st
}
func (s *Server) saveState(st state) error {
	if os.MkdirAll(s.p.dir, 0700) != nil || os.Chmod(s.p.dir, 0700) != nil || writeJSON(s.p.state(), st) != nil {
		return errSave
	}
	s.publish(st)
	return nil
}

// publish exposes the origin Cloudflare serves while the server is enabled.
func (s *Server) publish(st state) {
	if !st.Enabled || st.Hostname == "" {
		s.origin.Store(nil)
		return
	}
	s.origin.Store(&cloudflare.ServerOrigin{Hostname: st.Hostname, Port: s.d.Port, CAFile: s.p.cert()})
}

// Origin is the Cloudflare ingress target, only while the server is enabled.
func (s *Server) Origin() (cloudflare.ServerOrigin, bool) {
	o := s.origin.Load()
	if o == nil {
		return cloudflare.ServerOrigin{}, false
	}
	return *o, true
}

func managementURL(host string) string { return "https://" + host }

func (s *Server) set(id, state, code string) {
	s.smu.Lock()
	defer s.smu.Unlock()
	for i := range s.steps {
		if s.steps[i].ID == id {
			s.steps[i].State, s.steps[i].Code = state, code
			if state != "failed" {
				s.steps[i].Conflict = nil
			}
		}
	}
	if state == "failed" {
		s.code = code
	}
}

func code(e error) string {
	if c := connector.ErrorCode(e); c != "" {
		return c
	}
	return "netbird_server_failed"
}

// Setup starts the one-button job, or continues one that failed: every step
// first checks whether it is already done. It returns once the job runs.
func (s *Server) Setup() error { return s.SetupWithOptions(false) }

func (s *Server) SetupWithOptions(another bool) error {
	// Repeated clicks follow the existing setup; they never start a competitor.
	s.smu.Lock()
	active := s.jobCancel != nil
	s.smu.Unlock()
	if active {
		return nil
	}

	s.smu.Lock()
	defer s.smu.Unlock()
	if s.jobCancel != nil {
		select {
		case <-s.jobDone:
		default:
			return nil
		}
	}
	s.steps = make([]Step, len(stepOrder))
	for i, id := range stepOrder {
		s.steps[i] = Step{ID: id, State: "pending"}
	}
	s.code = ""
	ctx, cancel := context.WithCancel(context.Background())
	if another {
		ctx = connector.WithAnotherName(ctx)
	}
	done := make(chan struct{})
	s.jobCancel, s.jobDone = cancel, done
	s.jobStarted = time.Now()
	go func() {
		defer func() {
			if recover() != nil {
				s.smu.Lock()
				for i := range s.steps {
					if s.steps[i].State == "running" {
						s.steps[i].State, s.steps[i].Code = "failed", "netbird_server_failed"
					}
				}
				s.code = "netbird_server_failed"
				s.smu.Unlock()
			}
			cancel()
			s.smu.Lock()
			if s.jobDone == done {
				s.jobCancel = nil
			}
			close(done)
			s.smu.Unlock()
		}()
		s.job(ctx)
	}()
	return nil
}

func (s *Server) stopJob() {
	s.smu.Lock()
	cancel, done := s.jobCancel, s.jobDone
	s.smu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

func (s *Server) job(ctx context.Context) {
	var host, key, zone string
	var k secrets
	run := func(id string, f func() error) bool {
		s.set(id, "running", "")
		if e := f(); e != nil {
			if ctx.Err() != nil {
				e = errDisabled
			}
			s.failStep(id, e)
			return false
		}
		s.set(id, "done", "")
		return true
	}
	member := false
	steps := []struct {
		id string
		f  func() error
	}{
		{StepPrerequisites, func() error {
			if s.d.Preflight != nil {
				if e := s.d.Preflight(ctx); e != nil {
					return e
				}
			}
			var e error
			zone, e = s.d.Zone()
			if e == nil && s.d.Ready != nil {
				e = s.d.Ready(ctx, zone)
			}
			return e
		}},
		{StepBinary, func() error {
			if e := s.d.Install(ctx); e != nil {
				return errBinary
			}
			_, _, e := s.d.Binary(ctx)
			if e != nil {
				return errBinary
			}
			return nil
		}},
		{StepConfig, func() (e error) { host, k, e = s.configure(ctx); return e }},
		{StepRoute, func() error {
			if e := s.d.Route(ctx, host); e != nil {
				if connector.ErrorCode(e) == "cloudflare_name_taken" {
					// Taken since it was chosen: the next setup chooses again.
					s.mu.Lock()
					st := s.loadState()
					if !st.Routed {
						st.Hostname = ""
						_ = s.saveState(st)
					}
					s.mu.Unlock()
				}
				return e
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			st := s.loadState()
			st.Routed = true
			return s.saveState(st)
		}},
		{StepBridge, func() error {
			// RouteServer has just restarted the tunnel to publish our ingress.
			// Reuse the verified zone; public readiness is checked at StepCheck.
			s.mu.Lock()
			st := s.loadState()
			st.Bridged = true
			e := s.saveState(st)
			s.bridgeAt = time.Now().Add(bridgeEvery)
			s.mu.Unlock()
			if e != nil {
				return e
			}
			return s.d.Bridge(ctx, zone, host)
		}},
		{StepStart, func() error { return s.start(ctx) }},
		{StepAccount, func() (e error) { k, e = s.account(ctx, host, k); return e }},
		{StepSetupKey, func() (e error) {
			if member = s.d.Member != nil && s.d.Member(managementURL(host)); member {
				return nil
			}
			api, e := newManagementAPI(s.p, host, s.d.Port)
			if e == nil {
				key, e = api.setupKey(ctx, k.Token)
			}
			if e != nil {
				return errSetupKey
			}
			return nil
		}},
		{StepCheck, func() error { return s.check(ctx, managementURL(host)) }},
		{StepJoin, func() error {
			if !member {
				if e := s.d.Join(ctx, managementURL(host), key); e != nil {
					if connector.ErrorCode(e) != "" {
						return e
					}
					return errJoin
				}
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			st := s.loadState()
			st.Joined = true
			return s.saveState(st)
		}},
	}
	for _, step := range steps {
		if !run(step.id, step.f) {
			return
		}
	}
}

// configure writes everything the server needs and records the intent, so
// Cloudflare serves the name and a restart restores the server.
func (s *Server) configure(ctx context.Context) (string, secrets, error) {
	s.mu.Lock()
	st := s.loadState()
	s.mu.Unlock()
	if st.Hostname == "" {
		host, e := s.d.Name(ctx)
		if e != nil {
			return "", secrets{}, e
		}
		st.Hostname = host
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var k secrets
	if e := readJSON(s.p.secrets(), &k); e != nil {
		if !errors.Is(e, os.ErrNotExist) {
			return "", k, errSave
		}
		if k, e = newSecrets(st.Hostname); e != nil {
			return "", k, errSave
		}
	}
	if st.Metrics == 0 || st.Health == 0 {
		st.Metrics, st.Health = freePort(), freePort()
		if st.Metrics == 0 || st.Health == 0 {
			return "", k, errPortInUse
		}
	}
	if s.proc == nil || !s.proc.Alive() {
		if !portFree(s.d.Port) {
			return "", k, errPortInUse
		}
	}
	if os.MkdirAll(s.p.dir, 0700) != nil || os.Chmod(s.p.dir, 0700) != nil || writeJSON(s.p.secrets(), k) != nil || ensureCertificate(s.p, st.Hostname) != nil {
		return "", k, errSave
	}
	if e := s.writeConfig(st, k); e != nil {
		return "", k, e
	}
	st.Enabled = true
	return st.Hostname, k, s.saveState(st)
}

func (s *Server) writeConfig(st state, k secrets) error {
	raw, e := renderConfig(s.p, st, k)
	if e != nil {
		return errSave
	}
	if os.MkdirAll(s.p.data(), 0700) != nil || connector.WritePrivate(s.p.config(), raw) != nil {
		return errSave
	}
	return nil
}

func freePort() int {
	l, e := ipc.ReserveLoopback(context.Background(), 0)
	if e != nil {
		return 0
	}
	defer l.Release()
	return int(l.Port())
}
func portFree(port int) bool {
	if port < 1 || port > 65535 {
		return false
	}
	return ipc.ProbeLoopback(context.Background(), uint16(port)) == nil
}

// start brings the server up under supervision and waits until it answers.
// A server that keeps exiting fails the step, while supervision keeps trying.
func (s *Server) start(ctx context.Context) error {
	s.mu.Lock()
	if !s.restoring.Active() && (s.proc == nil || !s.proc.Alive()) {
		s.exits = 0
		if e := s.beginLocked(); e != nil {
			s.mu.Unlock()
			return e
		}
	}
	s.mu.Unlock()
	deadline := time.Now().Add(s.window(s.d.StartWindow, 10*time.Minute))
	poll := s.window(s.d.Poll, time.Second)
	for {
		s.mu.Lock()
		healthy, exits := s.healthy, s.exits
		s.mu.Unlock()
		switch {
		case healthy:
			return nil
		case exits >= 2:
			return errStart
		case time.Now().After(deadline):
			return errNotReady
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(poll):
		}
	}
}

// beginLocked starts supervision: the restore path every provider shares,
// then a watch that restores again if the server exits. mu is held.
func (s *Server) beginLocked() error {
	s.attempt++
	s.healthy = false
	if s.watching == nil {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		s.watching, s.watchDone = cancel, done
		go s.watch(ctx, done)
	}
	return s.restoring.Begin(context.Background(), &daemon{s: s})
}

func (s *Server) watch(ctx context.Context, done chan struct{}) {
	defer close(done)
	tick := time.NewTicker(s.window(s.d.Poll, 5*time.Second))
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		s.mu.Lock()
		if s.restoring.Snapshot().State == supervision.Failed {
			s.healthy = false
			s.mu.Unlock()
			return
		}
		renew := ctx.Err() == nil && s.healthy && !time.Now().Before(s.renewAt)
		bridge := ctx.Err() == nil && s.healthy && !time.Now().Before(s.bridgeAt)
		s.mu.Unlock()
		if renew {
			next := s.renewToken(ctx)
			s.mu.Lock()
			s.renewAt = next
			s.mu.Unlock()
		}
		if bridge {
			next := s.refreshBridge(ctx)
			s.mu.Lock()
			s.bridgeAt = next
			s.mu.Unlock()
		}
	}
}

const (
	renewBefore = 30 * 24 * time.Hour
	renewRetry  = time.Hour
	bridgeEvery = 24 * time.Hour
	bridgeRetry = 10 * time.Minute
)

// refreshBridge runs in the watch: first after Connect starts, so an update
// that brings a new bridge script replaces the deployed one, then daily, so
// a route removed in Cloudflare comes back. It returns when to look again.
func (s *Server) refreshBridge(ctx context.Context) time.Time {
	s.mu.Lock()
	st := s.loadState()
	s.mu.Unlock()
	if !st.Enabled || !st.Bridged || st.Hostname == "" || s.d.Bridge == nil {
		return time.Now().Add(bridgeEvery)
	}
	zone, e := s.d.Zone()
	s.mu.Lock()
	s.bridgeZone = e != nil
	s.mu.Unlock()
	if e == nil {
		e = s.d.Bridge(ctx, zone, st.Hostname)
	}
	if e != nil {
		connector.Logf("netbird-server: Signal bridge not updated: %s", code(e))
		return time.Now().Add(bridgeRetry)
	}
	return time.Now().Add(bridgeEvery)
}

// renewToken replaces the owner's API token a month before it expires, so
// Connect keeps reading the server. It runs in the watch, so only while the
// server runs, and returns when to look again. The old token is deleted
// only once the new one is saved.
func (s *Server) renewToken(ctx context.Context) time.Time {
	retry := time.Now().Add(renewRetry)
	s.mu.Lock()
	host := s.loadState().Hostname
	var k secrets
	e := readJSON(s.p.secrets(), &k)
	s.mu.Unlock()
	if e != nil || k.Token == "" || k.UserID == "" {
		return retry
	}
	if !k.TokenExpires.IsZero() && time.Until(k.TokenExpires) > renewBefore {
		return k.TokenExpires.Add(-renewBefore)
	}
	api, e := newManagementAPI(s.p, host, s.d.Port)
	if e != nil {
		return retry
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	plain, t, e := api.newToken(bounded, k.Token, k.UserID)
	if e != nil {
		connector.Logf("netbird-server: API token not renewed: %v", e)
		return retry
	}
	old := k.TokenID
	k.Token, k.TokenID, k.TokenExpires = plain, t.ID, t.Expires
	s.mu.Lock()
	if ctx.Err() == nil {
		e = writeJSON(s.p.secrets(), k)
	} else {
		e = ctx.Err()
	}
	s.mu.Unlock()
	if e != nil {
		_ = api.deleteToken(bounded, plain, k.UserID, t.ID)
		return retry
	}
	if old != "" && old != t.ID {
		if e = api.deleteToken(bounded, plain, k.UserID, old); e != nil {
			connector.Logf("netbird-server: old API token not deleted: %v", e)
		}
	}
	return t.Expires.Add(-renewBefore)
}

// stopLocked ends supervision and the server. mu is held.
func (s *Server) stopLocked() {
	// The restore loop checks its run under mu before acting, so it needs
	// no wait here; waiting under mu could block it forever.
	s.restoring.Cancel()
	if s.watching != nil {
		s.watching()
		s.mu.Unlock()
		<-s.watchDone
		s.mu.Lock()
		s.watching = nil
	}
	if s.proc != nil {
		_ = s.proc.Stop()
		s.proc = nil
	}
	s.healthy = false
}

// daemon is the server's part of the shared tolerant supervision.
type daemon struct {
	s    *Server
	proc Process
}

func (d *daemon) Open(ctx context.Context) error {
	s := d.s
	path, version, e := s.d.Binary(ctx)
	if e != nil {
		return connector.ErrBinaryRequired
	}
	st := s.loadState()
	var k secrets
	if !st.Enabled || readJSON(s.p.secrets(), &k) != nil {
		return errNotConfigured
	}
	if e = s.writeConfig(st, k); e != nil {
		return connector.Coded("invalid_config", "server configuration unavailable")
	}
	p, e := s.d.Launcher.Start(ctx, Command{Path: path, Args: []string{"--config", s.p.config()}, Env: serverEnv(s.p.dir), Dir: s.p.dir})
	if e != nil {
		return errStart
	}
	d.proc, s.proc, s.version, s.started = p, p, version, time.Now()
	return nil
}
func (*daemon) Waits() bool { return true }
func (d *daemon) WaitReady(ctx context.Context) error {
	host := d.s.loadState().Hostname
	return supervise.Readiness{
		Window: d.s.window(d.s.d.StartWindow, 10*time.Minute), Probe: 10 * time.Second, Pause: d.s.d.Poll,
		Alive: d.Alive, Read: func(ctx context.Context) error { return d.s.answering(ctx, host) },
	}.Wait(ctx)
}

// LastExit preserves the owned child's actual outcome for the shared policy.
func (d *daemon) LastExit() supervision.Exit {
	if p, ok := d.proc.(interface{ Exit() supervision.Exit }); ok {
		return p.Exit()
	}
	return supervision.Exit{Code: -1, Reason: "daemon_exit"}
}
func (d *daemon) Alive() bool { return d.proc != nil && d.proc.Alive() }
func (d *daemon) SignIn(context.Context) error {
	d.s.healthy = true
	return nil
}
func (d *daemon) Close() error {
	if d.proc != nil {
		if !d.proc.Alive() {
			d.s.exits++
		}
		if err := d.proc.Stop(); err != nil {
			return err
		}
		if d.s.proc == d.proc {
			d.s.proc = nil
		}
		d.proc = nil
	}
	return nil
}

// answering is ready when the management API answers on loopback over the
// server's own TLS. The server's /health is not used: in the combined
// server it counts only standalone relay listeners, so it always reads
// unhealthy.
func (s *Server) answering(ctx context.Context, host string) error {
	api, e := newManagementAPI(s.p, host, s.d.Port)
	if e != nil {
		return e
	}
	if _, e = api.setupRequired(ctx); e != nil {
		return errNotReady
	}
	return nil
}

// account makes the server's owner once and keeps the owner's API token.
func (s *Server) account(ctx context.Context, host string, k secrets) (secrets, error) {
	if k.Token != "" {
		// The token renews only while the server runs; an expired one cannot
		// renew itself, so say so instead of failing at the setup key.
		if !k.TokenExpires.IsZero() && !time.Now().Before(k.TokenExpires) {
			return k, errTokenExpired
		}
		return k, nil
	}
	api, e := newManagementAPI(s.p, host, s.d.Port)
	if e != nil {
		return k, errAccount
	}
	required, e := api.setupRequired(ctx)
	if e != nil {
		return k, errAccount
	}
	if !required {
		return k, errOwnerExists
	}
	user, token, e := api.setup(ctx, k.AdminEmail, k.AdminPassword)
	if e != nil {
		return k, errAccount
	}
	k.Token, k.UserID, k.TokenExpires = token, user, time.Now().Add(tokenDays*24*time.Hour)
	// The token's ID lets its renewal delete it; the only token is this one.
	if list, e := api.tokens(ctx, token, user); e == nil && len(list) == 1 {
		k.TokenID, k.TokenExpires = list[0].ID, list[0].Expires
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if writeJSON(s.p.secrets(), k) != nil {
		return k, errSave
	}
	return k, nil
}

// check retries while the new name and route settle at Cloudflare. When
// Cloudflare refuses gRPC, the zone's gRPC setting is off: there is no API
// for it, so the step shows how to turn it on and keeps checking, then
// continues by itself.
func (s *Server) check(ctx context.Context, url string) error {
	deadline := time.Now().Add(s.window(s.d.CheckWindow, 3*time.Minute))
	var guided time.Time
	backoff := supervise.Backoff{First: s.window(s.d.Poll, 5*time.Second), Max: 30 * time.Second}
	for {
		e := s.d.Check(ctx, url)
		wait := backoff.Next()
		if connector.ErrorCode(e) == connector.ErrorCode(errGRPCBlocked) {
			if guided.IsZero() {
				guided = time.Now()
				s.set(StepCheck, "running", connector.ErrorCode(errGRPCBlocked))
			}
			if time.Since(guided) < s.window(s.d.GuideWindow, 20*time.Minute) {
				wait = s.window(s.d.Poll, 5*time.Second)
			} else {
				return e
			}
		} else if e == nil || time.Now().After(deadline) {
			return e
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
	}
}

// Restore starts an enabled server when Connect starts. Nothing runs, and
// no goroutine or timer exists, while the server is off.
func (s *Server) Restore(context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.loadState()
	s.publish(st)
	if !st.Enabled {
		return nil
	}
	return s.beginLocked()
}

// Close stops the server for Connect's shutdown but keeps the intent.
func (s *Server) Close() {
	s.stopJob()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopLocked()
}

// Disable disconnects this NAS from the server, stops it, withdraws its
// Cloudflare name and Signal bridge, and removes its data when asked. A failed name removal is
// kept for a retry, with the data; a failed disconnect is reported after the
// rest is done.
func (s *Server) Disable(ctx context.Context, removeData bool) error {
	s.stopJob()
	s.mu.Lock()
	st := s.loadState()
	s.mu.Unlock()
	var left error
	if s.d.Leave != nil && st.Hostname != "" {
		if e := s.d.Leave(ctx, managementURL(st.Hostname)); e != nil {
			connector.Logf("netbird-server: this NAS did not leave: %v", e)
			left = errLeave
		}
	}
	s.mu.Lock()
	s.stopLocked()
	st = s.loadState()
	var e error
	if st.Enabled || st.Joined && left == nil {
		st.Enabled = false
		if left == nil {
			st.Joined = false
		}
		e = s.saveState(st)
	}
	s.publish(st)
	s.mu.Unlock()
	if e != nil {
		return e
	}
	// Withdraw the name and the bridge independently: a name that cannot be
	// removed yet must not keep the bridge Worker running in the account.
	var withdrawn error
	if st.Routed && st.Hostname != "" {
		if e = s.d.Unroute(ctx, st.Hostname); e != nil {
			withdrawn = e
		} else {
			s.mu.Lock()
			st.Routed = false
			e = s.saveState(st)
			s.mu.Unlock()
			if e != nil {
				return e
			}
		}
	}
	if st.Bridged && s.d.Unbridge != nil {
		if e = s.d.Unbridge(ctx); e != nil {
			if withdrawn == nil {
				withdrawn = e
			}
		} else {
			s.mu.Lock()
			st.Bridged = false
			e = s.saveState(st)
			s.mu.Unlock()
			if e != nil {
				return e
			}
		}
	}
	if withdrawn != nil {
		return withdrawn
	}
	s.smu.Lock()
	s.steps, s.code = nil, ""
	s.smu.Unlock()
	if removeData {
		s.mu.Lock()
		defer s.mu.Unlock()
		if os.RemoveAll(s.p.dir) != nil {
			return errSave
		}
	}
	return left
}

// UpdateBinary moves a running server to a newly verified executable, and
// back to the previous one if the new one does not answer.
func (s *Server) UpdateBinary(ctx context.Context, prepare func() (func() error, func() error, error)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	apply, revert, e := prepare()
	if e != nil {
		return e
	}
	if e = apply(); e != nil {
		return e
	}
	if !s.loadState().Enabled {
		return nil
	}
	e = s.restartLocked(ctx)
	if e == nil {
		return nil
	}
	if r := revert(); r != nil {
		e = errors.Join(e, r)
	} else if r = s.restartLocked(ctx); r == nil {
		return e
	}
	// Whatever is selected now stays supervised, as after a restart.
	_ = s.beginLocked()
	return e
}

// restartLocked replaces the server and waits for it to answer. mu is held.
func (s *Server) restartLocked(ctx context.Context) error {
	s.stopLocked()
	d := &daemon{s: s}
	if e := d.Open(ctx); e != nil {
		return e
	}
	s.mu.Unlock()
	e := d.WaitReady(ctx)
	s.mu.Lock()
	if e != nil {
		d.Close()
		return errStart
	}
	s.healthy = true
	if s.watching == nil {
		wctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		s.watching, s.watchDone = cancel, done
		go s.watch(wctx, done)
	}
	return nil
}

// Status reads the server's state; a running server also reports its use.
func (s *Server) Status(ctx context.Context) Status {
	var out Status
	if s.d.Dependencies == nil {
		if zone, e := s.d.Zone(); e != nil {
			out.Prerequisite = code(e)
		} else {
			out.Zone = zone
			s.mu.Lock()
			if s.bridgeZone {
				s.bridgeZone, s.bridgeAt = false, time.Time{}
			}
			s.mu.Unlock()
			if s.d.Ready != nil {
				if e = s.d.Ready(ctx, zone); e != nil {
					out.Prerequisite = code(e)
				}
			}
		}
	}
	s.smu.Lock()
	out.Steps = append([]Step(nil), s.steps...)
	out.Code = s.code
	working := s.jobCancel != nil
	if working {
		select {
		case <-s.jobDone:
			working = false
		default:
		}
	}
	if working {
		out.Job = &JobStatus{StartedAt: s.jobStarted}
		for _, step := range s.steps {
			if step.State == "running" {
				out.Job.Step = step.ID
				break
			}
		}
	}
	s.smu.Unlock()
	s.mu.Lock()
	st := s.loadState()
	var k secrets
	_ = readJSON(s.p.secrets(), &k)
	proc, healthy, version, started := s.proc, s.healthy, s.version, s.started
	snapshot := s.restoring.Snapshot()
	if snapshot.State != supervision.Stopped {
		out.Supervision = &snapshot
	}
	s.mu.Unlock()
	if s.d.Dependencies != nil {
		r := s.d.Dependencies(ctx, st.Hostname, st.Enabled)
		out.Dependencies = &r
		if r.UpstreamsReady {
			s.mu.Lock()
			if s.bridgeZone {
				s.bridgeZone = false
				s.bridgeAt = time.Time{}
			}
			s.mu.Unlock()
		}
		out.Zone = r.Zone
		if r.Problem != nil {
			out.Prerequisite = r.Problem.Code
		}
		// A historical prerequisite failure cannot override the current truth.
		if !working && !r.Checking && out.Code != "" && isDependencyFailure(out.Code) && out.Code != out.Prerequisite {
			old := out.Code
			out.Code = out.Prerequisite
			if out.Code == "" {
				out.Code = "netbird_server_setup_incomplete"
			}
			for i := range out.Steps {
				if out.Steps[i].State == "failed" && out.Steps[i].Code == old {
					out.Steps[i].Code = out.Code
				}
			}
		}
	}
	out.ResumeRequired = st.Enabled && !st.Joined && !working
	out.Hostname, out.Joined = st.Hostname, st.Joined
	if st.Hostname != "" && (st.Enabled || st.Routed || st.Bridged) {
		out.ManagementURL = managementURL(st.Hostname)
	}
	if st.Enabled {
		out.AdminEmail = k.AdminEmail
	}
	if !st.Enabled && (st.Routed || st.Bridged) {
		out.CleanupHostname = st.Hostname
	}
	switch {
	case working:
		out.State = "working"
	case out.Code != "":
		out.State = "failed"
	case !st.Enabled:
		out.State = "off"
	case snapshot.State == supervision.Failed:
		out.State = "failed"
		out.Code = snapshot.Reason
	case snapshot.State == supervision.Waiting || snapshot.State == supervision.Backoff:
		out.State = string(snapshot.State)
	case healthy:
		out.State = "running"
	default:
		out.State = "starting"
	}
	if proc == nil || !proc.Alive() || !healthy {
		return out
	}
	out.Version = version
	out.UptimeSeconds = int64(time.Since(started) / time.Second)
	out.MemoryBytes = residentBytes(proc.Pid())
	out.DataBytes = diskBytes(s.p.data())
	if k.Token != "" {
		bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if api, e := newManagementAPI(s.p, st.Hostname, s.d.Port); e == nil {
			if total, online, e := api.peers(bounded, k.Token); e == nil {
				out.Peers, out.PeersOnline = &total, &online
			}
		}
	}
	return out
}

// Enabled reports whether the server is set up to run; the Free link keeps
// the Workers token and Signal bridge while it is.
func (s *Server) Enabled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadState().Enabled
}

// Credentials is the owner's sign-in for the server's own login page.
func (s *Server) Credentials() (email, password string, e error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var k secrets
	if !s.loadState().Enabled || readJSON(s.p.secrets(), &k) != nil || k.Token == "" {
		return "", "", errNotConfigured
	}
	return k.AdminEmail, k.AdminPassword, nil
}
