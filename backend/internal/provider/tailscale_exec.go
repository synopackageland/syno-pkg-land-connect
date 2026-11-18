package provider

import (
	"bytes"
	"context"
	"dsmapp/internal/connector"
	"dsmapp/internal/localipc"
	"dsmapp/internal/supervise"
	"dsmapp/internal/transient"
	"encoding/json"
	"errors"
	"github.com/synopackageland/syno-pkg-land-sdk/supervision"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/ipc"
)

type TailscaleCommands interface {
	Run(context.Context, string, ...string) ([]byte, error)
	Start(string, ...string) (connector.Process, error)
}

// PrivateTargets resolves approved App/DSM targets and persists authorities from
// fresh daemon status before Serve can use them. Implementations own App state.
type PrivateTargets interface {
	ResolvePrivateTargets(context.Context, []PortMapping, string) ([]PortMapping, error)
}

type TailscaleExec struct {
	Targets  PrivateTargets
	Dir      string
	Binaries BinaryManager
	Commands TailscaleCommands
	// LoginWindow bounds URL acquisition; default 60 s. Human login gets a separate 10-minute window.
	LoginWindow time.Duration
	// ReadyWindow bounds one wait for a new daemon to answer; default 3 min.
	// ProbeTimeout bounds one readiness read; default 30 s, since a
	// swapped-out daemon pages itself back in first.
	ReadyWindow, ProbeTimeout time.Duration
	Now                       func() time.Time
}
type tailscaleExecSession struct {
	// mu guards the login phase and owned routes for the few moments they
	// change; a status read never holds it, so health can read without the
	// provider lock while status reads too.
	mu                    sync.Mutex
	background            bool
	version               string
	targets               PrivateTargets
	dir, cli, socket      string
	window                time.Duration
	commands              TailscaleCommands
	daemon                connector.Process
	storage               *runtimeMonitor
	forwards              []connector.Process
	mappings              []PortMapping
	login                 connector.Process
	server, origin, socks string
	started, deadline     time.Time
	now                   func() time.Time
	phase                 string
	readyWindow, probe    time.Duration
	ready                 bool
	// restored marks a daemon started from the saved identity: no new
	// sign-in makes it faster, so it never times out reaching the control
	// server or the network.
	restored bool
}

