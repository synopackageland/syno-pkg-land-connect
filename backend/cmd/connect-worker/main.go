// Command connect-worker owns the control socket and enabled provider runtimes.
package main

import (
	"context"
	"dsmapp/internal/accounttoken"
	"dsmapp/internal/app"
	"dsmapp/internal/appaccess"
	"dsmapp/internal/apps"
	"dsmapp/internal/binaries"
	"dsmapp/internal/cfaccess"
	"dsmapp/internal/cloudflare"
	"dsmapp/internal/connector"
	"dsmapp/internal/control"
	"dsmapp/internal/daemon"
	"dsmapp/internal/dependency"
	"dsmapp/internal/githubtoken"
	"dsmapp/internal/ingress"
	"dsmapp/internal/lanswitch"
	"dsmapp/internal/nbserver"
	"dsmapp/internal/ports"
	"dsmapp/internal/provider"
	"dsmapp/internal/relayworker"
	"dsmapp/internal/signalbridge"
	"dsmapp/internal/transient"
	"dsmapp/internal/workers"
	"errors"
	"fmt"
	"github.com/synopackageland/syno-pkg-land-sdk/dsmservices"
	"github.com/synopackageland/syno-pkg-land-sdk/supervision"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) >= 3 && os.Args[1] == "provider-child" {
		if provider.ExecLimited(os.Args[2:]) != nil {
			os.Exit(1)
		}
		return
	}
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "SynoPkgConnect: package service unavailable")
		os.Exit(1)
	}
}
func run() (runErr error) {
	if len(os.Args) != 4 {
		return fmt.Errorf("usage: connectd start|serve|stop|status target var")
	}
	action, target, dir := os.Args[1], os.Args[2], filepath.Join(os.Args[3], "connect")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	if e := os.Chmod(dir, 0700); e != nil {
		return e
	}
	socket := control.Socket
	client := connector.Client{Socket: socket}
	ctx := context.Background()
	switch action {
	case "status":
		_, e := client.Call(ctx, "ping", nil)
		return e
	case "revoke":
		return revoke(ctx, client, dir, os.Args[3])
	case "retain":
		return uninstallRetainingData(client, dir, os.Args[3])
	case "stop":
		if _, e := os.Lstat(socket); os.IsNotExist(e) {
			return nil
		}
		_, e := client.Call(ctx, "shutdown", nil)
		if e != nil {
			return e
		}
		for i := 0; i < 60; i++ {
			if _, e = os.Lstat(socket); os.IsNotExist(e) {
				return nil
			}
			time.Sleep(100 * time.Millisecond)
		}
		return fmt.Errorf("service shutdown incomplete")
	case "start":
		if _, e := client.Call(ctx, "ping", nil); e == nil {
			return nil
		}
		bin, e := os.Executable()
		if e != nil {
			return e
		}
		cmd := exec.Command(filepath.Join(filepath.Dir(bin), "connectd"), "serve", target, os.Args[3])
		cmd.Env = []string{"PATH=/usr/bin:/bin"}
		if e = cmd.Start(); e != nil {
			return e
		}
		cmd.Process.Release()
		for i := 0; i < 60; i++ {
			if _, e = client.Call(ctx, "ping", nil); e == nil {
				return nil
			}
			time.Sleep(100 * time.Millisecond)
		}
		return fmt.Errorf("startup incomplete")
	case "work":
		connector.SetLog(os.Stderr)
	default:
		return fmt.Errorf("unknown action")
	}
	// The serving parent passes its held daemon lock as fd 3 and a pipe as fd 4.
	lock, parent := os.NewFile(3, "daemon.lock"), os.NewFile(4, "parent")
	if lock == nil || parent == nil {
		return fmt.Errorf("missing daemon lock")
	}
	go exitWithParent(parent)
	defer lock.Close()
	if e := daemon.HeldLock(lock, dir); e != nil {
		return e
	}
	_ = os.Remove(filepath.Join(dir, daemon.StoppedMarker))
	binaryStore := binaries.New(filepath.Join(os.Args[3], "binaries"), binaries.NewOfficialSource().UseToken(githubtoken.Store{Dir: os.Args[3]}.Load))
	binaryRuntime := binaries.NewRuntimeAdapter(binaryStore)
	approvalStore := apps.Store{Dir: os.Args[3]}
	dsmHTTPSPort := func(ctx context.Context) (int, error) {
		p, e := ports.DatastoreDetector{}.Detect(ctx)
		return p.HTTPS, e
	}
	recoveryChanges := make(chan struct{}, 1)
	recoveryChanged := func() {
		select {
		case recoveryChanges <- struct{}{}:
		default:
		}
	}
	var appOwner *appaccess.Engine
	var accessOwner *cfaccess.Manager
	var accessMonitor *connector.Demand
	// The self-hosted NetBird server is served on the own domain while it is on.
	var nbServer *nbserver.Server
	var serverDependencies dependency.Model
	cf := &provider.CloudflareTunnel{Tunnel: cloudflare.NewTunnel(cloudflare.Config{
		Changed: recoveryChanged,
		Dependency: func(ctx context.Context) error {
			if err := supervision.NetworkReady(ctx, "region1.v2.argotunnel.com"); err != nil {
				return err
			}
			portsView, err := (ports.DatastoreDetector{}).Detect(ctx)
			if err != nil {
				return err
			}
			port := portsView.HTTPS
			conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
			if err != nil {
				return err
			}
			conn.Close()
			return nil
		},
		CheckRemoval: serverDependencies.Guard,
		Unrestricted: func(run func() error) error {
			if accessOwner == nil {
				return run()
			}
			return accessOwner.Unrestricted(run)
		},
		CheckAccessMode: func(mode string) error {
			if accessOwner != nil && accessOwner.Enabled() && mode != "domain" {
				return connector.Coded("access_alias_unsupported", "email restrictions do not support this public connection")
			}
			return nil
		},
		BeforeRoute: func(ctx context.Context, account, host string) error {
			if accessOwner == nil {
				return nil
			}
			return accessOwner.PrepareAccount(ctx, account, host)
		},
		AccessProtected: func() bool { return accessOwner != nil && accessOwner.Enabled() },
		NetBirdServer:   func() (cloudflare.ServerOrigin, bool) { return nbServer.Origin() },
		NameDir:         os.Args[3],
		DSMTarget: func(ctx context.Context, authority string) (string, error) {
			if accessOwner != nil && accessOwner.Enabled() {
				if err := accessOwner.Prepare(ctx, []string{authority}); err != nil {
					return "", err
				}
			}
			if err := appOwner.BindTrustedDSM(ctx, apps.Public, authority); err != nil {
				return "", err
			}
			return cloudflare.ApprovedDSMTarget(approvalStore, dsmHTTPSPort)(ctx, authority)
		},
		IngressOnly: func() bool { return appOwner != nil && appOwner.IngressOnly() },
		Dir:         filepath.Join(dir, "cloudflare"),
		Cloudflared: func(ctx context.Context) (string, error) {
			b, e := binaryRuntime.Resolve(ctx, "cloudflared")
			return b.Path, e
		},
		DSMPort: func(ctx context.Context) (int, error) {
			p, e := ports.DatastoreDetector{}.Detect(ctx)
			return p.HTTPS, e
		},
	})}
	tsDir := filepath.Join(dir, "tailscale")
	ts := provider.NewTailscale(tsDir, provider.TailscaleExec{Dir: tsDir, Binaries: binaryRuntime, Targets: provider.ApprovedPrivateTargets{Store: approvalStore, DSMPort: dsmHTTPSPort, EnsureDSM: func(ctx context.Context, kind, authority string) error {
		return appOwner.BindTrustedDSM(ctx, kind, authority)
	}}})
	ts.HTTPPort = func(ctx context.Context) (int, error) {
		v, e := (ports.DatastoreDetector{}).Detect(ctx)
		return v.HTTP, e
	}
	nbDir := filepath.Join(dir, "netbird")
	nb := provider.NewNetBird(nbDir, provider.NetBirdExec{Dir: nbDir, Binaries: binaryRuntime})
	nb.HTTPPort = func(ctx context.Context) (int, error) {
		v, e := (ports.DatastoreDetector{}).Detect(ctx)
		return v.HTTP, e
	}
	ts.OnSupervisionChange = recoveryChanged
	nb.OnSupervisionChange = recoveryChanged
	selector := provider.NewMultiSelection(dir, cf, ts, provider.NewIPv6Runtime(dir, nil), nb)
	var m *managedSelection
	var binaryService *binaries.Service
	// The Workers token also deploys the server's Signal bridge; the manager
	// is made below, after the providers.
	var workerOwner *workers.Manager
	nbServer = nbserver.New(nbserver.Deps{
		Dir: filepath.Join(dir, "netbird-server"),
		Install: func(ctx context.Context) error {
			return installBinary(ctx, binaryService, binaryRuntime, "netbird-server")
		},
		Binary: func(ctx context.Context) (string, string, error) {
			b, e := binaryRuntime.Resolve(ctx, "netbird-server")
			return b.Path, b.Version, e
		},
		Preflight: func(ctx context.Context) error {
			serverDependencies.Invalidate()
			_, e := serverDependencies.Preflight(ctx)
			return e
		},
		Dependencies: serverDependencies.Observe,
		Zone:         func() (string, error) { return serverDependencies.Preflight(context.Background()) },
		Name:         cf.Tunnel.ServerHostname,
		Route:        cf.Tunnel.RouteServer, Unroute: cf.Tunnel.UnrouteServer,

		Bridge: func(ctx context.Context, zone, host string) error {
			e := workerOwner.EnsureSignalBridge(ctx, zone, host)
			if e == nil {
				serverDependencies.Invalidate()
			}
			return e
		},
		Unbridge: func(ctx context.Context) error { return workerOwner.RemoveSignalBridge(ctx) },
		Check:    nbserver.GRPCCheck{}.Check,
		Join: func(ctx context.Context, url, key string) error {
			if e := installBinary(ctx, binaryService, binaryRuntime, "netbird"); e != nil {
				return e
			}
			return joinServer(ctx, m, selector.StatusFor, url, key)
		},
		Member: func(url string) bool {
			s := selector.StatusFor("netbird")
			return s.State == "connected" && s.ManagementURL == url
		},
		Leave: func(_ context.Context, url string) error {
			return leaveServer(m, selector.StatusFor, url)
		},
	})
	binaryService = binaries.NewService(binaryStore, binaries.WithServer(binaries.RuntimeInstalledHook(selector, binaryRuntime), "netbird-server", nbServer, binaryRuntime))
	binaryService.CurrentVersion = binaryRuntime.SelectedVersion
	// Connect starts detached from the CGI request; see connector.Async.
	// Cloudflare and NetBird check their own reach; the others read the
	// selector's own status until they add a probe.
	probe := cloudflare.Probe{}.For(selector.StatusFor, connector.StatusProbe(selector.StatusFor))
	// The Free link is checked through public DSM on it, once the engine runs.
	freeLink := cloudflare.Probe{}.Workers(func() string {
		if appOwner == nil {
			return ""
		}
		return appOwner.WorkersDSMURL()
	})
	checker := connector.NewChecker(func(ctx context.Context, id string) (bool, error) {
		if id == "cloudflare" && accessOwner != nil && accessOwner.Enabled() {
			return false, connector.Coded("access_login_required", "email sign-in required; origin not externally verified")
		}
		if id == "workers" {
			return freeLink(ctx, id)
		}
		if id == "netbird" {
			return nb.Probe(ctx)
		}
		if id == "tailscale" {
			return ts.Probe(ctx)
		}
		return probe(ctx, id)
	})
	defer checker.Close()
	// App bindings register their per-scope proxies with this registry. Safety
	// checks run without a UI request and never grant or restore an approval.
	pinProxies := &apps.PinRegistry{}
	defer pinProxies.Close()
	m = &managedSelection{lifecycleSelection: connector.NewAsync(selector), binaries: binaryService, checker: checker, pinProxies: pinProxies}
	defer func() {
		e := m.Stop()
		if e == nil {
			e = clearTransient(dir, os.Args[3])
		}
		runErr = errors.Join(runErr, e)
	}()
	// The server stops first: its setup job may still be using m.
	defer nbServer.Close()
	// Track trusted DSM/custom settings through the same selecting owner as
	// connect/disconnect and binary updates.
	portStore := ports.Store{Dir: os.Args[3]}
	selector.SetPortSource(ports.ResolvedSource{Store: portStore, Detector: ports.DatastoreDetector{}})
	// Port tracking, health and swap sampling serve enabled connections only;
	// with none on, connectd keeps no loop or ticker for them.
	pressure := provider.NewSwapPressure()
	selector.SetPressure(pressure.High)
	networkEvents := &connector.Demand{Active: selector.Enabled, Loop: func(ctx context.Context) {
		_ = supervision.WatchNetwork(ctx, func() { selector.DependencyChanged(ctx) })
	}}
	defer networkEvents.Close()
	connections := &connector.Demand{Active: selector.NeedsPolling, Loop: func(ctx context.Context) {
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); pressure.Run(ctx, 10*time.Second) }()
		go func() { defer wg.Done(); selector.WatchHealth(ctx, 30*time.Second) }()
		monitor := ports.Reconciler{
			Store: portStore, Detector: ports.DatastoreDetector{},
			Applier: ports.SelectingApplier{Selection: selector}, Debounce: 250 * time.Millisecond,
			OnStatus: func(view ports.Snapshot, _ error) {
				bounded, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				_ = portStore.RecordObservation(bounded, view)
			},
		}
		_ = monitor.Run(ctx, 5*time.Second, nil)
		wg.Wait()
		if errors.Is(context.Cause(ctx), connector.ErrIdle) {
			_ = portStore.ClearObservation()
		}
	}}
	done := make(chan struct{}, 1)
	shutdown := func() {
		select {
		case done <- struct{}{}:
		default:
		}
	}
	appDiscovery := apps.Discovery{DetectPorts: func(ctx context.Context) (int, int, error) {
		p, err := (ports.DatastoreDetector{}).Detect(ctx)
		return p.HTTP, p.HTTPS, err
	}, Known: &apps.KnownPorts{}}
	workerOwner = &workers.Manager{Store: workers.Store{Dir: filepath.Join(dir, "workers"), Credentials: &accounttoken.Store{Path: filepath.Join(dir, "account-token", "credential.json")}}, API: workers.API{Script: []byte(relayworker.Script()), Bridge: []byte(signalbridge.Script())}}
	defer workerOwner.Close()
	accessOwner = cfaccess.New(cfaccess.Config{Context: ctx, Activity: func() {
		if accessMonitor != nil {
			accessMonitor.Update()
		}
	}, Reconciled: func(ctx context.Context) {
		if appOwner != nil {
			_ = appOwner.Check(ctx)
		}
	}, Path: filepath.Join(dir, "access", "state.json"), Credential: workerOwner.AccountCredential, Endpoints: func() (cfaccess.Inventory, error) {
		out, e := appOwner.AccessInventory()
		if e != nil {
			return out, e
		}
		account, hosts, unsupported, e := cf.Tunnel.AccessInventory()
		out.Account = account
		out.DSMHost = cf.Tunnel.AccessDSMHostname()
		out.Hosts = append(out.Hosts, hosts...)
		out.Unsupported = out.Unsupported || unsupported
		return out, e
	}, Changed: func() {
		if appOwner != nil {
			appOwner.StopPublicStreams()
		}
	}})
	defer accessOwner.Close()
	workerOwner.ValidateAccountToken = accessOwner.CheckCredential
	workerOwner.KeepAccountToken = accessOwner.InUse
	m.beforeDisconnect = func(id string) error {
		if id == "" || id == "cloudflare" {
			return serverDependencies.Guard(dependency.Tunnel)
		}
		return nil
	}
	serverDependencies.Enabled = nbServer.Enabled
	serverDependencies.Cloudflare = func() dependency.Cloudflare {
		st := cf.Status()
		zone, e := cf.Tunnel.ServerZone()
		code := connector.ErrorCode(e)
		if code == "" && e != nil {
			code = "cloudflare_reconnect_required"
		}
		return dependency.Cloudflare{State: st.State, Mode: st.Mode, Zone: zone, Code: code}
	}
	serverDependencies.Token = func(ctx context.Context, zone string) error {
		st, e := workerOwner.Status(ctx)
		if e != nil {
			return e
		}
		if !st.Token {
			code := "workers_not_connected"
			if st.Invalid {
				code = "workers_token_invalid"
			}
			return connector.Coded(code, "the Signal bridge token needs attention")
		}
		return workerOwner.CheckSignalBridge(ctx, zone)
	}
	serverDependencies.Bridge = workerOwner.ObserveSignalBridge
	serverDependencies.Signal = (nbserver.GRPCCheck{}).Check
	workerOwner.CheckBridgeRemoval = func() error { return serverDependencies.Guard(dependency.Bridge) }
	workerOwner.KeepSignalBridge = func() bool { return serverDependencies.Guard(dependency.Bridge) != nil }
	appOwner = appaccess.New(ctx, appaccess.Config{
		IngressConnector: cf.Status,
		Store:            approvalStore, Discovery: appDiscovery, Workers: workerOwner, Access: accessOwner,
		ProjectionPath: filepath.Join(os.Args[3], "spl", "external.json"),
		DSMPort:        dsmHTTPSPort, Private: ts, Public: cf.Tunnel, Registry: pinProxies,
		// Custom Apps dial only vetted addresses, never Connect's own ports, and
		// check visitors against DSM's Auth API on loopback.
		Guard: apps.TargetGuard{OwnPorts: apps.OwnListeningPorts},
		Login: apps.DSMLogin{Endpoint: func(ctx context.Context) (string, error) {
			p, e := ports.DatastoreDetector{}.Detect(ctx)
			return "http://127.0.0.1:" + strconv.Itoa(p.HTTP) + "/webapi/entry.cgi", e
		}}.Login,
		Services: func(context.Context) []dsmservices.Definition { return dsmservices.Read(dsmservices.Dirs...) },
		// A new Free link address for DSM settles like a new own domain.
		WorkersAddress: func() { checker.Reset("workers") },
		Verifier: apps.NewVerifier(apps.Options{ReferencePort: func() (int, error) {
			bounded, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			return dsmHTTPSPort(bounded)
		}}),
	})
	defer appOwner.Close()
	m.trustDSM = appOwner.TrustDSMFirstUse
	m.dsmDetails = func() *connector.TrustedCertificate {
		state, err := approvalStore.Load()
		if err != nil || state.DSM == nil || state.DSM.Certificate == nil {
			return nil
		}
		c := state.DSM.Certificate
		return &connector.TrustedCertificate{Issuer: c.Issuer, Expires: c.Expires, CertSHA256: c.CertSHA256}
	}
	ts.AppMappings = appOwner.PrivateMappings
	if e := cf.SetAppTargets(appOwner); e != nil {
		return e
	}
	appAccess := appOwner.Service()
	startupCtx, startupCancel := context.WithTimeout(ctx, 10*time.Second)
	_, _ = appAccess.Discovery.Scan(startupCtx)
	startupCancel()
	// App safety checks run only while an App URL or DSM approval is recorded.
	safety := &connector.Demand{Active: appOwner.InUse, Loop: func(ctx context.Context) {
		connector.RunSafetyChecks(ctx, appOwner.Check, func(e error) { connector.Logf("App access safety check unavailable: %s", apps.ErrorCode(e)) })
		if errors.Is(context.Cause(ctx), connector.ErrIdle) {
			// One last pass withdraws what the closed App left behind.
			bounded, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			_ = appOwner.Check(bounded)
			cancel()
		}
	}}
	defer safety.Close()
	accessMonitor = &connector.Demand{Active: accessOwner.InUse, Loop: accessOwner.Run}
	defer accessMonitor.Close()
	changed := func() { safety.Update(); accessMonitor.Update(); accessOwner.Trigger() }
	// Reads answer while the daemon restores; mutations wait on the barrier.
	restored := make(chan struct{})
	appOwner.SetRestoring(true)
	// The LAN switch responder runs only while its option is on.
	lanSwitch := &lanswitch.Service{Path: filepath.Join(dir, "lanswitch.json"), DSM: ports.DatastoreDetector{}.Redirect}
	lanSwitch.Restore()
	defer lanSwitch.Close()
	server, e := control.Start(ctx, control.Config{
		Apps: appAccess, Binaries: binaryService, NetBird: nbServer, LANSwitch: lanSwitch,
		Controller: m, Shutdown: shutdown, Restored: restored, AfterApps: changed,
	})
	if e != nil {
		return e
	}
	defer server.Close()
	stopMonitor := restoreAndMonitor(ctx, func(ctx context.Context) error {
		// The server's origin is published first, so the restored tunnel serves it.
		serverErr := nbServer.Restore(ctx)
		return errors.Join(serverErr, m.Restore(ctx), appAccess.Lifecycle.Recover(ctx))
	}, connections)
	defer stopMonitor()
	// Restored connections settle from here, not from when the daemon started.
	checker.Begin(time.Now())
	selector.OnChange(func() { connections.Update(); networkEvents.Update(); changed() })
	networkEvents.Update()
	changed()
	appOwner.SetRestoring(false)
	close(restored)
	ingressServer, err := ingress.Start(ctx, ingress.Socket, appAccess, changed)
	if err != nil {
		return err
	}
	defer ingressServer.Close()
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)
	for {
		select {
		case <-done:
			return nil
		case <-signals:
			return nil
		case <-recoveryChanges:
			connections.Update()
			networkEvents.Update()
		}
	}
}

