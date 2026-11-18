package provider

import (
	"context"
	"dsmapp/internal/connector"
	"dsmapp/internal/supervise"
	"dsmapp/internal/transient"
	"encoding/json"
	"github.com/synopackageland/syno-pkg-land-sdk/supervision"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/ipc"
)

// NetBirdExec runs an unmodified, verified binary as the package account. It
// never invokes service install/start, a shell, root, or system network tools.
type NetBirdExec struct {
	Dir      string
	Binaries BinaryManager
	Commands TailscaleCommands
	// ProbeTimeout bounds one readiness probe; default 30 s, since a
	// swapped-out daemon on a loaded NAS can take that long to page back in.
	ProbeTimeout time.Duration
	// ReadyWindow bounds one wait for a new daemon to answer; default 3 min.
	// A restore waits again while the daemon still runs.
	ReadyWindow time.Duration
	LoginWindow time.Duration
	URLWindow   time.Duration
}
type netbirdExecSession struct {
	dir, cli, socket  string
	probe             time.Duration
	commands          TailscaleCommands
	daemon            connector.Process
	storage           *runtimeMonitor
	mu                sync.Mutex
	cancel            context.CancelFunc
	done              chan struct{}
	challenge         NetBirdSnapshot
	origins           []string
	window, urlWindow time.Duration
	readyWindow       time.Duration
	ready             bool
	proxyPort         int
}

func netbirdOSCommands(dir string) OSCommands {
	return OSCommands{Dir: dir, Env: []string{"USER=SynoPkgConnect", "NB_STATE_DIR=" + dir, "NB_USE_NETSTACK_MODE=true", "NB_NETSTACK_SKIP_PROXY=false", "NB_ENABLE_NETSTACK_LOCAL_FORWARDING=true", "NB_SOCKS5_LISTENER_ADDRESS=127.0.0.1"}}
}

type netbirdCommands struct {
	OSCommands
	mu    sync.Mutex
	login connector.Process
}

func (c *netbirdCommands) StopLogin() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.login != nil {
		return c.login.Stop()
	}
	return nil
}

func (c *netbirdCommands) Stream(ctx context.Context, binary string, out io.Writer, args ...string) error {
	cmd := c.command(binary, args...)
	cmd.Stdout = out
	cmd.Stderr = out
	c.mu.Lock()
	p, e := connector.StartOwned(cmd)
	if e != nil {
		c.mu.Unlock()
		return nbError("control_unreachable")
	}
	c.login = p
	c.mu.Unlock()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for p.Alive() {
		select {
		case <-ctx.Done():
			return p.Stop()
		case <-ticker.C:
		}
	}
	if cmd.ProcessState == nil || !cmd.ProcessState.Success() {
		return nbError("login_denied")
	}
	return nil
}
func (d NetBirdExec) CheckBinaries(ctx context.Context) error {
	if d.Binaries == nil {
		return connector.ErrBinaryRequired
	}
	b, e := d.Binaries.Resolve(ctx, "netbird")
	if e != nil || !filepath.IsAbs(b.Path) {
		return connector.ErrBinaryRequired
	}
	return nil
}
func (d NetBirdExec) Open(ctx context.Context) (NetBirdSession, error) {
	if e := d.CheckBinaries(ctx); e != nil {
		return nil, e
	}
	b, e := d.Binaries.Resolve(ctx, "netbird")
	if e != nil {
		return nil, ErrUnavailable
	}
	dir := filepath.Join(d.Dir, "client")
	if e = os.MkdirAll(dir, 0700); e != nil {
		return nil, ErrUnavailable
	}
	if e = os.Chmod(dir, 0700); e != nil {
		return nil, ErrUnavailable
	}
	if e = os.Remove(filepath.Join(dir, "setup-key")); e != nil && !os.IsNotExist(e) {
		return nil, ErrUnavailable
	}
	proxyPort := 0
	commands := d.Commands
	if commands == nil {
		listener, err := ipc.ReserveLoopback(ctx, 0)
		if err != nil {
			return nil, nbError("proxy_bind_failed")
		}
		proxyPort = int(listener.Port())
		listener.Release()
		base, err := netbirdRuntimeCommands(dir)
		if err != nil {
			return nil, ErrUnavailable
		}
		base.Env = append(base.Env, "NB_SOCKS5_LISTENER_PORT="+strconv.Itoa(proxyPort))
		commands = &netbirdCommands{OSCommands: base}
	}
	probe := d.ProbeTimeout
	if probe <= 0 {
		probe = 30 * time.Second
	}
	readyWindow := d.ReadyWindow
	if readyWindow <= 0 {
		readyWindow = 3 * time.Minute
	}
	window := d.LoginWindow
	if window <= 0 {
		window = 10 * time.Minute
	}
	urlWindow := d.URLWindow
	if urlWindow <= 0 {
		urlWindow = time.Minute
	}
	s := &netbirdExecSession{readyWindow: readyWindow, window: window, urlWindow: urlWindow, proxyPort: proxyPort, dir: dir, cli: b.Path, socket: "unix://" + filepath.Join(dir, "netbird.sock"), probe: probe, commands: commands}
	s.daemon, e = commands.Start(b.Path, "service", "run", "--config", filepath.Join(dir, "default.json"), "--daemon-addr", s.socket, "--log-file", "console", "--log-level", "error", "--disable-profiles", "--disable-networks")
	if e != nil {
		return nil, ErrUnavailable
	}
	if d.Commands == nil {
		runtimeDir, err := transient.For(dir, "netbird", false)
		if err != nil {
			_ = s.daemon.Stop()
			return nil, ErrUnavailable
		}
		s.storage = watchRuntime(runtimeDir, "netbird", runtimeSweep, s.daemon.Stop, s.daemon.(interface{ Done() <-chan struct{} }).Done())
	}
	return s, nil
}
func (s *netbirdExecSession) run(ctx context.Context, args ...string) ([]byte, error) {
	raw, e := s.commands.Run(ctx, s.cli, append([]string{"--daemon-addr", s.socket, "--log-file", "console", "--log-level", "error"}, args...)...)
	if e != nil || len(raw) > 65536 {
		return nil, ErrUnavailable
	}
	return raw, nil
}
func (s *netbirdExecSession) ConfigureLogin(origins []string) {
	s.origins = append([]string(nil), origins...)
}

