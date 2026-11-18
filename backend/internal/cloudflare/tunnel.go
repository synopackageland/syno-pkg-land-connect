package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"dsmapp/internal/apps"
	"dsmapp/internal/transient"
	"github.com/synopackageland/syno-pkg-land-sdk/ipc"

	"dsmapp/internal/connector"
	"dsmapp/internal/dependency"
	"dsmapp/internal/publicname"
	"dsmapp/internal/supervise"
	"github.com/synopackageland/syno-pkg-land-sdk/supervision"
)

var (
	errPortUnknown = connector.Coded("port_detection_unavailable", "DSM's port could not be detected")
	errStart       = connector.Coded("cloudflare_start_failed", "cloudflared could not start")
	errStopped     = connector.Coded("cloudflare_stopped", "cloudflared stopped")
	errNoAddress   = connector.Coded("cloudflare_quick_address_unavailable", "Cloudflare gave no temporary address")
	errHostname    = connector.Coded("cloudflare_invalid_hostname", "the address must be a domain name such as nas.example.com")
	errSave        = connector.Coded("cloudflare_save_failed", "the connection settings could not be saved")
)

// Config wires a Tunnel to the NAS. Zero durations use the defaults.
type Config struct {
	Changed         func()
	Dependency      func(context.Context) error
	CheckRemoval    func(dependency.Resource) error
	CheckAccessMode func(string) error
	Unrestricted    func(func() error) error
	BeforeRoute     func(context.Context, string, string) error
	AccessProtected func() bool
	NameDir         string
	Apps            AppTargets
	// DSMTarget resolves the approved HTTPS pin-proxy socket for this authority.
	DSMTarget      func(context.Context, string) (string, error)
	IngressOnly    func() bool
	CheckPort      func(context.Context, int) error
	CheckWeb       func(context.Context, string) error
	Dir            string
	Cloudflared    func(context.Context) (string, error)
	DSMPort        func(context.Context) (int, error)
	Launcher       Launcher
	API            API
	MetricsAddr    func() (string, error)
	LoginTimeout   time.Duration
	AddressTimeout time.Duration
	Poll           time.Duration
	RestartDelay   time.Duration
	// NetBirdServer is the self-hosted server's origin while it is enabled.
	NetBirdServer func() (ServerOrigin, bool)
}

func orDefault(d, fallback time.Duration) time.Duration {
	if d <= 0 {
		return fallback
	}
	return d
}

// Tunnel runs one official cloudflared at a time: "quick" (a temporary
// trycloudflare.com address) or "domain" (a tunnel on the user's own domain,
// made once after a browser sign-in). A "token" tunnel (made in the
// Cloudflare dashboard) saved by an earlier version still restores and stops,
// but no request starts a new one. Starts are serialized by mu; the
// running job owns its processes and reports through set.
type Tunnel struct {
	cleanupURL  string
	cleanupNote string
	healthAddr  string
	safe        *domainUpdate
	cfg         Config
	mu          sync.Mutex
	// ops serializes authorization, DNS, and compensation, including async workers.
	ops        sync.Mutex
	authCancel context.CancelFunc
	authDone   chan struct{}
	cancel     context.CancelFunc
	done       chan struct{}

	smu    sync.Mutex
	jobs   uint64
	gen    uint64
	status connector.Status
	// aside is the status of a sign-in that runs beside the job serving
	// status, shown in its place until it ends.
	aside    *connector.Status
	asideGen uint64
	// restoring retries a restore whose cloudflared or DSM port is not
	// available yet, through the restore path every provider shares.
	restoring supervise.Restorer
}

func NewTunnel(cfg Config) *Tunnel {
	t := &Tunnel{cfg: cfg}
	t.status = t.idle()
	return t
}

func (t *Tunnel) home() string      { return filepath.Join(t.cfg.Dir, "home") }
func (t *Tunnel) tokenFile() string { return filepath.Join(t.cfg.Dir, "token") }
func (t *Tunnel) launcher() Launcher {
	if t.cfg.Launcher == nil {
		return ExecLauncher{}
	}
	return t.cfg.Launcher
}

