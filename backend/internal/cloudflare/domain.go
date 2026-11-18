package cloudflare

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"dsmapp/internal/connector"
	"dsmapp/internal/dependency"
	"github.com/synopackageland/syno-pkg-land-sdk/supervision"
)

var (
	errSubdomain     = connector.Coded("cloudflare_invalid_subdomain", "the name must be one label such as nas")
	errNoDomain      = connector.Coded("cloudflare_no_saved_domain", "no own domain is set up yet")
	errTunnelMissing = connector.Coded("cloudflare_tunnel_missing", "Cloudflare cannot find the saved tunnel")
	errLogin         = connector.Coded("cloudflare_login_failed", "the Cloudflare sign-in did not finish")
	errLoginTimeout  = connector.Coded("cloudflare_login_timeout", "the Cloudflare sign-in took too long")
	errInterrupted   = connector.Coded("cloudflare_login_interrupted", "the Cloudflare sign-in was interrupted; sign in again")
	errNameTaken     = connector.Coded("cloudflare_name_taken", "this name is already used in the domain; choose another")
	errCreate        = connector.Coded("cloudflare_create_failed", "Cloudflare could not create the tunnel")
	errRoute         = connector.Coded("cloudflare_route_failed", "Cloudflare could not point the name at the tunnel")
	errZone          = connector.Coded("cloudflare_zone_mismatch", "sign in to the domain this NAS already uses")
)

// domain is a tunnel made on the user's own domain. Its credentials sit
// beside it in a private file; its sign-in certificate is retained separately.
// Records saved by older versions may still list TCP services; they are
// ignored, so their ingress is never written again.
type domain struct {
	AddressCreatedAt time.Time `json:"addressCreatedAt,omitempty"`
	Hostname         string    `json:"hostname"`
	TunnelID         string    `json:"tunnelId"`
	// Routed are the App names already pointed at this tunnel.
	Routed []string `json:"routed,omitempty"`
	// AppRoutes binds an App identity to the exact DNS name Connect created.
	// It survives route withdrawal so deletion and orphan reconciliation do
	// not have to infer ownership from current publication state.
	AppRoutes map[string]string `json:"appRoutes,omitempty"`
}

func (t *Tunnel) domainDir() string          { return filepath.Join(t.cfg.Dir, "domain") }
func (t *Tunnel) domainFile(n string) string { return filepath.Join(t.domainDir(), n) }
func (t *Tunnel) certFile() string           { return filepath.Join(t.home(), ".cloudflared", "cert.pem") }

func (t *Tunnel) savedDomain() (domain, error) {
	var d domain
	raw, e := connector.ReadPrivate(t.domainFile("domain.json"))
	if e != nil || json.Unmarshal(raw, &d) != nil || !validHostname(d.Hostname) || !tunnelUUID.MatchString(d.TunnelID) {
		return domain{}, errNoDomain
	}
	if _, e = connector.ReadPrivate(t.domainFile("credentials.json")); e != nil {
		return domain{}, errNoDomain
	}
	return d, nil
}

func (t *Tunnel) saveDomain(d domain, c credentials) error {
	if e := t.rememberTunnel(c); e != nil {
		return e
	}
	if e := privateDir(t.domainDir()); e != nil {
		return e
	}
	if e := connector.WritePrivate(t.domainFile("credentials.json"), c.privateJSON()); e != nil {
		return storageError(e)
	}
	return t.saveRecord(d)
}

func (t *Tunnel) saveRecord(d domain) error {
	raw, _ := json.Marshal(d)
	if e := connector.WritePrivate(t.domainFile("domain.json"), raw); e != nil {
		return storageError(e)
	}
	return nil
}