func (d TailscaleExec) Open(ctx context.Context) (TailscaleSession, error) {
	if d.Binaries == nil {
		return nil, ErrUnavailable
	}
	daemon, err := d.Binaries.Resolve(ctx, "tailscaled")
	if err != nil || !filepath.IsAbs(daemon.Path) {
		return nil, ErrUnavailable
	}
	cli, err := d.Binaries.Resolve(ctx, "tailscale")
	if err != nil || !filepath.IsAbs(cli.Path) {
		return nil, ErrUnavailable
	}
	if err = os.MkdirAll(d.Dir, 0700); err != nil {
		return nil, ErrUnavailable
	}
	if err = os.Chmod(d.Dir, 0700); err != nil {
		return nil, ErrUnavailable
	}
	if err = os.Remove(filepath.Join(d.Dir, "auth-key")); err != nil && !os.IsNotExist(err) {
		return nil, ErrUnavailable
	}
	commands := d.Commands
	if commands == nil {
		base, err := tailscaleRuntimeCommands(d.Dir)
		if err != nil {
			return nil, ErrUnavailable
		}
		commands = base
	}
	runtimeDir, err := transient.For(d.Dir, "tailscale", true)
	if err != nil {
		return nil, ErrUnavailable
	}
	window := d.LoginWindow
	if window <= 0 {
		window = 60 * time.Second
	}
	s := &tailscaleExecSession{dir: d.Dir, cli: cli.Path, socket: filepath.Join(d.Dir, "tailscaled.sock"), window: window, commands: commands, now: d.Now, background: d.Commands == nil, version: cli.Version, targets: d.Targets}
	if s.now == nil {
		s.now = time.Now
	}
	s.readyWindow, s.probe = d.ReadyWindow, d.ProbeTimeout
	if s.readyWindow <= 0 {
		s.readyWindow = 3 * time.Minute
	}
	if s.probe <= 0 {
		s.probe = 30 * time.Second
	}
	listener, e := ipc.ReserveLoopback(ctx, 0)
	if e != nil {
		return nil, connector.Coded("proxy_bind_failed", "loopback proxy unavailable")
	}
	s.socks = listener.Address()
	listener.Release()
	// No TUN, root, host DNS/routes, shared system daemon, or arbitrary listen host.
	s.daemon, err = commands.Start(daemon.Path, "--tun=userspace-networking", "--socket="+s.socket, "--state="+filepath.Join(d.Dir, "tailscaled.state"), "--statedir="+runtimeDir, "--port=0", "--socks5-server="+s.socks)
	if err != nil {
		return nil, ErrUnavailable
	}
	if d.Commands == nil {
		s.storage = watchRuntime(runtimeDir, "tailscale", runtimeSweep, s.daemon.Stop, s.daemon.(interface{ Done() <-chan struct{} }).Done())
	}
	return s, nil
}
func (s *tailscaleExecSession) run(ctx context.Context, args ...string) ([]byte, error) {
	return s.runFor(ctx, 2*time.Second, args...)
}
func (s *tailscaleExecSession) runFor(ctx context.Context, limit time.Duration, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	raw, err := s.commands.Run(ctx, s.cli, append([]string{"--socket=" + s.socket}, args...)...)
	if err != nil || len(raw) > 65536 {
		return nil, ErrUnavailable
	}
	return raw, nil
}
func (s *tailscaleExecSession) Login(ctx context.Context, key string) error {
	return s.LoginWithServer(ctx, key, "", "")
}
func (s *tailscaleExecSession) LoginWithServer(ctx context.Context, key, server, origin string) error {
	if e := s.awaitSocket(ctx); e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if server != "" {
		if _, e := exactOrigin(server); e != nil {
			return ErrUnavailable
		}
	}
	if origin != "" {
		if _, e := exactOrigin(origin); e != nil {
			return ErrUnavailable
		}
	}
	if s.login != nil {
		return nil
	}
	s.server, s.origin = server, origin
	args := []string{"--socket=" + s.socket, "up", "--json", "--accept-dns=false", "--accept-routes=false", "--hostname=synopkgconnect"}
	if server != "" {
		args = append(args, "--login-server="+server)
	}
	if key != "" {
		path := filepath.Join(s.dir, "auth-key")
		if e := connector.WritePrivate(path, []byte(key)); e != nil {
			return ErrUnavailable
		}
		args = append(args, "--auth-key=file:"+path)
	}
	p, e := s.commands.Start(s.cli, args...)
	if e != nil {
		_ = os.Remove(filepath.Join(s.dir, "auth-key"))
		return connector.Coded("login_denied", "login command failed")
	}
	s.login = p
	s.started = s.now()
	s.phase = "url"
	s.deadline = s.started.Add(s.window)
	return nil
}
func (s *tailscaleExecSession) finishLogin() error {
	if s.login != nil {
		if e := s.login.Stop(); e != nil {
			return ErrUnavailable
		}
		s.login = nil
	}
	if e := os.Remove(filepath.Join(s.dir, "auth-key")); e != nil && !os.IsNotExist(e) {
		return ErrUnavailable
	}
	return nil
}

// awaitSocket queries only our private daemon until it answers. On a
// swapping NAS that can take minutes; the probes back off rather than add load.
func (s *tailscaleExecSession) awaitSocket(ctx context.Context) error {
	s.mu.Lock()
	ready := s.ready
	s.mu.Unlock()
	if ready {
		return nil
	}
	e := supervise.Readiness{Window: s.readyWindow, Probe: s.probe, Pause: 50 * time.Millisecond, Alive: s.daemon.Alive, Read: func(ctx context.Context) error {
		_, e := s.runFor(ctx, s.probe, "status", "--json")
		return e
	}}.Wait(ctx)
	if errors.Is(e, supervise.ErrExited) {
		return ErrDaemonExited
	}
	if e != nil {
		return connector.Coded("daemon_start_timeout", "private socket unavailable")
	}
	s.mu.Lock()
	s.ready = true
	s.mu.Unlock()
	return nil
}

// WaitReady lets a restore wait for the daemon without the provider lock.
func (s *tailscaleExecSession) WaitReady(ctx context.Context) error { return s.awaitSocket(ctx) }

// Alive reports whether the daemon process still runs.
func (s *tailscaleExecSession) Alive() bool {
	return (s.storage == nil || !s.storage.failed.Load()) && s.daemon.Alive()
}

// KeepWaiting marks a daemon restored from the saved identity.
func (s *tailscaleExecSession) KeepWaiting() { s.mu.Lock(); s.restored = true; s.mu.Unlock() }