// settings is the restart intent. It holds no secret.
type settings struct {
	Enabled  bool   `json:"enabled"`
	Mode     string `json:"mode"`
	Hostname string `json:"hostname,omitempty"`
}

func (t *Tunnel) settingsFile() string { return filepath.Join(t.cfg.Dir, "settings.json") }
func (t *Tunnel) loadSettings() settings {
	var s settings
	if raw, e := connector.ReadPrivate(t.settingsFile()); e == nil {
		json.Unmarshal(raw, &s)
	}
	return s
}
func (t *Tunnel) saveSettings(s settings) error {
	raw, _ := json.Marshal(s)
	if e := connector.WritePrivate(t.settingsFile(), raw); e != nil {
		return storageError(e)
	}
	return nil
}

func (t *Tunnel) Status() connector.Status {
	t.smu.Lock()
	defer t.smu.Unlock()
	s := t.status
	s.CleanupNote = t.cleanupNote
	s.CleanupURL = t.cleanupURL
	if t.aside != nil {
		s = *t.aside
	}
	s.Endpoints = append([]connector.Endpoint(nil), s.Endpoints...)
	if s.NameConflict != nil {
		s.SuggestedName = s.NameConflict.SuggestedName
	}
	if s.SuggestedName == "" {
		s.SuggestedName, _ = t.defaultName()
	}
	return s
}

// begin makes s the status of a new job; set then ignores older jobs.
func (t *Tunnel) begin(s connector.Status) uint64 {
	t.smu.Lock()
	defer t.smu.Unlock()
	t.jobs++
	t.gen, t.aside = t.jobs, nil
	s.Provider = "cloudflare"
	t.status = s
	return t.gen
}

// beside starts a job whose status s is shown while the current job keeps
// running and reporting underneath.
func (t *Tunnel) beside(s connector.Status) uint64 {
	t.smu.Lock()
	defer t.smu.Unlock()
	if t.status.State == "connected" {
		s.Endpoints = append([]connector.Endpoint(nil), t.status.Endpoints...)
		s.ShareURL = t.status.ShareURL
	}
	t.jobs++
	s.Provider = "cloudflare"
	t.aside, t.asideGen = &s, t.jobs
	return t.jobs
}

// besideEnded shows the job underneath again, with the reason the job
// beside it ended while it is still serving.
func (t *Tunnel) besideEnded(gen uint64, e error) {
	t.smu.Lock()
	defer t.smu.Unlock()
	if t.aside == nil || t.asideGen != gen {
		return
	}
	t.aside = nil
	if t.status.State == "connected" || t.status.State == "connecting" {
		t.status.Code = failCode(e)
		t.status.NameConflict = connector.Conflict(e)
	}
}

func (t *Tunnel) set(gen uint64, change func(*connector.Status)) {
	t.smu.Lock()
	defer t.smu.Unlock()
	if gen == t.gen {
		before := t.status.State
		change(&t.status)
		if before != t.status.State && t.cfg.Changed != nil {
			t.cfg.Changed()
		}
	}
	if t.aside != nil && gen == t.asideGen {
		change(t.aside)
	}
}

func failCode(e error) string {
	if code := connector.ErrorCode(e); code != "" {
		return code
	}
	return "cloudflare_failed"
}

// fail ends a job. A failed own domain names the saved one, which
// reconnects without signing in.
func (t *Tunnel) fail(gen uint64, e error) {
	saved, missing := t.savedDomain()
	t.set(gen, func(s *connector.Status) {
		s.State, s.Code, s.LoginURL, s.Endpoints = "failed", failCode(e), "", nil
		s.NameConflict = connector.Conflict(e)
		if s.Mode == "domain" && missing == nil {
			s.Hostname = saved.Hostname
		}
	})
}