// Domain puts DSM at subdomain of the zone the user picks while signing in
// to Cloudflare. An empty subdomain reconnects the saved own domain.
func (t *Tunnel) Domain(ctx context.Context, subdomain string) error {
	if subdomain != "" && !dnsLabel.MatchString(subdomain) {
		return errSubdomain
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	saved, missing := t.savedDomain()
	if subdomain == "" && missing != nil {
		return missing
	}
	if !t.PreserveDomain(subdomain) {
		if e := t.CheckRemoval(dependency.OwnDomain); e != nil {
			return e
		}
	}
	// The saved domain's own name reconnects without signing in again.
	if subdomain == "" || missing == nil && strings.HasPrefix(saved.Hostname, subdomain+".") {
		t.stop()
		return t.reconnect(ctx)
	}
	t.stop()
	requestCtx := ctx
	return t.signInFor(ctx, func(ctx context.Context, gen uint64, bin string) (domain, error) {
		return t.setUp(connector.WithNameReuse(ctx, connector.ReuseName(requestCtx)), gen, bin, subdomain)
	})
}

// signInFor runs one sign-in job: set up names with the certificate, then
// serve the domain it returns.
func (t *Tunnel) signInFor(ctx context.Context, setUp func(context.Context, uint64, string) (domain, error)) error {
	bin, port, e := t.prepare(ctx)
	if e != nil {
		return e
	}
	if e = t.saveSettings(settings{Enabled: true, Mode: "domain"}); e != nil {
		return e
	}
	t.spawn(connector.Status{State: "needs-login", Mode: "domain"}, func(ctx context.Context, gen uint64) {
		d, e := setUp(ctx, gen, bin)
		if e == nil {
			if e = t.serveDomain(ctx, gen, bin, d, port); e == nil {
				return
			}
		}
		if ctx.Err() == nil {
			t.fail(gen, e)
		}
	})
	return nil
}

// serveDomain runs d as job gen until ctx ends. It returns at once only
// when cloudflared cannot start.
func (t *Tunnel) serveDomain(ctx context.Context, gen uint64, bin string, d domain, port int) error {
	r, e := t.domainRun(d, port)
	if e != nil {
		return e
	}
	p, addr, out, e := t.launch(ctx, bin, r)
	if e != nil {
		return e
	}
	cfg := supervision.Defaults()
	if t.cfg.RestartDelay > 0 {
		cfg.Initial = t.cfg.RestartDelay
		cfg.Min = min(time.Second, cfg.Initial)
	}
	t.watch(ctx, gen, p, addr, out, r, supervision.New(cfg, nil))
	return nil
}

func (t *Tunnel) reconnect(ctx context.Context) error {
	d, e := t.savedDomain()
	if e != nil {
		return e
	}
	bin, port, e := t.prepare(ctx)
	if e != nil {
		return e
	}
	if e = t.saveSettings(settings{Enabled: true, Mode: "domain"}); e != nil {
		return e
	}
	r, e := t.domainRun(d, port)
	if e != nil {
		// Restore and reconnect show why the saved domain cannot serve, such
		// as a DSM target that still needs the administrator's review.
		t.refused(d, e)
		return e
	}
	// A live retained tunnel may still have DNS pointing at an older identity.
	// Confirmed deletion keeps the existing watcher-driven recreation path;
	// an unavailable control API does not prevent retained credentials serving.
	if signed, err := readCert(t.certFile()); err == nil {
		if deleted, err := t.cfg.API.tunnelDeleted(ctx, signed, d.TunnelID); err == nil && !deleted {
			t.ops.Lock()
			err = t.route(ctx, bin, d.TunnelID, d.Hostname)
			t.ops.Unlock()
			if err != nil {
				t.refused(d, err)
				return err
			}
		}
	}
	return t.serve(ctx, bin, r)
}

// refused shows why the saved domain d cannot serve. The next start begins a
// new status, so nothing stays failed once the cause is approved.
func (t *Tunnel) refused(d domain, e error) {
	t.begin(connector.Status{State: "failed", Code: failCode(e), Mode: "domain", Hostname: d.Hostname, NameConflict: connector.Conflict(e), DSMPortChange: connector.PortChange(e)})
}

// domainRun writes the local configuration, so only this hostname reaches
// DSM and nothing else is served, then runs the tunnel by its credentials.
func (t *Tunnel) domainRun(d domain, port int) (run, error) {
	if e := t.saveRecord(d); e != nil {
		return run{}, e
	}
	return t.domainRunFile(d, port, t.domainFile("config.yml"))
}

func (t *Tunnel) domainRunFile(d domain, port int, config string) (run, error) {
	credentials := t.domainFile("credentials.json")
	ingressOnly := t.cfg.IngressOnly != nil && t.cfg.IngressOnly()
	if t.cfg.DSMTarget == nil && !ingressOnly {
		return run{}, errProxyRequired
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	dsmRule := ""
	if !ingressOnly {
		socket, e := t.cfg.DSMTarget(ctx, d.Hostname)
		if e != nil {
			return run{}, e
		}
		if !privateSocket(socket) {
			return run{}, errProxyRequired
		}
		dsmRule = fmt.Sprintf("  - hostname: %q\n    service: %q\n", d.Hostname, "unix:"+socket)
	}
	appRules, e := t.appIngress(d)
	if e != nil {
		return run{}, e
	}
	serverRule, protocol, e := t.serverIngress(d)
	if e != nil {
		return run{}, e
	}
	yaml := fmt.Sprintf("tunnel: %q\ncredentials-file: %q\n%singress:\n", d.TunnelID, credentials, protocol) + dsmRule
	yaml += appRules + serverRule
	yaml += "  - service: http_status:404\n"
	if e := connector.WritePrivate(config, []byte(yaml)); e != nil {
		return run{}, storageError(e)
	}
	return run{mode: "domain", hostname: d.Hostname, addressCreatedAt: d.AddressCreatedAt, port: port, args: func(metrics string) []string {
		return []string{"tunnel", "--no-autoupdate", "--config", config, "--metrics", metrics, "run", "--credentials-file", credentials, d.TunnelID}
	}}, nil
}

// setUp signs in, creates the tunnel and routes the name to it, in that
// order. Retain successful authorization; failed setup discards it. A tunnel
// whose name cannot be routed is deleted again.
func (t *Tunnel) setUp(ctx context.Context, gen uint64, bin, subdomain string) (result domain, resultErr error) {
	t.ops.Lock()
	defer t.ops.Unlock()
	cert := t.certFile()
	keep := false
	defer func() {
		if connector.Conflict(resultErr) != nil {
			keep = true
		}
		if !keep {
			_ = os.Remove(cert)
		}
	}()
	signed, zone, e := t.authorized(ctx, gen, bin)
	if e != nil {
		return domain{}, e
	}
	d := domain{Hostname: subdomain + "." + zone}
	if suggested, _ := t.defaultName(); subdomain == suggested {
		d.Hostname, e = t.defaultDomainName(ctx, signed, zone, nil)
		if e != nil {
			return domain{}, e
		}
	}
	if !validHostname(d.Hostname) {
		return domain{}, errHostname
	}
	t.set(gen, func(s *connector.Status) { s.State, s.LoginURL, s.Hostname = "connecting", "", d.Hostname })
	if e = t.namesFree(ctx, signed, []string{d.Hostname}); e != nil {
		return domain{}, e
	}
	created, e := t.create(ctx, bin, cert, subdomain, signed.accountID)
	if e != nil {
		return domain{}, e
	}
	d.TunnelID = created.tunnelID
	if e = t.route(ctx, bin, d.TunnelID, d.Hostname); e != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		t.launcher().Run(cleanup, Command{Path: bin, Args: []string{"tunnel", "--origincert", cert, "delete", d.TunnelID}, Home: t.home()})
		return domain{}, e
	}
	d.AddressCreatedAt = time.Now()
	if previous, err := t.savedDomain(); err == nil && previous.TunnelID != d.TunnelID {
		if e = t.retireTunnel(signed, previous.TunnelID); e != nil {
			return domain{}, e
		}
	}
	if e = t.saveDomain(d, created); e != nil {
		return domain{}, e
	}
	keep = true
	return d, nil
}

// signedIn signs in to Cloudflare and reads the zone the user picked. The
// caller removes unsuccessful or wrong-zone authorization.
func (t *Tunnel) signedIn(ctx context.Context, gen uint64, bin string) (cert, string, error) {
	file := t.certFile()
	if e := os.MkdirAll(filepath.Dir(file), 0700); e != nil {
		return cert{}, "", storageError(e)
	}
	os.Remove(file) // cloudflared does not sign in again over a certificate
	if e := t.signIn(ctx, gen, bin); e != nil {
		return cert{}, "", e
	}
	signed, e := readCert(file)
	if e != nil {
		return cert{}, "", errLogin
	}
	zone, e := t.cfg.API.ZoneName(ctx, signed)
	return signed, zone, e
}

// namesFree classifies collisions before creating any resources.
func (t *Tunnel) namesFree(ctx context.Context, signed cert, names []string) error {
	_, err := t.nameClaims(ctx, signed, names)
	return err
}
func (t *Tunnel) route(ctx context.Context, bin, tunnelID, name string) error {
	c, e := readCert(t.certFile())
	if e != nil {
		return errReconnect
	}
	if t.cfg.BeforeRoute != nil && name != t.serverHostname() {
		if e := t.cfg.BeforeRoute(ctx, c.accountID, name); e != nil {
			return e
		}
	}
	observed, e := t.classifyName(ctx, c, name, tunnelID)
	if e != nil {
		return e
	}
	if observed.owned && observed.tunnel != tunnelID {
		if e = t.retireTunnel(c, observed.tunnel); e != nil {
			return e
		}
	}
	note, e := t.cfg.API.claimName(ctx, c, observed, tunnelID, connector.ReuseFor(ctx, name), func() error {
		if err := t.launcher().Run(ctx, Command{Path: bin, Args: []string{"tunnel", "--origincert", t.certFile(), "route", "dns", tunnelID, name}, Home: t.home()}); err != nil {
			return errRoute
		}
		return nil
	})
	if note != "" {
		t.smu.Lock()
		t.cleanupNote = note
		t.cleanupURL = "https://one.dash.cloudflare.com/" + c.accountID + "/networks/connectors"
		t.smu.Unlock()
	}
	if e == nil {
		if e = t.rememberRoute(ctx, c, name, tunnelID); e != nil {
			return e
		}
	}
	return e
}

// authorized reuses the certificate a name conflict kept, else signs in.
func (t *Tunnel) authorized(ctx context.Context, gen uint64, bin string) (cert, string, error) {
	if _, e := os.Lstat(t.certFile()); !os.IsNotExist(e) {
		signed, e := readCert(t.certFile())
		if e != nil {
			return cert{}, "", errReconnect
		}
		zone, e := t.cfg.API.ZoneName(ctx, signed)
		if e != nil {
			return cert{}, "", errReconnect
		}
		return signed, zone, nil
	}
	return t.signedIn(ctx, gen, bin)
}

// signIn runs `cloudflared tunnel login` and shows its page until the user
// approves and cloudflared exits.
func (t *Tunnel) signIn(ctx context.Context, gen uint64, bin string) error {
	out := &lines{find: loginURLFromOutput}
	p, e := t.launcher().Start(ctx, Command{Path: bin, Args: []string{"tunnel", "login"}, Home: t.home(), Output: out})
	if e != nil {
		return errLogin
	}
	defer p.Stop()
	timeout := time.NewTimer(orDefault(t.cfg.LoginTimeout, 10*time.Minute))
	defer timeout.Stop()
	tick := time.NewTicker(orDefault(t.cfg.Poll, time.Second))
	defer tick.Stop()
	for p.Alive() {
		if page := out.found(); page != "" {
			t.set(gen, func(s *connector.Status) { s.LoginURL = page })
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timeout.C:
			return errLoginTimeout
		case <-tick.C:
		}
	}
	return nil
}

// create makes a tunnel named after the subdomain and reads its credentials.
func (t *Tunnel) create(ctx context.Context, bin, cert, subdomain, account string) (credentials, error) {
	suffix := make([]byte, 4)
	rand.Read(suffix)
	name := "synopkgconnect-" + subdomain + "-" + hex.EncodeToString(suffix)
	file := filepath.Join(t.home(), "created.json")
	os.Remove(file)
	defer os.Remove(file)
	if t.launcher().Run(ctx, Command{Path: bin, Args: []string{"tunnel", "--origincert", cert, "create", "--credentials-file", file, name}, Home: t.home()}) != nil {
		return credentials{}, errCreate
	}
	f, e := os.Open(file)
	if e != nil {
		return credentials{}, errCreate
	}
	defer f.Close()
	raw, _ := io.ReadAll(io.LimitReader(f, 4097))
	c, e := parseCredentials(raw)
	if e != nil || c.account != account {
		return credentials{}, errCreate
	}
	if e := t.rememberTunnel(c); e != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = t.launcher().Run(cleanup, Command{Path: bin, Args: []string{"tunnel", "--origincert", cert, "delete", c.tunnelID}, Home: t.home()})
		return credentials{}, e
	}
	return c, nil
}