// WaitReady waits until the new daemon answers a status read. On a swapping
// NAS that can take minutes; the probes back off rather than add load. The
// read asks only for the daemon's own state: status --json also waits for
// NetBird's connection engine, which never answers while it keeps trying to
// reach its control server, so a running daemon would never read as ready
// and never be signed in. CLIs before 0.70 have no --check; they fall back.
func (s *netbirdExecSession) WaitReady(ctx context.Context) error {
	s.mu.Lock()
	ready := s.ready
	s.mu.Unlock()
	if ready {
		return nil
	}
	e := supervise.Readiness{Window: s.readyWindow, Probe: s.probe, Alive: s.daemon.Alive, Read: func(ctx context.Context) error {
		_, e := s.run(ctx, "status", "--check", "live")
		if e != nil && ctx.Err() == nil {
			_, e = s.run(ctx, "status", "--json")
		}
		return e
	}}.Wait(ctx)
	if e != nil {
		return nbError("daemon_start_timeout")
	}
	s.mu.Lock()
	s.ready = true
	s.mu.Unlock()
	return nil
}

// Alive reports whether the daemon process still runs.
func (s *netbirdExecSession) Alive() bool {
	return (s.storage == nil || !s.storage.failed.Load()) && s.daemon.Alive()
}

func (s *netbirdExecSession) Login(ctx context.Context, management, key string) (result error) {
	if e := s.WaitReady(ctx); e != nil {
		return e
	}
	args := []string{"up", "--management-url", management, "--hostname", "synopkgconnect", "--disable-client-routes", "--disable-server-routes", "--disable-dns", "--disable-ipv6", "--allow-server-ssh=false", "--allow-remote-jobs=false", "--no-browser", "--wireguard-port=0"}
	path := filepath.Join(s.dir, "setup-key")
	if key != "" {
		if e := connector.WritePrivate(path, []byte(key)); e != nil {
			return nbError("setup_key_rejected")
		}
		defer func() {
			if e := os.Remove(path); e != nil && !os.IsNotExist(e) {
				result = ErrUnavailable
			}
		}()
		args = append(args, "--setup-key-file", path)
		limited, stop := context.WithTimeout(ctx, time.Minute)
		defer stop()
		if _, e := s.run(limited, args...); e != nil {
			return nbError("setup_key_rejected")
		}
		return nil
	}
	// The CLI belongs to the session, not the CGI/start request. Returning a
	// challenge must not cancel WaitSSOLogin; Stop joins its owned process.
	loginCtx, stop := context.WithCancel(context.Background())
	s.cancel = stop
	s.done = make(chan struct{})
	parser := newNetbirdLoginParser(netbirdLoginOrigins(management, s.origins))
	parser.onError = stop
	timer := time.NewTimer(s.urlWindow)
	authenticated := make(chan struct{})
	signedIn := false // guarded by s.mu, including a racing timer delivery
	s.challenge.Deadline = time.Now().Add(s.urlWindow)
	parser.onChallenge = func(u, code string) {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.challenge.LoginURL = u
		s.challenge.UserCode = code
		s.challenge.Deadline = time.Now().Add(s.window)
		timer.Reset(s.window)
	}
	go func() {
		select {
		case <-s.done:
		case <-loginCtx.Done():
		case <-authenticated:
		case <-timer.C:
			s.mu.Lock()
			if signedIn {
				s.mu.Unlock()
				return
			}
			code := "login_url_timeout"
			if s.challenge.LoginURL != "" {
				code = "login_wait_timeout"
			}
			s.challenge.Code = code
			s.challenge.LoginURL = ""
			s.challenge.UserCode = ""
			s.mu.Unlock()
			stop()
			<-s.done
			_ = s.daemon.Stop()
		}
	}()
	go func() {
		defer close(s.done)
		defer timer.Stop()
		streamCommand := func(command []string) error {
			argv := append([]string{"--daemon-addr", s.socket, "--log-file", "console", "--log-level", "error"}, command...)
			if stream, ok := s.commands.(interface {
				Stream(context.Context, string, io.Writer, ...string) error
			}); ok {
				return stream.Stream(loginCtx, s.cli, parser, argv...)
			}
			raw, err := s.commands.Run(loginCtx, s.cli, argv...)
			if err == nil {
				_, err = parser.Write(raw)
			}
			return err
		}
		// A registered identity logs in without a URL. Bound authentication
		// separately: up may then wait for Management/Signal connectivity.
		e := streamCommand([]string{"login", "--management-url", management, "--hostname", "synopkgconnect", "--no-browser"})
		if e == nil {
			s.mu.Lock()
			signedIn = true
			s.challenge.Deadline = time.Time{}
			s.challenge.LoginURL, s.challenge.UserCode = "", ""
			s.mu.Unlock()
			timer.Stop()
			close(authenticated)
			if loginCtx.Err() == nil {
				e = streamCommand(args)
			}
		}
		_, _, parseErr := parser.Challenge()
		if parseErr != nil {
			e = parseErr
		} else if e != nil && parser.diagnostic != "" {
			e = nbError(parser.diagnostic)
		}
		s.mu.Lock()
		if e != nil && s.challenge.Code == "" {
			code := connector.ErrorCode(e)
			if code == "" {
				code = "control_unreachable"
			}
			s.challenge.Code = code
			if code == "login_origin_required" {
				s.challenge.LoginOrigin = parser.origin
			}
			s.challenge.LoginURL = ""
			s.challenge.UserCode = ""
		}
		s.mu.Unlock()
		if e != nil {
			_ = s.daemon.Stop()
		}
	}()
	return nil
}
func (s *netbirdExecSession) Inspect(ctx context.Context) (NetBirdSnapshot, error) {
	s.mu.Lock()
	challenge := s.challenge
	s.mu.Unlock()
	if challenge.Code != "" {
		return challenge, nil
	}
	if !s.daemon.Alive() {
		return NetBirdSnapshot{}, ErrDaemonExited
	}
	raw, e := s.run(ctx, "status", "--json")
	if e != nil {
		return NetBirdSnapshot{}, e
	}
	s.mu.Lock()
	challenge = s.challenge
	s.mu.Unlock()
	if challenge.Code != "" {
		return challenge, nil
	}
	var v struct {
		DaemonStatus string `json:"daemonStatus"`
		FQDN         string `json:"fqdn"`
		Management   struct {
			Connected *bool `json:"connected"`
		} `json:"management"`
		Signal struct {
			Connected *bool `json:"connected"`
		} `json:"signal"`
		IP    string `json:"netbirdIp"`
		Peers struct {
			Connected *int `json:"connected"`
		} `json:"peers"`
	}
	if json.Unmarshal(raw, &v) != nil || v.DaemonStatus == "" || v.Management.Connected == nil || v.Signal.Connected == nil {
		return NetBirdSnapshot{Code: "provider_status_invalid"}, nil
	}
	if v.DaemonStatus == "NeedsLogin" && challenge.LoginURL == "" && s.done != nil {
		select {
		case <-s.done:
			// Login may have completed after the snapshot above. Receiving
			// done joins its final write; preserve the actual refusal code
			// (or final challenge) instead of inventing malformed output.
			s.mu.Lock()
			challenge = s.challenge
			s.mu.Unlock()
			if challenge.Code != "" {
				return challenge, nil
			}
			if challenge.LoginURL == "" {
				challenge.Code = "login_output_invalid"
			}
		default:
		}
	}
	challenge.ManagementConnected = *v.Management.Connected
	challenge.SignalConnected = *v.Signal.Connected
	challenge.IP = v.IP
	challenge.DaemonStatus = v.DaemonStatus
	challenge.FQDN = v.FQDN
	challenge.PeersOnline = v.Peers.Connected
	return challenge, nil
}