// idle describes a stopped tunnel, naming a saved own domain to reconnect.
func (t *Tunnel) idle() connector.Status {
	s := connector.Status{Provider: "cloudflare", State: "disconnected", Mode: t.loadSettings().Mode}
	if d, e := t.savedDomain(); e == nil {
		s.Hostname = d.Hostname
	}
	return s
}

// stop cancels the running job and waits until its processes are gone.
func (t *Tunnel) stop() {
	t.restoring.Cancel()
	if t.cancel != nil {
		t.cancel()
		<-t.done
		t.cancel = nil
	}
	t.stopAuthorization()
	t.begin(t.idle())
}

// spawn runs job as the owner of the new status generation.
func (t *Tunnel) spawn(s connector.Status, job func(context.Context, uint64)) {
	gen := t.begin(s)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	t.cancel, t.done = cancel, done
	go func() {
		defer close(done)
		job(ctx, gen)
	}()
}

// prepare finds cloudflared and DSM's HTTPS port and makes the private
// directories. Nothing starts without them.
func (t *Tunnel) prepareReadOnly(ctx context.Context) (string, int, error) {
	bin, e := t.cfg.Cloudflared(ctx)
	if e != nil {
		return "", 0, connector.ErrBinaryRequired
	}
	port, e := t.cfg.DSMPort(ctx)
	if e != nil || port < 1 || port > 65535 {
		return "", 0, errPortUnknown
	}
	return bin, port, nil
}
func (t *Tunnel) prepare(ctx context.Context) (string, int, error) {
	bin, port, e := t.prepareReadOnly(ctx)
	if e != nil {
		return "", 0, e
	}
	for _, dir := range []string{t.cfg.Dir, t.home()} {
		if e := privateDir(dir); e != nil {
			return "", 0, e
		}
	}
	return bin, port, nil
}

// run is one long-lived cloudflared serving DSM at hostname.
type run struct {
	addressCreatedAt           time.Time
	mode, hostname, limitation string
	port                       int
	args                       func(metrics string) []string
	bind                       func(context.Context, string) error
	close                      func() error
}

func (t *Tunnel) launch(ctx context.Context, bin string, r run) (Process, string, *lines, error) {
	metrics := t.cfg.MetricsAddr
	if metrics == nil {
		metrics = freeLoopback
	}
	addr, e := metrics()
	if e != nil {
		return nil, "", nil, errStart
	}
	out := &lines{}
	if r.hostname == "" {
		out.find = quickHostnameFromOutput
	} else {
		out.find = missingTunnelFromOutput
	}
	p, e := t.launcher().Start(ctx, Command{Path: bin, Args: r.args(addr), Home: t.home(), Output: out})
	if e != nil {
		if supervise.Permanent("cloudflare", e) {
			return nil, "", nil, e
		}
		return nil, "", nil, errStart
	}
	return p, addr, out, nil
}

// serve starts r now and watches it in the background.
func (t *Tunnel) serve(ctx context.Context, bin string, r run) error {
	p, addr, out, e := t.launch(ctx, bin, r)
	if e != nil {
		if r.close != nil {
			_ = r.close()
		}
		t.begin(connector.Status{State: "failed", Code: connector.ErrorCode(e), Mode: r.mode, Hostname: r.hostname})
		return e
	}
	cfg := supervision.Defaults()
	if t.cfg.RestartDelay > 0 {
		cfg.Initial = t.cfg.RestartDelay
		cfg.Min = min(time.Second, cfg.Initial)
	}
	policy := supervision.New(cfg, nil)
	if t.restoring.Active() && t.restoring.Policy() != nil {
		policy = t.restoring.Policy()
	}
	t.spawn(connector.Status{State: "connecting", Mode: r.mode, Hostname: r.hostname, KnownLimitation: r.limitation}, func(ctx context.Context, gen uint64) { t.watch(ctx, gen, p, addr, out, r, policy) })
	return nil
}