// revoke disconnects through a live daemon, else persists a disabled state and
// removes credentials offline. Never restore or start the daemon for uninstall.
func revoke(ctx context.Context, client connector.Client, dir, data string) error {
	lock, e := os.OpenFile(filepath.Join(dir, "daemon.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return e
	}
	defer lock.Close()
	switch e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); {
	case e == nil:
		// No daemon owns the socket; holding the lock keeps one from starting.
		if e = removeAbandonedSocket(client.Socket); e != nil {
			return e
		}
	case errors.Is(e, syscall.EWOULDBLOCK):
		if _, e = os.Lstat(client.Socket); e == nil {
			if e = disconnectAndWait(ctx, client); e != nil {
				return e
			}
		} else if !os.IsNotExist(e) {
			return e
		}
	default:
		return e
	}
	// A reinstall that keeps data starts with everything off: nothing reaches
	// the NAS from outside until an administrator turns it on again.
	e = errors.Join(
		provider.RevokeOffline(dir),
		nbserver.DisableOffline(filepath.Join(dir, "netbird-server")),
		workers.DisableOffline(filepath.Join(dir, "workers")),
		clearTransient(dir, data),
	)
	if e != nil {
		return e
	}
	return app.RevokeLegacySimulation(data)
}

// C1 stops the package before invoking its uninstall action. Take its worker
// lock and refuse a live worker rather than turning retained connections off.
func uninstallRetainingData(client connector.Client, dir, data string) error {
	lock, err := daemon.Lock(dir)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = removeAbandonedSocket(client.Socket); err != nil {
		return err
	}
	if err = provider.RetainOffline(dir); err != nil {
		return err
	}
	return errors.Join(
		nbserver.DisableOffline(filepath.Join(dir, "netbird-server")),
		workers.DisableOffline(filepath.Join(dir, "workers")),
		app.RevokeLegacySimulation(data),
		clearTransient(dir, data),
	)
}