// Resumes reports whether the daemon is bringing its saved identity up by
// itself: its status reads Connecting or Connected. status --json waits while
// the engine connects, so a read that does not answer while the daemon
// answers its own state also counts. The reads are short: the provider lock
// is held.
func (s *netbirdExecSession) Resumes(ctx context.Context) bool {
	full, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	raw, e := s.run(full, "status", "--json")
	if e == nil {
		var v struct {
			DaemonStatus string `json:"daemonStatus"`
		}
		return json.Unmarshal(raw, &v) == nil && (v.DaemonStatus == "Connected" || v.DaemonStatus == "Connecting")
	}
	if full.Err() == nil || ctx.Err() != nil {
		return false
	}
	own, stop := context.WithTimeout(ctx, 3*time.Second)
	defer stop()
	return s.Responsive(own) == nil
}

// Responsive reports whether the daemon answers its own state (Idle,
// Connecting or Connected) while no sign-in is in flight. NetBird answers it
// at once even while its connection engine is busy; a sign-in in flight, a
// refused sign-in or a daemon that answers nothing is not an answer.
func (s *netbirdExecSession) Responsive(ctx context.Context) error {
	if s.done != nil {
		select {
		case <-s.done:
		default:
			return ErrUnavailable
		}
	}
	_, e := s.run(ctx, "status", "--check", "ready")
	return e
}
func (s *netbirdExecSession) Stop() error {
	s.storage.Close()
	if s.cancel != nil {
		s.cancel()
		<-s.done
	}
	if owned, ok := s.commands.(interface{ StopLogin() error }); ok {
		if e := retryStop("netbird login", owned.StopLogin); e != nil {
			return ErrNetBirdStop
		}
	}
	if err := s.daemon.Stop(); err != nil {
		return err
	}
	if err := os.Remove(strings.TrimPrefix(s.socket, "unix://")); err != nil && !os.IsNotExist(err) {
		return ErrNetBirdStop
	}
	return transient.Clear(s.dir, "netbird")
}