// watch follows cloudflared until it exits or the job ends. Connected means
// cloudflared reached Cloudflare and the address is known; whether the
// address reaches DSM is the separate check.
func (t *Tunnel) watch(ctx context.Context, gen uint64, p Process, addr string, out *lines, r run, policy *supervision.Policy) {
	defer func() {
		_ = p.Stop()
		if r.close != nil {
			_ = r.close()
		}
	}()
	host := r.hostname
	boundHost := ""
	recovered := false
	cleaned := false
	addressCreatedAt := r.addressCreatedAt
	deadline := time.Now().Add(orDefault(t.cfg.AddressTimeout, time.Minute))
	tick := time.NewTicker(min(orDefault(t.cfg.Poll, time.Second), time.Second))
	defer tick.Stop()
	policy.Started(time.Now())
	for ctx.Err() == nil {
		// A live connector does not prove its original DSM listener still
		// exists. Stop stale forwarding and offer the existing review action.
		if r.mode != "token" {
			if current, err := t.cfg.DSMPort(ctx); err == nil && current > 0 && current != r.port {
				t.set(gen, func(s *connector.Status) {
					s.State, s.Code, s.Endpoints = "failed", "pin_proxy_required", nil
					s.DSMPortChange = &connector.DSMPortChange{From: r.port, To: current}
				})
				return
			}
		}
		var recoveryStartError error
		if r.mode == "domain" && (out.found() == "cloudflare_tunnel_missing" || t.recreationPending()) {
			t.set(gen, func(s *connector.Status) {
				s.State = "connecting"
				s.Phase = "recovering"
				s.Code = ""
				s.Endpoints = nil
			})
			if err := p.Stop(); err != nil {
				t.fail(gen, err)
				return
			}
			nextRun, err := t.recoverRun(ctx, gen, r)
			if err != nil {
				return
			}
			r = nextRun
			recovered = true
			// Replacing the deleted identity is not a child crash. A failed
			// launch still enters the usual restart supervision below.
			out = &lines{}
			bin, err := t.cfg.Cloudflared(ctx)
			if err == nil {
				var next Process
				var nextAddr string
				var nextOut *lines
				next, nextAddr, nextOut, err = t.launch(ctx, bin, r)
				if err == nil {
					p, addr, out = next, nextAddr, nextOut
					policy.Started(time.Now())
				}
			}
			recoveryStartError = err
			host, addressCreatedAt = r.hostname, r.addressCreatedAt
		}
		if r.mode != "domain" && out.found() == "cloudflare_tunnel_missing" {
			t.fail(gen, errTunnelMissing)
			return
		}
		if !p.Alive() {
			exit := supervision.Exit{Code: -1, Reason: "cloudflare_child_exit"}
			if owned, ok := p.(interface{ Exit() supervision.Exit }); ok {
				exit = owned.Exit()
			}
			if recoveryStartError != nil {
				exit = supervision.ExitOf(recoveryStartError)
				exit.Permanent = exit.Permanent || supervise.Permanent("cloudflare", recoveryStartError)
			}
			if err := p.Stop(); err != nil {
				exit.Permanent = true
				exit.Reason = "cleanup_required"
			}
			snapshot := policy.Exited(time.Now(), exit)
			t.set(gen, func(s *connector.Status) {
				s.Supervision = &snapshot
				s.State = string(snapshot.State)
				s.Phase = string(snapshot.State)
				s.Code = snapshot.Reason
				s.Endpoints = nil
			})
			connector.Logf("cloudflare: state=%s attempts=%d reason=%s", snapshot.State, snapshot.Attempts, snapshot.Reason)
			if snapshot.State != supervision.Backoff {
				return
			}
			if err := supervision.Wait(ctx, time.Until(snapshot.NextRetry)); err != nil {
				return
			}
			// A failed exec remains in this same series, never a fresh policy.
			for {
				if t.cfg.Dependency != nil {
					policy.Waiting("network")
					snapshot = policy.Snapshot()
					t.set(gen, func(s *connector.Status) { s.Supervision = &snapshot; s.State = "waiting"; s.Phase = "waiting" })
					if supervision.Dependency(ctx, t.cfg.Dependency, nil) != nil {
						return
					}
				}
				bin, e := t.cfg.Cloudflared(ctx)
				var next Process
				var nextAddr string
				var nextOut *lines
				if e == nil {
					next, nextAddr, nextOut, e = t.launch(ctx, bin, r)
				}
				if e == nil {
					p, addr, out = next, nextAddr, nextOut
					policy.Started(time.Now())
					break
				}
				ex := supervision.ExitOf(e)
				ex.Permanent = ex.Permanent || supervise.Permanent("cloudflare", e)
				snapshot = policy.Exited(time.Now(), ex)
				t.set(gen, func(s *connector.Status) {
					s.Supervision = &snapshot
					s.State = string(snapshot.State)
					s.Code = snapshot.Reason
				})
				if snapshot.State != supervision.Backoff {
					return
				}
				if supervision.Wait(ctx, time.Until(snapshot.NextRetry)) != nil {
					return
				}
			}
			addressCreatedAt = r.addressCreatedAt
			host = r.hostname
			boundHost = ""
			deadline = time.Now().Add(orDefault(t.cfg.AddressTimeout, time.Minute))
		}
		if host == "" {
			host = quickHostnameFromMetrics(metricsGet(ctx, addr, "/quicktunnel"))
		}
		if host == "" {
			host = out.found()
		}
		if host == "" && time.Now().After(deadline) {
			t.fail(gen, errNoAddress)
			return
		}
		if r.mode == "quick" && host != "" && addressCreatedAt.IsZero() {
			addressCreatedAt = time.Now()
		}
		if r.bind != nil && host != "" && boundHost != host {
			t.set(gen, func(s *connector.Status) { s.Hostname = host })
			if err := r.bind(ctx, host); err != nil {
				t.fail(gen, err)
				return
			}
			boundHost = host
		}
		ready := host != "" && metricsGet(ctx, addr, "/ready") != nil
		if ready && r.mode == "domain" && !cleaned && t.ops.TryLock() {
			cleaned = true
			if c, err := readCert(t.certFile()); err == nil {
				if d, err := t.savedDomain(); err == nil {
					t.cleanupRetired(ctx, c, d.TunnelID)
				}
			}
			t.ops.Unlock()
		}
		t.smu.Lock()
		if gen == t.gen {
			t.healthAddr = addr
		}
		t.smu.Unlock()
		t.set(gen, func(s *connector.Status) {
			s.AddressCreatedAt = addressCreatedAt
			s.Hostname, s.State, s.Endpoints = host, "connecting", nil
			snapshot := policy.Snapshot()
			s.Supervision = &snapshot
			if s.Code == "cloudflare_restarting" {
				s.Code = ""
			}
			if ready {
				if recovered {
					s.Code = ""
					s.Phase = ""
					recovered = false
				}
				s.State, s.Endpoints = "connected", []connector.Endpoint{{URL: "https://" + host, Public: true, LocalPort: r.port}}
			}
		})
		select {
		case <-ctx.Done():
		case <-tick.C:
		}
	}
}