// Configure serves exactly the given private App routes: each a Unix target
// behind HTTPS on the MagicDNS name. Nothing else needs Serve, since userspace
// tailscaled forwards connections to its own address to loopback.
func (s *tailscaleExecSession) Configure(ctx context.Context, mappings []PortMapping) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.configure(ctx, mappings)
}
func (s *tailscaleExecSession) configure(ctx context.Context, mappings []PortMapping) error {
	if e := ctx.Err(); e != nil {
		return e
	}
	if e := validServeRoutes(mappings, false); e != nil {
		return e
	}
	if len(mappings) > 0 {
		raw, e := s.run(ctx, "status", "--json")
		if e != nil {
			return e
		}
		var status struct {
			CertDomains []string
			Self        struct{ DNSName string }
		}
		if json.Unmarshal(raw, &status) != nil {
			return ErrUnavailable
		}
		enabled := false
		for _, domain := range status.CertDomains {
			if domain == strings.TrimSuffix(status.Self.DNSName, ".") {
				enabled = true
			}
		}
		if !enabled {
			// Withdrawal precedes refusal, including an already configured route.
			if e := s.withdrawOwnedRoutes(); e != nil {
				return e
			}
			return connector.Coded("serve_prerequisite_missing", "enable tailnet HTTPS certificates")
		}
		name := strings.TrimSuffix(status.Self.DNSName, ".")
		if !dnsName.MatchString(name) || len(name) > 253 {
			return ErrUnavailable
		}
		if s.targets != nil {
			var e error
			mappings, e = s.targets.ResolvePrivateTargets(ctx, append([]PortMapping(nil), mappings...), name)
			if e != nil {
				if stop := s.withdrawOwnedRoutes(); stop != nil {
					return stop
				}
				return e
			}
		}
	}
	for _, m := range mappings {
		if m.Protocol == "unix" && (!unixServeVersion(s.version) || !validProxySocket(m.Socket)) {
			return connector.Coded("tailscale_update_required", "Unix targets require Tailscale 1.104.1 or later and a private proxy socket")
		}
	}
	if e := validServeRoutes(mappings, true); e != nil {
		return e
	}
	if slices.Equal(s.mappings, mappings) && len(s.forwards) == len(mappings) {
		alive := true
		for _, p := range s.forwards {
			alive = alive && p.Alive()
		}
		if alive {
			return nil
		}
	}
	kept := make([]connector.Process, 0, len(mappings))
	added := []connector.Process{}
	cleanup := func() {
		for _, p := range added {
			_ = p.Stop()
		}
	}
	// Validate every conflicting replacement before starting anything. Native
	// foreground Serve processes let independent ports be added incrementally.
	for _, m := range mappings {
		for _, old := range s.mappings {
			if old.PublicPort == m.PublicPort && old != m {
				return connector.Coded("apply_unavailable", "this port cannot be replaced without interrupting its route")
			}
		}
	}
	for _, m := range mappings {
		var reused connector.Process
		for i, old := range s.mappings {
			if old == m && i < len(s.forwards) && s.forwards[i].Alive() {
				reused = s.forwards[i]
				break
			}
		}
		if reused != nil {
			kept = append(kept, reused)
			continue
		}
		p, err := s.commands.Start(s.cli, "--socket="+s.socket, "serve", "--yes", "--https="+strconv.Itoa(m.PublicPort), mappingTarget(m))
		if err != nil {
			cleanup()
			return reason("socket not ready before the deadline")
		}
		added = append(added, p)
		kept = append(kept, p)
	}
	for _, old := range s.forwards {
		if !slices.Contains(kept, old) {
			if e := old.Stop(); e != nil {
				cleanup()
				return errors.New("tailscale forwarding cleanup required")
			}
		}
	}
	s.forwards = kept
	s.mappings = append([]PortMapping(nil), mappings...)
	return nil
}

// validServeRoutes accepts at most sixteen private App routes on distinct
// ports: before resolution an App's HTTPS port or its proxy socket, after it
// only the proxy socket.
func validServeRoutes(mappings []PortMapping, resolved bool) error {
	if len(mappings) > 16 {
		return errors.New("too many private App routes")
	}
	ports := map[int]bool{}
	for _, m := range mappings {
		ok := m.Protocol == "unix" && validProxySocket(m.Socket) || !resolved && m.Protocol == "https" && m.Socket == ""
		if !ok || m.LocalPort < 1 || m.LocalPort > 65535 || m.PublicPort < 1 || m.PublicPort > 65535 || ports[m.PublicPort] {
			return errors.New("invalid or duplicate private App route")
		}
		ports[m.PublicPort] = true
	}
	return nil
}