func clearTransient(dir, data string) error {
	return errors.Join(
		transient.Clear(filepath.Join(dir, "cloudflare"), "cloudflare"),
		transient.Clear(filepath.Join(dir, "tailscale"), "tailscale"),
		transient.Clear(filepath.Join(dir, "netbird", "client"), "netbird"),
		transient.Clear(data, "ports"),
	)
}

// revokePoll paces the repeated disconnect while a live daemon still stops.
var revokePoll = time.Second

// disconnectAndWait repeats the disconnect, which joins the pending one, until
// the daemon reports every connection stopped. The stop itself is bounded.
func disconnectAndWait(ctx context.Context, client connector.Client) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	for {
		s, e := client.Call(ctx, "disconnect", nil)
		if e != nil {
			return e
		}
		if !s.Stopping() {
			return nil
		}
		select {
		case <-ctx.Done():
			return errors.New("connections did not stop")
		case <-time.After(revokePoll):
		}
	}
}

// A daemon killed before its deferred cleanup leaves its socket behind.
// Anything other than a socket is refused rather than removed.
func removeAbandonedSocket(path string) error {
	info, e := os.Lstat(path)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("unexpected control socket")
	}
	return os.Remove(path)
}

// Restore owns the selector until persisted connection intent has been resolved.
// Starting reconciliation earlier can consume its apply deadline while blocked
// on that lock. The returned barrier also prevents a late monitor apply racing
// provider shutdown and reopening an owned daemon. The monitor starts only if
// a restored connection is enabled.
func restoreAndMonitor(ctx context.Context, restore func(context.Context) error, monitor *connector.Demand) func() {
	// Provider failures remain visible in status; never enable them.
	if e := restore(ctx); e != nil {
		connector.Logf("restore: %v", e)
	}
	monitor.Update()
	return monitor.Close
}

// exitWithParent ends a worker whose serving parent was killed, as a kill
// would: no cleanup, so the lock frees at once and the socket stays.
func exitWithParent(parent *os.File) {
	_, _ = io.Copy(io.Discard, parent)
	os.Exit(1)
}