// metricsReady reports whether cloudflared's metrics server answered within
// ctx, and whether it reports a connection to Cloudflare.
func metricsReady(ctx context.Context, addr string) (answered, ready bool) {
	if addr == "" {
		return false, false
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/ready", nil)
	if e != nil {
		return false, false
	}
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	res, e := (&http.Client{Transport: transport}).Do(req)
	if e != nil {
		return false, false
	}
	res.Body.Close()
	return true, res.StatusCode == http.StatusOK
}

// metricsGet returns a 200 reply from cloudflared's loopback metrics server.
func metricsGet(ctx context.Context, addr, path string) []byte {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+path, nil)
	if e != nil {
		return nil
	}
	// One poll per second: a kept-alive connection per poll would pile up.
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, e := client.Do(req)
	if e != nil {
		return nil
	}
	defer res.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(res.Body, 4096))
	if res.StatusCode != http.StatusOK || e != nil {
		return nil
	}
	return raw // never nil: an empty 200 is still a reply
}

func freeLoopback() (string, error) {
	l, e := ipc.ReserveLoopback(context.Background(), 0)
	if e != nil {
		return "", e
	}
	defer l.Release()
	return l.Address(), nil
}

// Quick runs a Quick Tunnel to DSM. Its address changes on every start.
func (t *Tunnel) Quick(ctx context.Context) error {
	if t.cfg.CheckAccessMode != nil {
		if e := t.cfg.CheckAccessMode("quick"); e != nil {
			return e
		}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stop()
	t.ops.Lock()
	e := os.Remove(t.certFile())
	t.ops.Unlock()
	if e != nil && !errors.Is(e, os.ErrNotExist) {
		return storageError(e)
	}
	return t.quick(ctx)
}
func (t *Tunnel) quick(ctx context.Context) error {
	if t.cfg.Unrestricted != nil {
		return t.cfg.Unrestricted(func() error { return t.quickOrigin(ctx) })
	}
	return t.quickOrigin(ctx)
}
func (t *Tunnel) quickOrigin(ctx context.Context) error {
	if t.cfg.CheckAccessMode != nil {
		if e := t.cfg.CheckAccessMode("quick"); e != nil {
			return e
		}
	}
	bin, port, e := t.prepare(ctx)
	if e != nil {
		return e
	}
	if t.cfg.DSMTarget == nil {
		return errProxyRequired
	}
	if e = t.saveSettings(settings{Enabled: true, Mode: "quick"}); e != nil {
		return e
	}
	if e = transient.Clear(t.cfg.Dir, "cloudflare"); e != nil {
		return e
	}
	dir, e := transient.For(t.cfg.Dir, "cloudflare", true)
	if e != nil {
		return e
	}
	config := filepath.Join(dir, "quick.yml")
	// cloudflared's Quick CLI requires --url to select Quick mode. Ingress
	// configuration takes precedence over that flag (2026.10.7). The URL
	// is an unreachable fallback, never DSM HTTP or an unverified HTTPS URL.
	raw := fmt.Sprintf("ingress:\n  - service: %q\n", "unix:"+apps.PinSocket(apps.QuickDSMSocketID))
	if e = connector.WritePrivate(config, []byte(raw)); e != nil {
		_ = transient.Clear(t.cfg.Dir, "cloudflare")
		return e
	}
	return t.serve(ctx, bin, run{mode: "quick", port: port, close: func() error { return transient.Clear(t.cfg.Dir, "cloudflare") }, bind: func(ctx context.Context, host string) error {
		if !quickHost(host) {
			return errProxyRequired
		}
		socket, err := t.cfg.DSMTarget(ctx, host)
		if err != nil {
			return err
		}
		if socket != apps.PinSocket(apps.QuickDSMSocketID) {
			return errProxyRequired
		}
		return nil
	}, args: func(metrics string) []string {
		return []string{"tunnel", "--no-autoupdate", "--config", config, "--metrics", metrics, "--url", "http://127.0.0.1:1"}
	}})
}

// RefreshDSMRoute follows explicit administrator review, reissuing a temporary
// address if enabled. A stopped connection stays stopped.
func (t *Tunnel) RefreshDSMRoute(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.loadSettings()
	if !s.Enabled || s.Mode != "quick" {
		return nil
	}
	t.stop()
	return t.quick(ctx)
}

// validHostname is a public name the user owns, not a Cloudflare one.
func validHostname(host string) bool {
	return validDomain(host) && !strings.HasSuffix(host, ".cfargotunnel.com") && !strings.HasSuffix(host, ".trycloudflare.com")
}

// token runs a dashboard tunnel an earlier version saved, by its private
// token file; hostname is the address the user routed to it there.
func (t *Tunnel) token(ctx context.Context, bin, hostname string) error {
	if t.cfg.Unrestricted != nil {
		return t.cfg.Unrestricted(func() error { return t.tokenOrigin(ctx, bin, hostname) })
	}
	return t.tokenOrigin(ctx, bin, hostname)
}
func (t *Tunnel) tokenOrigin(ctx context.Context, bin, hostname string) error {
	if t.cfg.CheckAccessMode != nil {
		if e := t.cfg.CheckAccessMode("token"); e != nil {
			return e
		}
	}
	return t.serve(ctx, bin, run{mode: "token", hostname: hostname, limitation: "cloudflare_remote_config_possible", args: func(metrics string) []string {
		return []string{"tunnel", "--no-autoupdate", "--metrics", metrics, "run", "--token-file", t.tokenFile()}
	}})
}

// Restore resumes the tunnel that was running when connectd stopped. A
// cloudflared or DSM port not available yet, as while a slow NAS boots,
// keeps retrying with backoff through the shared supervise.Restorer.
func (t *Tunnel) Restore(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.loadSettings()
	if !s.Enabled {
		return nil
	}
	t.ops.Lock()
	t.dropTCPLeftovers()
	t.ops.Unlock()
	switch s.Mode {
	case "quick":
	case "domain":
		if _, e := t.savedDomain(); e != nil {
			t.begin(connector.Status{State: "failed", Code: connector.ErrorCode(errInterrupted), Mode: "domain"})
			return nil
		}
	case "token":
		raw, e := connector.ReadPrivate(t.tokenFile())
		if e != nil || validateToken(string(raw)) != nil || !validHostname(s.Hostname) {
			t.begin(connector.Status{State: "failed", Code: connector.ErrorCode(ErrInvalidToken), Mode: "token"})
			return nil
		}
	default:
		return nil
	}
	r := &t.restoring
	r.Name, r.Lock, r.First = "cloudflare", &t.mu, t.cfg.RestartDelay
	r.Changed = t.cfg.Changed
	r.Dependency = t.cfg.Dependency
	r.Attempt = t.attempt
	r.Transition = func(snapshot supervision.Snapshot) {
		t.smu.Lock()
		defer t.smu.Unlock()
		t.status.Supervision = &snapshot
		if snapshot.State == supervision.Failed || snapshot.State == supervision.Waiting || snapshot.State == supervision.Backoff {
			t.status.State = string(snapshot.State)
			t.status.Phase = string(snapshot.State)
			t.status.Code = snapshot.Reason
		}
	}
	return r.Begin(ctx, &cloudflaredRestore{t: t, mode: s.Mode, hostname: s.Hostname})
}

// attempt names the current job for the logs.
func (t *Tunnel) attempt() uint64 {
	t.smu.Lock()
	defer t.smu.Unlock()
	return t.gen
}

// cloudflaredRestore is Cloudflare's part of a restore. cloudflared answers
// once started and its watch restarts it, so only the start itself retries.
type cloudflaredRestore struct {
	t              *Tunnel
	mode, hostname string
	bin            string
}

func (d *cloudflaredRestore) Open(ctx context.Context) error {
	bin, _, e := d.t.prepare(ctx)
	if e != nil {
		d.t.begin(connector.Status{State: "connecting", Phase: "starting", Code: failCode(e), Mode: d.mode, Hostname: d.hostname})
		return e
	}
	d.bin = bin
	return nil
}
func (*cloudflaredRestore) Waits() bool                     { return false }
func (*cloudflaredRestore) WaitReady(context.Context) error { return nil }
func (*cloudflaredRestore) Alive() bool                     { return false }
func (*cloudflaredRestore) Close() error                    { return nil }
func (d *cloudflaredRestore) SignIn(ctx context.Context) error {
	switch d.mode {
	case "quick":
		return d.t.quick(ctx)
	case "domain":
		return d.t.reconnect(ctx)
	}
	return d.t.token(ctx, d.bin, d.hostname)
}

// Stop ends the tunnel but keeps the intent, so Restore starts it again.
func (t *Tunnel) Stop() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stop()
	t.finishUpdate(false)
	return nil
}