func (s *tailscaleExecSession) withdrawOwnedRoutes() error {
	var failed error
	for _, p := range s.forwards {
		failed = errors.Join(failed, p.Stop())
	}
	if failed == nil {
		s.forwards, s.mappings = nil, nil
	}
	return failed
}

var proxySocketID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}\.sock$`)

func validProxySocket(path string) bool {
	return filepath.IsAbs(path) && filepath.Clean(path) == path && filepath.Dir(path) == localipc.RunDir && proxySocketID.MatchString(filepath.Base(path))
}
func unixServeVersion(version string) bool {
	base, suffix, _ := strings.Cut(strings.TrimPrefix(version, "v"), "-")
	parts := strings.Split(base, ".")
	if len(parts) != 3 {
		return false
	}
	var nums [3]int
	for i, p := range parts {
		n, e := strconv.Atoi(p)
		if e != nil || n < 0 {
			return false
		}
		nums[i] = n
	}
	for i, n := range [3]int{1, 104, 1} {
		if nums[i] != n {
			return nums[i] > n
		}
	}
	return suffix == ""
}
func mappingTarget(m PortMapping) string {
	if m.Protocol == "unix" {
		return "unix:" + m.Socket
	}
	return m.Protocol + "://127.0.0.1:" + strconv.Itoa(m.LocalPort)
}

// deadlineError runs with s.mu held.
func (s *tailscaleExecSession) deadlineError() error {
	if s.restored && (s.phase == "url" || s.phase == "network") {
		return nil
	}
	if s.phase != "" && !s.now().Before(s.deadline) {
		code := "login_wait_timeout"
		if s.phase == "url" {
			code = "control_unreachable"
		}
		if s.phase == "network" {
			code = "network_connect_timeout"
		}
		if s.phase == "approval" {
			code = "machine_approval_wait_timeout"
		}
		_ = s.stop()
		return connector.Coded(code, "App wait deadline exceeded")
	}
	return nil
}
func (s *tailscaleExecSession) Inspect(ctx context.Context) (TailscaleSnapshot, error) {
	s.mu.Lock()
	e := s.deadlineError()
	s.mu.Unlock()
	if e != nil {
		return TailscaleSnapshot{}, e
	}
	if !s.daemon.Alive() {
		return TailscaleSnapshot{}, ErrDaemonExited
	}
	raw, err := s.run(ctx, "status", "--json")
	if err != nil {
		return TailscaleSnapshot{}, err
	}
	s.mu.Lock()
	out, mappings, forwards, err := s.observeStatus(raw)
	s.mu.Unlock()
	if err != nil || len(mappings) == 0 {
		// No private App: there is no route to read back.
		out.Serving = err == nil
		return out, err
	}
	for _, p := range forwards {
		if !p.Alive() {
			return out, connector.Coded("feature_approval_required", "Serve command exited before readiness")
		}
	}
	raw, err = s.run(ctx, "serve", "status", "--json")
	if err != nil {
		return out, err
	}
	out.Serving, err = checkServeMode(raw, strings.TrimSuffix(out.DNSName, "."), mappings)
	return out, err
}

// observeStatus parses one status read and advances the login phase; it runs
// with s.mu held and returns the owned routes to check without it.
func (s *tailscaleExecSession) observeStatus(raw []byte) (out TailscaleSnapshot, mappings []PortMapping, forwards []connector.Process, err error) {
	var status struct {
		BackendState, AuthURL string
		CertDomains           []string
		TailscaleIPs          []string
		Self                  struct {
			DNSName      string
			TailscaleIPs []string
		}
		CurrentTailnet struct{ Name string }
		Peer           json.RawMessage
	}
	if json.Unmarshal(raw, &status) != nil {
		return TailscaleSnapshot{}, nil, nil, ErrUnavailable
	}
	out = TailscaleSnapshot{Version: s.version, BackendState: status.BackendState, AuthURL: status.AuthURL, DNSName: status.Self.DNSName, Tailnet: status.CurrentTailnet.Name, IPs: status.TailscaleIPs, CertDomains: status.CertDomains}
	if len(out.IPs) == 0 {
		out.IPs = status.Self.TailscaleIPs
	}
	out.PeersOnline = onlineTailscalePeers(status.Peer)

	if s.phase != "" {
		switch status.BackendState {
		case "Running":
			if e := s.finishLogin(); e != nil {
				return out, nil, nil, e
			}
			s.phase = ""
		case "NeedsLogin":
			if status.AuthURL != "" {
				if trustedTailscaleURL(status.AuthURL, s.server, s.origin) == "" {
					return out, nil, nil, connector.Coded("login_url_rejected", "untrusted challenge")
				}
				if s.phase == "url" {
					s.phase = "login"
					s.deadline = s.now().Add(10 * time.Minute)
				}
			} else {
				out.Code = "control_unreachable"
			}
		case "NeedsMachineAuth":
			if s.phase == "url" {
				s.deadline = s.now().Add(10 * time.Minute)
			}
			s.phase = "approval"
		case "Starting":
			if s.phase == "login" || s.phase == "approval" {
				s.phase = "network"
				s.deadline = s.now().Add(60 * time.Second)
			}
		}
		if e := s.deadlineError(); e != nil {
			return out, nil, nil, e
		}
		if s.phase == "login" || s.phase == "approval" {
			out.LoginDeadline = s.deadline.UTC().Format(time.RFC3339)
		}
		if s.login != nil && !s.login.Alive() && status.BackendState != "Running" && status.BackendState != "NeedsMachineAuth" {
			return out, nil, nil, connector.Coded("login_denied", "login command exited before completion")
		}
	}
	return out, append([]PortMapping(nil), s.mappings...), append([]connector.Process(nil), s.forwards...), nil
}
func checkServeMode(raw []byte, name string, mappings []PortMapping) (bool, error) {
	// Require the complete observed configuration, including foreground sessions,
	// to contain exactly the approved HTTPS routes and nothing public.
	type handler struct {
		Proxy string
		Path  string
		Text  string
	}
	type web struct{ Handlers map[string]handler }
	type tcp struct {
		HTTPS        bool
		HTTP         bool
		TCPForward   string
		TerminateTLS string
	}
	type config struct {
		TCP         map[string]tcp
		Web         map[string]web
		AllowFunnel map[string]bool
		Foreground  map[string]json.RawMessage
	}
	var root config
	decode := func(raw []byte, c *config) error {
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		if err := d.Decode(c); err != nil {
			return err
		}
		if d.Decode(new(any)) != io.EOF {
			return ErrUnavailable
		}
		return nil
	}
	if decode(raw, &root) != nil || len(mappings) == 0 {
		return false, ErrUnavailable
	}
	configs := []config{root}
	for _, v := range root.Foreground {
		var c config
		if decode(v, &c) != nil || len(c.Foreground) != 0 {
			return false, ErrUnavailable
		}
		configs = append(configs, c)
	}
	desired := map[string]PortMapping{}
	for _, m := range mappings {
		if _, exists := desired[strconv.Itoa(m.PublicPort)]; exists {
			return false, ErrUnavailable
		}
		desired[strconv.Itoa(m.PublicPort)] = m
	}
	found := map[string]bool{}
	for _, c := range configs {
		if len(c.AllowFunnel) != 0 {
			return false, ErrUnavailable
		}
		for port, tcp := range c.TCP {
			m, ok := desired[port]
			if !ok || found[port] || tcp.TerminateTLS != "" || !tcp.HTTPS || tcp.HTTP || tcp.TCPForward != "" {
				return false, ErrUnavailable
			}
			handlers := c.Web[name+":"+port].Handlers
			h := handlers["/"]
			if len(handlers) != 1 || h.Proxy != mappingTarget(m) || h.Path != "" || h.Text != "" {
				return false, ErrUnavailable
			}
			found[port] = true
		}
		for host := range c.Web {
			port := strings.TrimPrefix(host, name+":")
			if _, ok := c.TCP[port]; !ok || host != name+":"+port {
				return false, ErrUnavailable
			}
		}
	}
	return len(found) == len(mappings), nil
}

// Stop signals every owned child at once, so slow exits overlap instead of
// adding up. The daemon stops even if a forwarding command fails to stop.
func (s *tailscaleExecSession) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stop()
}
func (s *tailscaleExecSession) stop() error {
	s.storage.Close()
	steps := []func() error{s.daemon.Stop, s.finishLogin}
	for _, p := range s.forwards {
		steps = append(steps, p.Stop)
	}
	if concurrently(steps...) != nil {
		return errors.New("tailscale cleanup required")
	}
	s.forwards = nil
	if err := os.Remove(s.socket); err != nil && !os.IsNotExist(err) {
		return errors.New("tailscale socket cleanup required")
	}
	return transient.Clear(s.dir, "tailscale")
}

// OSCommands never logs process output, and bounds captured JSON/status output.
type OSCommands struct {
	Dir        string
	Env        []string
	LimitFiles bool
}

func (c OSCommands) command(binary string, args ...string) *exec.Cmd {
	cmd := exec.Command(binary, args...)
	if c.LimitFiles {
		// Production calls run in connect-worker. Keep exec failures within
		// the ordinary supervised child lifecycle.
		self, err := os.Executable()
		if err != nil {
			self = "/nonexistent/connect-worker"
		}
		cmd = exec.Command(self, append([]string{"provider-child", binary}, args...)...)
	}
	cmd.Dir = c.Dir
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + c.Dir, "TS_NO_LOGS_NO_SUPPORT=true"}
	cmd.Env = append(cmd.Env, c.Env...)
	cmd.Stderr = io.Discard
	return cmd
}
func (c OSCommands) Start(binary string, args ...string) (connector.Process, error) {
	cmd := c.command(binary, args...)
	cmd.Stdout = io.Discard
	return connector.StartOwned(cmd)
}

type limitedOutput struct{ bytes.Buffer }

func (b *limitedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 65536 {
		return 0, errors.New("output limit")
	}
	return b.Buffer.Write(p)
}
func (c OSCommands) Run(ctx context.Context, binary string, args ...string) ([]byte, error) {
	cmd := c.command(binary, args...)
	out := &limitedOutput{}
	cmd.Stdout = out
	p, err := connector.StartOwned(cmd)
	if err != nil {
		return nil, ErrUnavailable
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for p.Alive() {
		select {
		case <-ctx.Done():
			_ = p.Stop()
			return nil, ErrUnavailable
		case <-ticker.C:
		}
	}
	// StartOwned's Wait completion synchronizes stdout writes. Exit success must
	// be checked explicitly; raw stderr/error details never cross the boundary.
	if cmd.ProcessState == nil || !cmd.ProcessState.Success() {
		return nil, ErrUnavailable
	}
	return out.Bytes(), nil
}

// onlineTailscalePeers counts peers the daemon reports online. A status without
// a Peer field yields nil, so no count is guessed; null means no peers.
func onlineTailscalePeers(raw json.RawMessage) *int {
	if len(raw) == 0 {
		return nil
	}
	var peers map[string]struct{ Online bool }
	if json.Unmarshal(raw, &peers) != nil {
		return nil
	}
	online := 0
	for _, peer := range peers {
		if peer.Online {
			online++
		}
	}
	return &online
}

func (d TailscaleExec) Dependency(ctx context.Context, s Settings) error {
	if d.Commands != nil {
		return nil
	}
	host := "controlplane.tailscale.com"
	raw := s.LoginServer

	if raw != "" {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			return connector.Coded("invalid_config", "invalid management address")
		}
		host = u.Hostname()
	}
	return supervision.NetworkReady(ctx, host)
}

func (s *tailscaleExecSession) LastExit() supervision.Exit {
	if s.storage != nil && s.storage.failed.Load() {
		return supervision.Exit{Code: -1, Reason: s.storage.failureReason(), Permanent: true}
	}
	if p, ok := s.daemon.(interface{ Exit() supervision.Exit }); ok && !s.daemon.Alive() {
		return p.Exit()
	}
	return supervision.Exit{Code: -1, Reason: "daemon_exit"}
}

func tailscaleRuntimeCommands(dir string) (OSCommands, error) {
	if err := transient.Clear(dir, "tailscale"); err != nil {
		return OSCommands{}, err
	}
	runtimeDir, err := transient.For(dir, "tailscale", true)
	if err != nil {
		return OSCommands{}, err
	}
	return OSCommands{Dir: dir, LimitFiles: true, Env: []string{"TS_LOGS_DIR=" + runtimeDir, "TMPDIR=" + runtimeDir, "TS_DISABLE_TAILDROP=true", "TS_USE_CACHED_NETMAP=false"}}, nil
}