func netbirdRuntimeCommands(dir string) (OSCommands, error) {
	if err := transient.Clear(dir, "netbird"); err != nil {
		return OSCommands{}, err
	}
	runtimeDir, err := transient.For(dir, "netbird", true)
	if err != nil {
		return OSCommands{}, err
	}
	base := netbirdOSCommands(dir)
	base.LimitFiles = true
	base.Env = append(base.Env, "TMPDIR="+runtimeDir, "XDG_CONFIG_HOME="+runtimeDir+"/config")
	for i, env := range base.Env {
		if strings.HasPrefix(env, "NB_STATE_DIR=") {
			base.Env[i] = "NB_STATE_DIR=" + runtimeDir
		}
	}
	return base, nil
}

func (d NetBirdExec) Dependency(ctx context.Context, s Settings) error {
	if d.Commands != nil {
		return nil
	}
	host := "controlplane.tailscale.com"
	raw := s.LoginServer
	host = "api.netbird.io"
	raw = s.ManagementURL

	if raw != "" {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			return connector.Coded("invalid_config", "invalid management address")
		}
		host = u.Hostname()
	}
	return supervision.NetworkReady(ctx, host)
}

func (s *netbirdExecSession) LastExit() supervision.Exit {
	if s.storage != nil && s.storage.failed.Load() {
		return supervision.Exit{Code: -1, Reason: s.storage.failureReason(), Permanent: true}
	}
	if p, ok := s.daemon.(interface{ Exit() supervision.Exit }); ok && !s.daemon.Alive() {
		return p.Exit()
	}
	return supervision.Exit{Code: -1, Reason: "daemon_exit"}
}