// Disable ends the tunnel for good and deletes the pasted token. A saved own
// domain stays, so it can reconnect without signing in again.
func (t *Tunnel) Disable() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.restoring.Cancel()
	if t.cancel != nil {
		t.cancel()
		<-t.done
		t.cancel = nil
	}
	t.stopAuthorization()
	s := t.loadSettings()
	s.Enabled = false
	var failed error
	if _, e := os.Lstat(t.settingsFile()); e == nil {
		failed = t.saveSettings(s)
	}
	if e := os.Remove(t.tokenFile()); e != nil && !errors.Is(e, os.ErrNotExist) {
		failed = storageError(e)
	}
	t.begin(t.idle())
	t.finishUpdate(false)
	t.ops.Lock()
	if e := os.Remove(t.certFile()); e != nil && !errors.Is(e, os.ErrNotExist) {
		failed = storageError(e)
	}
	t.ops.Unlock()
	return failed
}

// Revoke deletes every Cloudflare secret in dir without starting anything.
// The token is only ever a file, so anything else there is reported, not
// removed.
func Revoke(dir string) error {
	var failed error
	if e := os.Remove(filepath.Join(dir, "token")); e != nil && !errors.Is(e, os.ErrNotExist) {
		failed = e
	}
	for _, name := range []string{"domain", "home", "settings.json"} {
		if e := os.RemoveAll(filepath.Join(dir, name)); e != nil {
			failed = e
		}
	}
	return failed
}

var errMode = connector.Coded("cloudflare_invalid_request", "choose a temporary address or your own domain")

// Request is what the page sends for one mode; other fields must be empty.
type Request struct {
	Mode, Subdomain, Token, Hostname string
	ReuseName                        *connector.NameReuse
}

func Validate(r Request) error {
	switch {
	case r.Mode == "quick" && r.ReuseName == nil && r.Subdomain == "" && r.Token == "" && r.Hostname == "":
		return nil
	case r.Mode == "domain" && r.Token == "" && r.Hostname == "":
		if r.Subdomain != "" && !dnsLabel.MatchString(r.Subdomain) {
			return errSubdomain
		}
		return nil
	}
	return errMode
}

// Start runs the mode r asks for.
func (t *Tunnel) Start(ctx context.Context, r Request) error {
	if e := Validate(r); e != nil {
		return e
	}
	switch r.Mode {
	case "quick":
		return t.Quick(ctx)
	}
	return t.Domain(connector.WithNameReuse(ctx, r.ReuseName), r.Subdomain)
}

func (t *Tunnel) defaultName() (string, error) {
	if d, err := t.savedDomain(); err == nil {
		label, _, _ := strings.Cut(d.Hostname, ".")
		return label, nil
	}
	return publicname.NASName(), nil
}

// CheckRemoval is shared by explicit tunnel, authorization and name changes.
// Lifecycle Stop and reconnect keep the connection's credentials and bypass it.
func (t *Tunnel) CheckRemoval(resource dependency.Resource) error {
	if t.cfg.CheckRemoval != nil {
		return t.cfg.CheckRemoval(resource)
	}
	return nil
}
func (t *Tunnel) PreserveDomain(subdomain string) bool {
	d, e := t.savedDomain()
	return e == nil && (subdomain == "" || strings.HasPrefix(d.Hostname, subdomain+"."))
}

// RecoveryTerminal is a memory-only observation for the package demand owner.
func (t *Tunnel) RecoveryTerminal() bool {
	t.smu.Lock()
	defer t.smu.Unlock()
	return t.status.State == "failed" || t.status.State == "stopped"
}
