// Package appaccess composes App approval, pin proxies and provider owners.
package appaccess

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"dsmapp/internal/apps"
	"dsmapp/internal/cfaccess"
	"dsmapp/internal/cloudflare"
	"dsmapp/internal/connector"
	"dsmapp/internal/provider"
	"dsmapp/internal/workers"
	"github.com/synopackageland/syno-pkg-land-sdk/dsmservices"
)

type PrivateProvider interface {
	AppAccessNetwork(context.Context) (provider.AppNetwork, error)
	RefreshApps(context.Context) error
}
type PublicProvider interface {
	AppDomain() (cloudflare.AppDomain, error)
	DefaultAppHostname(context.Context, string, []string) (string, error)
	DeleteAppRoute(context.Context, string) error
	Authorization(context.Context) (cloudflare.Authorization, error)
	ConnectAuthorization(context.Context) (cloudflare.AuthorizationResult, error)
	DisconnectAuthorization() error
	SetPublicApps(context.Context, []apps.Target) (cloudflare.PublicResult, error)
	CheckPublicApps(context.Context, []apps.Target) error
	ReconcileAppRoutes(context.Context) error
}
type Config struct {
	// IngressIdentity overrides installed package evidence in local fixtures only.
	IngressIdentity  func(string, int) error
	IngressConnector func() connector.Status
	Access           *cfaccess.Manager
	Workers          *workers.Manager
	Store            apps.Store
	Discovery        apps.Discovery
	ProjectionPath   string
	DSMPort          func(context.Context) (int, error)
	Verifier         *apps.Verifier
	Private          PrivateProvider
	Public           PublicProvider
	Registry         *apps.PinRegistry
	// Guard vets every custom App dial; Login is the DSM sign-in gate.
	Guard apps.TargetGuard
	Login apps.Login
	// Services reads DSM's port definitions for the custom App pick list.
	Services func(context.Context) []dsmservices.Definition
	// WorkersAddress runs when public DSM gets a new Free link address
	// (turned on, Add DSM, a followed rename), so its connection check
	// settles from now, as a new own-domain connection does.
	WorkersAddress func()
}
type running struct {
	binding apps.PinBinding
	stop    func()
}
type Engine struct {
	entryMu    sync.Mutex
	cfg        Config
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex // Maps only; never held during state or provider operations.
	live       map[string]running
	allowed    map[string]bool
	recovering bool
	applied    map[string]string
	wg         sync.WaitGroup
	restoring  atomic.Bool
}

// SetRestoring marks providers as still restoring. Availability then answers
// without waiting on them, so the App list stays readable during a slow start.
func (e *Engine) SetRestoring(v bool) { e.restoring.Store(v) }

func New(ctx context.Context, cfg Config) *Engine {
	ctx, cancel := context.WithCancel(ctx)
	if cfg.Registry == nil {
		cfg.Registry = &apps.PinRegistry{}
	}
	e := &Engine{cfg: cfg, ctx: ctx, cancel: cancel, live: map[string]running{}, allowed: map[string]bool{}, applied: map[string]string{}}
	cfg.Registry.Observe = e.observe
	return e
}
func (e *Engine) Lifecycle() apps.Lifecycle {
	return apps.Lifecycle{Store: e.cfg.Store, ProjectionPath: e.cfg.ProjectionPath, Runtime: e}
}
func (e *Engine) Service() *apps.Service {
	return &apps.Service{Discovery: e.cfg.Discovery, Lifecycle: e.Lifecycle(), Approver: e, Provider: e, Guard: e.cfg.Guard}
}
func (e *Engine) Close() { e.cancel(); e.stopAll(); e.wg.Wait() }
func socketID() (string, error) {
	var b [16]byte
	_, err := rand.Read(b[:])
	return "a" + hex.EncodeToString(b[:]), err
}
func pin(b apps.PinBinding) apps.Pin {
	return apps.Pin{Mode: string(b.Mode), Names: append([]string(nil), b.Names...), SPKI: b.SPKI, CertSHA256: b.CertSHA256}
}
func binding(t apps.Target) apps.PinBinding {
	return apps.PinBinding{EntryPath: apps.EntryPath(t.Scope.Binding.Entry), Provider: t.Scope.Provider, Package: t.Package, Target: apps.TargetApp, Scope: t.Kind, Socket: t.Scope.Socket, Port: t.Scope.Binding.Port, Summary: t.Scope.Binding.Summary, Authority: t.Scope.Authority, Mode: apps.Mode(t.Scope.Binding.Pin.Mode), Names: t.Scope.Binding.Pin.Names, SPKI: t.Scope.Binding.Pin.SPKI}
}

func (e *Engine) Availability(ctx context.Context) (apps.Availability, error) {
	out := apps.Availability{Private: apps.PrivateAvailability{Code: "tailscale-off"}}
	if e.restoring.Load() {
		out.Starting = true
		return out, nil
	}
	if e.cfg.Private != nil {
		n, err := e.cfg.Private.AppAccessNetwork(ctx)
		if err == nil {
			out.Private = apps.PrivateAvailability{Available: n.Available, Host: n.Host, Code: n.Code}
		}
	}
	if e.cfg.Public != nil {
		if domain, err := e.cfg.Public.AppDomain(); err == nil {
			out.Public.Available = domain.Enabled
			out.Public.Enabled = domain.Enabled
			out.Public.Zone = domain.Zone
			if auth, err := e.cfg.Public.Authorization(ctx); err == nil {
				out.Public.Connected = auth.Connected
				out.Public.Invalid = auth.Zone != domain.Zone
			} else {
				out.Public.Invalid = true
			}
		}
		if p, ok := e.cfg.Public.(interface{ OrphanedAppRoutes() int }); ok {
			out.Public.OrphanedRoutes = p.OrphanedAppRoutes()
		}
	}
	if e.cfg.Workers != nil {
		out.HasWorkers = true
		e.followWorkers(ctx)
		out.Workers, _ = e.cfg.Workers.Status(ctx)
	}
	return out, nil
}
func (e *Engine) CloudflareConnect(ctx context.Context) (apps.LoginResult, error) {
	if e.cfg.Public == nil {
		return apps.LoginResult{}, apps.ErrUnavailable
	}
	out, err := e.cfg.Public.ConnectAuthorization(ctx)
	return apps.LoginResult{LoginURL: out.LoginURL}, err
}
func (e *Engine) CloudflareDisconnect(context.Context) error {
	if e.cfg.Public == nil {
		return apps.ErrUnavailable
	}
	return e.cfg.Public.DisconnectAuthorization()
}

func (e *Engine) CleanupPublicOrphans(ctx context.Context) (apps.OrphanCleanup, error) {
	p, ok := e.cfg.Public.(interface {
		CleanupOrphanedAppRoutes(context.Context) (int, error)
	})
	if !ok {
		return apps.OrphanCleanup{}, apps.ErrUnavailable
	}
	removed, err := p.CleanupOrphanedAppRoutes(ctx)
	return apps.OrphanCleanup{Removed: removed}, err
}

func (e *Engine) authority(ctx context.Context, pkg, kind string) (string, string, error) {
	if kind == apps.Public {
		if err := e.publicMode(ctx); err != nil {
			return "", "", err
		}
	}
	if kind == apps.Public && apps.PublicProvider(ctx) == apps.Workers {
		if e.cfg.Workers == nil {
			return "", "", apps.ErrUnavailable
		}
		host, err := e.cfg.Workers.Authority(ctx, pkg)
		if err != nil {
			return "", "", err
		}
		dsm, err := e.cfg.Workers.Authority(ctx, "")
		return host, dsm, err
	}
	if kind == apps.Private {
		if e.cfg.Private == nil {
			return "", "", apps.ErrUnavailable
		}
		n, err := e.cfg.Private.AppAccessNetwork(ctx)
		if err != nil || !n.Available {
			return "", "", apps.ErrUnavailable
		}
		port, err := e.dsmHTTPSPort(ctx)
		if err != nil {
			return "", "", err
		}
		return n.Host, net.JoinHostPort(n.Host, strconv.Itoa(port)), nil
	}
	if e.cfg.Public == nil {
		return "", "", apps.ErrUnavailable
	}
	if pkg == "" {
		if p, ok := e.cfg.Public.(interface{ Status() connector.Status }); ok {
			s := p.Status()
			if s.Mode == "quick" && apps.QuickDSMHost(s.Hostname) {
				return s.Hostname, s.Hostname, nil
			}
		}
	}
	d, err := e.cfg.Public.AppDomain()
	if err != nil || !d.Enabled {
		return "", "", apps.ErrUnavailable
	}
	if pkg == "" {
		return d.Hostname, d.Hostname, nil
	}
	state, err := e.cfg.Store.Load()
	if err != nil {
		return "", "", err
	}
	if def := state.Custom[pkg]; def.Ingress != nil {
		if requested := apps.Hostname(ctx, pkg); requested != "" && requested != def.Ingress.Hostname {
			return "", "", apps.ErrInvalid
		}
		ctx = apps.WithHostnames(ctx, map[string]string{pkg: def.Ingress.Hostname})
	}
	var host string
	if old := state.Apps[pkg].Public; old != nil && old.Provider != apps.Workers {
		host = old.Authority
	} else {
		// Explicit edits retain the collision preflight; only defaults walk the ladder.
		host = apps.Hostname(ctx, pkg)
		if host == "" {
			occupied := []string{d.Hostname}
			for other, app := range state.Apps {
				if other != pkg && app.Public != nil {
					occupied = append(occupied, app.Public.Authority)
				}
			}
			host, err = e.cfg.Public.DefaultAppHostname(ctx, pkg, occupied)
			if err != nil {
				return "", "", err
			}
		}
	}
	selected, err := selectHostname(host, apps.Hostname(ctx, pkg), d.Zone)
	if selected != host && state.Apps[pkg].Public != nil {
		return "", "", apps.ErrInvalid
	}
	return selected, d.Hostname, err
}

func (e *Engine) proposal(ctx context.Context, b apps.PinBinding, old *apps.PinBinding) (apps.PinBinding, apps.Certificate, error) {
	var reenable apps.Reenable
	if old != nil {
		b.Mode, b.Names, b.SPKI = old.Mode, old.Names, old.SPKI
		var err error
		reenable, err = e.cfg.Verifier.PrepareReenable(ctx, b)
		if err != nil {
			if b.Target == apps.TargetDSM {
				err = dsmHTTPSUnavailable(err)
			}
			return b, apps.Certificate{}, err
		}
		b, err = apps.ConfirmReenable(b, reenable)
		if err != nil {
			return b, apps.Certificate{}, err
		}
	}
	p, err := e.cfg.Verifier.Propose(ctx, b.Target, b.Port, b.Package, b.Authority, b.EntryPath)
	if err != nil {
		if b.Target == apps.TargetDSM {
			err = dsmHTTPSUnavailable(err)
		}
		return b, apps.Certificate{}, err
	}
	if old == nil {
		confirmed := apps.Confirm(p, b.Package, b.Authority)
		b.Mode, b.Names, b.SPKI = confirmed.Mode, confirmed.Names, confirmed.SPKI
	}
	cert := apps.Certificate{CertSHA256: p.CertSHA256, Mode: string(b.Mode), Issuer: p.Issuer, Expires: p.NotAfter.Format(time.RFC3339)}
	if b.Mode == apps.ModeW {
		cert.Names = append([]string(nil), b.Names...)
		if old != nil && !reflect.DeepEqual(old.Names, b.Names) {
			cert.PreviousNames = append([]string(nil), old.Names...)
		}
	} else {
		cert.SPKI = b.SPKI
		if p.SPKI == b.SPKI {
			cert.CertSHA256 = p.CertSHA256
		}
		if old != nil && old.SPKI != b.SPKI {
			cert.PreviousSPKI = old.SPKI
		}
	}
	return b, cert, nil
}

func (e *Engine) Inspect(ctx context.Context, app apps.App, kind string, previous *apps.Scope) (apps.Approval, error) {
	if e.cfg.Verifier == nil || e.cfg.DSMPort == nil {
		return apps.Approval{}, apps.ErrUnavailable
	}
	if apps.IsCustom(app.Package) {
		return e.inspectCustom(ctx, app, kind, previous)
	}
	authority, dsmAuthority, err := e.authority(ctx, app.Package, kind)
	if err != nil {
		return apps.Approval{}, err
	}
	if kind == apps.Private {
		servePort := app.Port
		if previous != nil {
			if h, p, e := net.SplitHostPort(previous.Authority); e == nil && h == authority {
				servePort, _ = strconv.Atoi(p)
			}
		} else if app.Entry != nil && app.Entry.Path != "" {
			state, err := e.cfg.Store.Load()
			if err != nil {
				return apps.Approval{}, err
			}
			servePort = privateEntryPort(app.Package, state, app.Port)
		}
		authority = net.JoinHostPort(authority, strconv.Itoa(servePort))
	}
	if previous != nil && (previous.Provider == apps.Workers) != (apps.PublicProvider(ctx) == apps.Workers) {
		return apps.Approval{}, apps.ErrInvalid
	}
	b := apps.PinBinding{EntryPath: apps.EntryPath(app.Entry), Provider: apps.PublicProvider(ctx), Package: app.Package, Target: apps.TargetApp, Scope: kind, Port: app.Port, Authority: authority, Summary: app.Summary}
	var old *apps.PinBinding
	if previous != nil {
		value := binding(apps.Target{Package: app.Package, Kind: kind, Scope: *previous})
		old = &value
	}
	b, cert, err := e.proposal(ctx, b, old)
	if err != nil {
		return apps.Approval{}, err
	}
	out := apps.Approval{Scope: apps.Scope{Provider: apps.PublicProvider(ctx), State: apps.Active, Authority: authority, Binding: apps.Binding{Protection: &app.Protection, Entry: app.Entry, Port: app.Port, Summary: app.Summary, Pin: pin(b)}}, Certificate: cert}
	if kind == apps.Public {
		out.Scope.Origin = "https://" + authority
	}
	if app.Entry != nil && app.Entry.Path != "" {
		return out, nil
	}
	out.DSM, out.DSMTarget, err = e.dsmProposal(ctx, kind, dsmAuthority)
	return out, err
}

// dsmProposal is the DSM target a scope still needs approved, or nil when its
// saved endpoint has the current port and authority and still verifies.
func (e *Engine) dsmProposal(ctx context.Context, kind, authority string) (*apps.DSMApproval, *apps.PinBinding, error) {
	state, err := e.cfg.Store.Load()
	if err != nil {
		return nil, nil, err
	}
	port, err := e.dsmHTTPSPort(ctx)
	if err != nil {
		return nil, nil, err
	}
	db := apps.PinBinding{Provider: apps.PublicProvider(ctx), Target: apps.TargetDSM, Scope: kind, Port: port, Authority: authority}
	var oldDSM *apps.PinBinding
	for _, saved := range apps.PinBindings(state) {
		if saved.Target == apps.TargetDSM && saved.Scope == kind && (saved.Provider == apps.Workers) == (apps.PublicProvider(ctx) == apps.Workers) {
			value := saved
			oldDSM = &value
		}
	}
	if oldDSM == nil && state.DSM != nil {
		d := state.DSM
		oldDSM = &apps.PinBinding{Target: apps.TargetDSM, Scope: kind, Port: d.Port, Authority: authority, Mode: apps.Mode(d.Pin.Mode), Names: d.Pin.Names, SPKI: d.Pin.SPKI, CertSHA256: d.Pin.CertSHA256}
	}
	if oldDSM != nil && oldDSM.Socket != "" && oldDSM.Port == port && oldDSM.Authority == authority && e.cfg.Verifier.VerifyTarget(ctx, *oldDSM) == nil {
		return nil, nil, nil
	}
	db, dc, err := e.proposal(ctx, db, oldDSM)
	if err != nil {
		return nil, nil, err
	}
	db.CertSHA256 = dc.CertSHA256
	if state.DSM != nil {
		dc.Previous = state.DSM.Certificate
	}
	return &apps.DSMApproval{Hostname: authority, Port: port, Pin: dc}, &db, nil
}

// InspectDSM proposes DSM alone for a provider that routes DSM without an App.
func (e *Engine) InspectDSM(ctx context.Context, kind string) (apps.Approval, error) {
	if e.cfg.Verifier == nil || e.cfg.DSMPort == nil {
		return apps.Approval{}, apps.ErrUnavailable
	}
	_, authority, err := e.authority(ctx, "", kind)
	if err != nil {
		return apps.Approval{}, err
	}
	// An unchanged retained trust needs route repair, not a second decision.
	if state, loadErr := e.cfg.Store.Load(); loadErr != nil {
		return apps.Approval{}, loadErr
	} else if state.DSM != nil && e.BindTrustedDSM(ctx, kind, authority) == nil {
		return apps.Approval{Kind: kind}, nil
	}
	out := apps.Approval{Kind: kind}
	out.DSM, out.DSMTarget, err = e.dsmProposal(ctx, kind, authority)
	return out, err
}

// OpenDSM persists a confirmed DSM-only approval through the same writer and
// revision check as an App approval, starts its pin proxy, then lets the
// provider route DSM through it. Trust applies to this NAS's certificate;
// both previously configured ways reconcile using the new trust.
func (e *Engine) OpenDSM(ctx context.Context, a apps.Approval) error {
	if a.DSMTarget == nil || a.ExpectedRevision == 0 {
		return apps.ErrInvalid
	}
	if a.DSMTarget.Provider == apps.Workers {
		if e.cfg.Workers == nil {
			return apps.ErrUnavailable
		}
		if err := e.cfg.Workers.Ensure(ctx, []string{""}); err != nil {
			return err
		}
	}
	state, err := e.cfg.Store.Load()
	if err != nil {
		return err
	}
	if state.NextRev != a.ExpectedRevision {
		return apps.ErrConfirmationChanged
	}
	dsm, err := e.mergeDSM(state, a.DSMTarget)
	if err != nil {
		return err
	}
	dsm.Certificate = &apps.TrustedCertificate{Issuer: a.DSM.Pin.Issuer, Expires: a.DSM.Pin.Expires, CertSHA256: a.DSM.Pin.CertSHA256}
	if err = e.ValidateDSM(ctx, dsm); err != nil {
		return err
	}
	kinds := []string{apps.Public, apps.Private}
	err = e.cfg.Store.Update(ctx, func(current *apps.State) error {
		if current.NextRev != a.ExpectedRevision {
			return apps.ErrConfirmationChanged
		}
		current.DSM = dsm
		_, err := current.Revision()
		return err
	})
	if err != nil {
		return err
	}
	for _, k := range kinds {
		if err = e.activateProxies(apps.Target{Kind: k}); err != nil {
			return err
		}
	}
	if state, err = e.cfg.Store.Load(); err != nil {
		return err
	}
	if err = apps.WriteProjection(e.cfg.ProjectionPath, state); err != nil {
		return err
	}
	for _, k := range kinds {
		e.mu.Lock()
		delete(e.applied, k)
		e.mu.Unlock()
		err = errors.Join(err, e.Remove(ctx, apps.Target{Kind: k}))
	}
	if err != nil {
		return err
	}
	return e.refreshDSMRoute(ctx, a.Kind)
}
func (e *Engine) CheckPublic(ctx context.Context, targets []apps.Target) error {
	if len(targets) > 0 && targets[0].Scope.Provider == apps.Workers {
		if e.cfg.Workers == nil {
			return apps.ErrUnavailable
		}
		pkgs := []string{""}
		for _, t := range targets {
			pkgs = append(pkgs, t.Package)
		}
		return e.cfg.Workers.Check(ctx, pkgs)
	}
	if e.cfg.Public == nil {
		return apps.ErrUnavailable
	}
	return e.cfg.Public.CheckPublicApps(ctx, targets)
}

func (e *Engine) validateLocal(ctx context.Context, t apps.Target) error {
	if apps.IsCustom(t.Package) {
		return e.validateCustom(t)
	}
	g, err := e.cfg.Discovery.ReadGateway(t.Package)
	if err != nil {
		return err
	}
	_, summary := apps.ProtectionOf(g)
	resolvedPort := g.Listen.HTTPS
	if g.IngressPath != "" {
		resolvedPort, err = e.dsmHTTPSPort(ctx)
		if err != nil {
			return err
		}
	}
	if g.IngressPath != apps.EntryPath(t.Scope.Binding.Entry) || resolvedPort != t.Scope.Binding.Port || summary != t.Scope.Binding.Summary || g.External.Connect == "none" || t.Kind == apps.Public && !g.HasExternal {
		return apps.ErrConfirmationChanged
	}
	return e.cfg.Verifier.VerifyTarget(ctx, binding(t))
}
func (e *Engine) Validate(ctx context.Context, t apps.Target) error {
	authority, _, err := e.authority(apps.WithPublicProvider(ctx, t.Scope.Provider), t.Package, t.Kind)
	if err != nil {
		return err
	}
	if t.Kind == apps.Private {
		h, p, err := net.SplitHostPort(t.Scope.Authority)
		if err != nil || h != authority {
			return apps.ErrConfirmationChanged
		}
		authority = net.JoinHostPort(authority, p)
	}
	if authority != t.Scope.Authority {
		return apps.ErrConfirmationChanged
	}
	return e.validateLocal(ctx, t)
}
func (e *Engine) ValidateDSM(ctx context.Context, d *apps.DSMState) error {
	port, err := e.dsmHTTPSPort(ctx)
	if err != nil {
		return err
	}
	if port != d.Port {
		return apps.ErrConfirmationChanged
	}
	s := apps.EmptyState()
	s.DSM = d
	for _, b := range apps.PinBindings(s) {
		if err = e.cfg.Verifier.VerifyTarget(ctx, b); err != nil {
			if apps.ReasonOf(err) == "" {
				return dsmHTTPSUnavailable(err)
			}
			return err
		}
	}
	return nil
}

func (e *Engine) mergeDSM(state apps.State, b *apps.PinBinding) (*apps.DSMState, error) {
	if b == nil {
		return nil, nil
	}
	d := &apps.DSMState{Port: b.Port, Pin: pin(*b)}
	if state.DSM != nil {
		*d = *state.DSM

		if d.Port != b.Port || !reflect.DeepEqual(d.Pin, pin(*b)) {
			for kind, ep := range map[string]**apps.Endpoint{apps.Public: &d.Public, apps.Private: &d.Private} {
				if *ep != nil {
					copy := **ep
					id, err := dsmSocketID(kind, copy.Authority)
					if err != nil {
						return nil, err
					}
					copy.Socket = id
					*ep = &copy
				}
			}
		}
		d.Port, d.Pin = b.Port, pin(*b)
	}
	id, err := dsmSocketID(b.Scope, b.Authority)
	if err != nil {
		return nil, err
	}
	ep := &apps.Endpoint{Provider: b.Provider, Socket: id, Authority: b.Authority}
	if b.Scope == apps.Private {
		d.Private = ep
	} else {
		d.Public = ep
		d.Origin = "https://" + b.Authority
	}
	return d, nil
}

type batchContextKey struct{}
type batchIntent struct {
	dsm              *apps.DSMState
	reenable         bool
	expectedRevision uint64
}

func (e *Engine) OpenApproved(ctx context.Context, targets []apps.Target, approvals []apps.Approval, reenable bool) (apps.Mutation, error) {
	if len(targets) == 0 || len(targets) != len(approvals) {
		return apps.Mutation{}, apps.ErrInvalid
	}
	state, err := e.cfg.Store.Load()
	if err != nil {
		return apps.Mutation{}, err
	}
	if approvals[0].ExpectedRevision != state.NextRev {
		return apps.Mutation{}, apps.ErrConfirmationChanged
	}
	var dsm *apps.DSMState
	for _, a := range approvals {
		if a.DSMTarget != nil {
			candidate, err := e.mergeDSM(state, a.DSMTarget)
			if err != nil {
				return apps.Mutation{}, err
			}
			if a.DSM != nil {
				candidate.Certificate = &apps.TrustedCertificate{Issuer: a.DSM.Pin.Issuer, Expires: a.DSM.Pin.Expires, CertSHA256: a.DSM.Pin.CertSHA256}
			}
			if dsm != nil && (dsm.Port != candidate.Port || !reflect.DeepEqual(dsm.Pin, candidate.Pin)) {
				return apps.Mutation{}, apps.ErrConfirmationChanged
			}
			dsm = candidate
		}
	}

	if targets[0].Kind == apps.Public {
		hosts := []string{}
		for _, t := range targets {
			hosts = append(hosts, t.Scope.Authority)
		}
		if dsm != nil && dsm.Public != nil {
			hosts = append(hosts, dsm.Public.Authority)
		}
		if err = e.prepareAccess(ctx, targets[0].Scope.Provider, hosts...); err != nil {
			return apps.Mutation{}, err
		}
	}
	if targets[0].Kind == apps.Public && targets[0].Scope.Provider == apps.Workers {
		if e.cfg.Workers == nil {
			return apps.Mutation{}, apps.ErrUnavailable
		}
		pkgs := []string{""}
		for _, t := range targets {
			if err = e.Validate(ctx, t); err != nil {
				return apps.Mutation{}, err
			}
			pkgs = append(pkgs, t.Package)
		}
		if dsm != nil {
			if err = e.ValidateDSM(ctx, dsm); err != nil {
				return apps.Mutation{}, err
			}
		}
		if err = e.cfg.Workers.Ensure(ctx, pkgs); err != nil {
			return apps.Mutation{}, err
		}
		ctx = context.WithValue(ctx, batchContextKey{}, batchIntent{dsm: dsm, reenable: reenable, expectedRevision: state.NextRev})
		if err = e.ActivatePublic(ctx, targets); err != nil {
			_ = e.PausePublic(ctx, targets, "workers_activation_failed")
			return apps.Mutation{}, err
		}
		if err = e.Remove(ctx, apps.Target{Kind: apps.Public}); err != nil {
			_ = e.PausePublic(ctx, targets, "workers_activation_failed")
			return apps.Mutation{}, err
		}
		out := apps.Mutation{}
		for _, t := range targets {
			out.Results = append(out.Results, apps.Result{Package: t.Package, OK: true})
		}
		return out, nil
	}
	if targets[0].Kind == apps.Public && !reenable {
		if e.cfg.Public == nil {
			return apps.Mutation{}, apps.ErrUnavailable
		}
		for _, t := range targets {
			if err = e.Validate(ctx, t); err != nil {
				return apps.Mutation{}, err
			}
		}
		if dsm != nil {
			if err = e.ValidateDSM(ctx, dsm); err != nil {
				return apps.Mutation{}, err
			}
		}
		if err = e.CheckPublic(ctx, targets); err != nil {
			return apps.Mutation{}, err
		}
		ctx = context.WithValue(ctx, batchContextKey{}, batchIntent{dsm: dsm, reenable: false, expectedRevision: state.NextRev})
		result, err := e.cfg.Public.SetPublicApps(ctx, targets)
		if err == nil {
			e.markApplied(apps.Public, e.routeSignature(apps.Public))
		}
		out := apps.Mutation{Results: []apps.Result{}}
		for _, r := range result.Results {
			out.Results = append(out.Results, apps.Result{Package: r.Package, OK: r.OK, Error: r.Code})
		}
		return out, err
	}
	out := apps.Mutation{Results: []apps.Result{}}
	for _, t := range targets {
		if err = e.Lifecycle().OpenWithDSM(ctx, t, reenable, dsm, state.NextRev); err != nil {
			return out, err
		}
		out.Results = append(out.Results, apps.Result{Package: t.Package, State: apps.Active, OK: true})
	}
	return out, nil
}

func (e *Engine) stop(id string) {
	e.mu.Lock()
	r, ok := e.live[id]
	delete(e.live, id)
	delete(e.allowed, id)
	e.mu.Unlock()
	e.cfg.Registry.Stop(id)
	if ok {
		r.stop()
	}
}
func (e *Engine) stopAll() {
	e.mu.Lock()
	ids := make([]string, 0, len(e.live))
	for id := range e.live {
		ids = append(ids, id)
	}
	e.mu.Unlock()
	for _, id := range ids {
		e.stop(id)
	}
}
func (e *Engine) ensure(b apps.PinBinding) error {
	if b.Mode == apps.ModeCustom {
		return e.ensureCustom(b)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.ctx.Err() != nil {
		return e.ctx.Err()
	}
	if existing, exists := e.live[b.Socket]; exists {
		old, newBinding := existing.binding, b
		old.Revision = 0
		newBinding.Revision = 0
		if reflect.DeepEqual(old, newBinding) {
			return nil
		}
		existing.stop()
		e.cfg.Registry.Stop(b.Socket)
		delete(e.live, b.Socket)
		delete(e.allowed, b.Socket)
	}
	root := e.cfg.Discovery.PackagesDir
	if root == "" {
		root = "/var/packages"
	}
	providerName := "tailscale"
	if b.Scope == apps.Public {
		providerName = "cloudflared"
	}
	state := apps.StorePinState{Store: e.cfg.Store, ProjectionPath: e.cfg.ProjectionPath, Cleanup: func(ctx context.Context, b apps.PinBinding) error {
		return e.Remove(ctx, apps.Target{Package: b.Package, Kind: b.Scope, Scope: apps.Scope{Socket: b.Socket}})
	}}
	p, err := apps.StartPinProxy(e.ctx, apps.PinConfig{Binding: b, Provider: providerName, InfoPath: filepath.Join(root, b.Package, "INFO"), GatewayJSON: filepath.Join(root, b.Package, "target", "sso", "gateway.json"), ProtectionDigest: b.Summary, State: state, Verifier: e.cfg.Verifier, Authorize: e.accessAuthorizer(b)})
	if err != nil {
		return err
	}
	e.live[b.Socket] = running{b, p.Stop}
	e.cfg.Registry.Set(b.Socket, p)
	e.wg.Add(1)
	go func() { defer e.wg.Done(); _ = p.Wait(); p.Stop() }()
	return nil
}

func (e *Engine) Start(ctx context.Context, state apps.State) error {
	e.stopAll()
	e.mu.Lock()
	e.recovering = true
	e.mu.Unlock()
	for _, b := range apps.PinBindings(state) {
		if err := e.ensure(b); err != nil {
			return err
		}
	}
	e.restoreDSM(ctx, state)
	return nil
}

// DSM endpoints are independent approvals, including when there are no Apps.
// Recheck retained trust without creating an endpoint or changing its pin.
// Transient startup failures are retried by Check, never persisted as revocation.
func (e *Engine) restoreDSM(ctx context.Context, state apps.State) {
	for _, b := range apps.PinBindings(state) {
		if b.Target != apps.TargetDSM {
			continue
		}
		if e.verifyRetainedDSM(ctx, state.DSM, b.Scope, b.Authority) != nil {
			continue
		}
		if e.ensure(b) == nil {
			e.mu.Lock()
			e.allowed[b.Socket] = true
			e.mu.Unlock()
		}
	}
}
func (e *Engine) activateProxies(t apps.Target) error {
	state, err := e.cfg.Store.Load()
	if err != nil {
		return err
	}
	e.pruneListeners(state)
	for _, b := range apps.PinBindings(state) {
		if b.Scope == t.Kind && (b.Package == t.Package || b.Target == apps.TargetDSM) {
			if err = e.ensure(b); err != nil {
				return err
			}
			e.mu.Lock()
			e.allowed[b.Socket] = true
			e.mu.Unlock()
		}
	}
	return nil
}
func (e *Engine) PrepareDNS(context.Context, apps.Target) error { return apps.ErrUnavailable }
func (e *Engine) Activate(ctx context.Context, t apps.Target) error {
	if err := e.activateProxies(t); err != nil {
		return err
	}
	e.mu.Lock()
	recovering := e.recovering
	e.mu.Unlock()
	if recovering {
		return nil
	}
	return e.Remove(ctx, apps.Target{Kind: t.Kind}) // Reconcile from current durable state, including the new scope.
}
func (e *Engine) Block(_ context.Context, t apps.Target) error {
	if t.Scope.Socket != "" {
		e.stop(t.Scope.Socket)
	}
	return nil
}
func (e *Engine) Remove(ctx context.Context, t apps.Target) error {
	if t.Kind == apps.Public && e.cfg.Workers != nil {
		if err := e.cfg.Workers.Reconcile(e.ctx, e.workerBindings()); err != nil {
			return err
		}
	}
	signature := e.routeSignature(t.Kind)
	e.mu.Lock()
	applied := e.applied[t.Kind]
	e.mu.Unlock()
	if applied == signature {
		return e.cleanupRemoved(ctx, t)
	}
	var err error
	if t.Kind == apps.Private {
		if e.cfg.Private != nil {
			err = e.cfg.Private.RefreshApps(ctx)
		}
	} else if e.cfg.Public != nil && e.domainPublicMode() {
		if _, readErr := e.cfg.Public.AppDomain(); readErr == nil {
			err = e.cfg.Public.ReconcileAppRoutes(ctx)
		}
	}
	if err == nil {
		e.markApplied(t.Kind, signature)
		err = e.cleanupRemoved(ctx, t)
	}
	return err
}

// Removal is durable intent, including after restart without a live proxy.
// Pauses and failed explicit Clears are excluded from automatic cloud deletion.
func (e *Engine) cleanupRemoved(ctx context.Context, t apps.Target) error {
	if t.Kind != apps.Public || t.Package == "" {
		return nil
	}
	state, err := e.cfg.Store.Load()
	if err != nil {
		return err
	}
	scope := state.Apps[t.Package].Public
	if scope == nil || scope.State != apps.Paused || scope.Reason != "removed" || !scope.Cleanup || t.Scope.Socket != "" && t.Scope.Socket != scope.Socket {
		return nil
	}
	if scope.Provider == apps.Workers {
		if e.cfg.Workers == nil {
			return apps.ErrUnavailable
		}
		return e.cfg.Workers.DeleteApp(ctx, t.Package)
	}
	if e.cfg.Public == nil {
		return apps.ErrUnavailable
	}
	return e.cfg.Public.DeleteAppRoute(ctx, scope.Authority)
}

func (e *Engine) Prune(ctx context.Context, state apps.State) error {
	// Keep waiting active proxies alive so their bounded mismatch timers survive.
	// They never gain a provider route before a successful fresh validation.
	durable, err := e.cfg.Store.Load()
	if err != nil {
		durable = apps.EmptyState()
	}
	e.pruneListeners(durable)
	e.mu.Lock()
	e.recovering = false
	e.mu.Unlock()
	errs := []error{e.Remove(ctx, apps.Target{Kind: apps.Private}), e.Remove(ctx, apps.Target{Kind: apps.Public})}
	for _, t := range apps.Targets(durable) {
		if t.Kind == apps.Public && t.Scope.State == apps.Paused && t.Scope.Reason == "removed" && t.Scope.Cleanup {
			errs = append(errs, e.cleanupRemoved(ctx, t))
		}
	}
	return errors.Join(errs...)
}

func (e *Engine) pruneListeners(state apps.State) {
	wanted := map[string]bool{}
	for _, b := range apps.PinBindings(state) {
		wanted[b.Socket] = true
	}
	e.mu.Lock()
	ids := []string{}
	for id := range e.live {
		if !wanted[id] {
			ids = append(ids, id)
		}
	}
	e.mu.Unlock()
	for _, id := range ids {
		e.stop(id)
	}
}
func (e *Engine) routeSignature(kind string) string {
	state, err := e.cfg.Store.Load()
	if err != nil {
		state = apps.EmptyState()
	}
	ids := []string{}
	for _, b := range apps.PinBindings(state) {
		e.mu.Lock()
		allowed := e.allowed[b.Socket]
		e.mu.Unlock()
		if b.Scope == kind && allowed {
			ids = append(ids, b.Socket+":"+b.Authority+":"+strconv.Itoa(b.Port)+":"+b.EntryPath)
		}
	}
	sort.Strings(ids)
	raw, _ := json.Marshal(ids)
	return string(raw)
}
func (e *Engine) markApplied(kind, signature string) {
	e.mu.Lock()
	e.applied[kind] = signature
	e.mu.Unlock()
}
func (e *Engine) PrivateMappings(ctx context.Context) ([]provider.PortMapping, error) {
	state, err := e.cfg.Store.Load()
	if err != nil {
		return []provider.PortMapping{}, nil
	} // Corruption withdraws every App route.
	out := []provider.PortMapping{}
	for _, b := range apps.PinBindings(state) {
		if b.Scope != apps.Private {
			continue
		}
		e.mu.Lock()
		allowed := e.allowed[b.Socket]
		e.mu.Unlock()
		if !allowed {
			continue
		}
		_, portText, err := net.SplitHostPort(b.Authority)
		if err != nil {
			return nil, err
		}
		port, err := strconv.Atoi(portText)
		if err != nil {
			return nil, err
		}
		out = append(out, provider.PortMapping{LocalPort: b.Port, PublicPort: port, Protocol: "unix", Socket: apps.PinSocket(b.Socket)})
	}
	return out, ctx.Err()
}

func (e *Engine) ActivePublic(ctx context.Context) ([]apps.Target, error) {
	state, err := e.cfg.Store.Load()
	if err != nil {
		return []apps.Target{}, nil
	}
	out := []apps.Target{}
	for _, t := range apps.Targets(state) {
		e.mu.Lock()
		allowed := e.allowed[t.Scope.Socket]
		e.mu.Unlock()
		if t.Kind == apps.Public && t.Scope.Provider != apps.Workers && t.Scope.State == apps.Active && allowed {
			if def := state.Custom[t.Package]; def.Ingress != nil {
				t.Path = def.Ingress.Path
				t.AdditionalPaths = append([]string(nil), def.Ingress.AdditionalPaths...)
			}
			out = append(out, t)
		}
	}
	return out, nil
}

// AppExists keeps custom definitions and their pending removal intent, while
// installed Apps follow the package directory itself. The directory survives
// an upgrade even when gateway metadata is briefly absent; only a confirmed
// missing package entry permits orphan cleanup. Other filesystem errors fail
// closed.
func (e *Engine) AppExists(_ context.Context, pkg string) bool {
	state, err := e.cfg.Store.Load()
	if err != nil {
		return true // Corrupt state is never authority to delete cloud resources.
	}
	if apps.IsCustom(pkg) {
		_, defined := state.Custom[pkg]
		_, pending := state.Apps[pkg]
		return defined || pending
	}
	if !filepath.IsLocal(pkg) {
		return true
	}
	root := e.cfg.Discovery.PackagesDir
	if root == "" {
		root = "/var/packages"
	}
	_, err = os.Lstat(filepath.Join(root, pkg))
	return err == nil || !errors.Is(err, os.ErrNotExist)
}

func (e *Engine) ActivatePublic(ctx context.Context, targets []apps.Target) error {
	intent, _ := ctx.Value(batchContextKey{}).(batchIntent)
	for _, t := range targets {
		if err := e.validateLocal(ctx, t); err != nil {
			return err
		}
	}
	if intent.dsm != nil {
		if err := e.ValidateDSM(ctx, intent.dsm); err != nil {
			return err
		}
	}
	err := e.cfg.Store.Update(ctx, func(state *apps.State) error {
		if intent.expectedRevision == 0 || state.NextRev != intent.expectedRevision {
			return apps.ErrConfirmationChanged
		}
		if intent.dsm != nil {
			state.DSM = intent.dsm
		}
		for _, t := range targets {
			old := state.Apps[t.Package].Public
			if old != nil && old.State == apps.Active {
				return apps.ErrConfirmationChanged
			}
			rev, err := state.Revision()
			if err != nil {
				return err
			}
			app := state.Apps[t.Package]
			app.Rev = rev
			scope := t.Scope
			app.Public = &scope
			state.Apps[t.Package] = app
			state.RememberCustomPublication(t.Package, &scope)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, t := range targets {
		if err = e.activateProxies(t); err != nil {
			return err
		}
	}
	state, err := e.cfg.Store.Load()
	if err != nil {
		return err
	}
	return apps.WriteProjection(e.cfg.ProjectionPath, state)
}
func (e *Engine) PausePublic(ctx context.Context, targets []apps.Target, reason string) error {
	err := e.cfg.Store.Update(ctx, func(state *apps.State) error {
		for _, t := range targets {
			app := state.Apps[t.Package]
			if app.Public == nil || app.Public.Socket != t.Scope.Socket {
				continue
			}
			rev, err := state.Revision()
			if err != nil {
				return err
			}
			app.Rev = rev
			app.Public.State = apps.Paused
			app.Public.Reason = reason
			app.Public.Cleanup = true
			state.Apps[t.Package] = app
		}
		return nil
	})
	for _, t := range targets {
		_ = e.Block(ctx, t)
	}
	if err != nil {
		return err
	}
	state, err := e.cfg.Store.Load()
	if err != nil {
		return err
	}
	return apps.WriteProjection(e.cfg.ProjectionPath, state)
}

// ClosePublic withdraws a multi-selection with one provider reconciliation.
func (e *Engine) ClosePublic(ctx context.Context, packages []string) (apps.Mutation, error) {
	if len(packages) == 0 || len(packages) > 64 {
		return apps.Mutation{}, apps.ErrInvalid
	}
	targets := []apps.Target{}
	err := e.cfg.Store.Update(ctx, func(state *apps.State) error {
		for _, pkg := range packages {
			app := state.Apps[pkg]
			if app.Public == nil {
				continue
			}
			state.RememberCustomPublication(pkg, app.Public)
			targets = append(targets, apps.Target{Package: pkg, Kind: apps.Public, Scope: *app.Public})
			rev, err := state.Revision()
			if err != nil {
				return err
			}
			app.Rev = rev
			app.Public = nil
			if app.Private == nil {
				delete(state.Apps, pkg)
			} else {
				state.Apps[pkg] = app
			}
		}
		return nil
	})
	if err != nil {
		return apps.Mutation{}, err
	}
	for _, t := range targets {
		_ = e.Block(ctx, t)
	}
	state, err := e.cfg.Store.Load()
	if err != nil {
		return apps.Mutation{}, err
	}
	projectErr := apps.WriteProjection(e.cfg.ProjectionPath, state)
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	removeErr := e.Remove(cleanup, apps.Target{Kind: apps.Public})
	out := apps.Mutation{Results: []apps.Result{}}
	for _, pkg := range packages {
		r := apps.Result{Package: pkg, OK: removeErr == nil}
		if removeErr != nil {
			r.Error = "apps_cleanup_required"
		}
		out.Results = append(out.Results, r)
	}
	return out, errors.Join(projectErr, removeErr)
}

func (e *Engine) observe(ctx context.Context, b apps.PinBinding) (apps.PinObservation, error) {
	out := apps.PinObservation{Ports: map[string]int{}, Paths: map[string]string{}}
	port, err := e.dsmHTTPSPort(ctx)
	if err != nil {
		return out, err
	}
	out.DSMPort = port
	if b.Scope == apps.Private && e.cfg.Private != nil {
		n, err := e.cfg.Private.AppAccessNetwork(ctx)
		if err != nil || !n.Available {
			return out, apps.ErrUnavailable
		}
		out.MagicDNS = n.Host
	}
	root := e.cfg.Discovery.PackagesDir
	if root == "" {
		root = "/var/packages"
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return out, err
	}
	for _, entry := range entries {
		g, err := e.cfg.Discovery.ReadGateway(entry.Name())
		if err == nil {
			out.Ports[g.Package] = g.Listen.HTTPS
			out.Paths[g.Package] = g.IngressPath
			if g.IngressPath != "" {
				out.Ports[g.Package] = port
			}
			if g.Package == b.Package {
				_, out.ProtectionDigest = apps.ProtectionOf(g)
			}
		}
	}
	return out, nil
}

// InUse reports whether the safety checks have anything to check: a live
// proxy, a recorded App URL or DSM approval, or records that cannot be read.
func (e *Engine) InUse() bool {
	e.mu.Lock()
	live := len(e.live)
	e.mu.Unlock()
	if live > 0 {
		return true
	}
	state, err := e.cfg.Store.Load()
	if err != nil || state.DSM != nil && (state.DSM.Public != nil || state.DSM.Private != nil) {
		return true
	}
	for _, t := range apps.Targets(state) {
		if state.Custom[t.Package].Ingress == nil || t.Scope.State == apps.Active {
			return true
		}
	}
	return false
}

// Check joins refusal checks and retries provider cleanup without restoring a
// paused/revoked scope. Active targets that were waiting at startup can return.
func (e *Engine) Check(ctx context.Context) error {
	entryErr := e.FollowEntries(ctx)
	checkErr := errors.Join(entryErr, e.cfg.Registry.Check(ctx))
	state, err := e.cfg.Store.Load()
	if err != nil {
		e.stopAll()
		empty := apps.EmptyState()
		return errors.Join(checkErr, e.Prune(ctx, empty), apps.WriteProjection(e.cfg.ProjectionPath, empty), err)
	}
	e.restoreDSM(ctx, state)
	for _, t := range apps.Targets(state) {
		if t.Scope.State != apps.Active {
			continue
		}
		if e.Validate(ctx, t) == nil {
			_ = e.activateProxies(t)
		}
	}
	err = e.Prune(ctx, state)
	if err == nil {
		needsSave := false
		for _, t := range apps.Targets(state) {
			needsSave = needsSave || t.Scope.Cleanup
		}
		if needsSave {
			err = e.cfg.Store.Update(ctx, func(current *apps.State) error {
				for pkg, app := range current.Apps {
					if app.Rev != state.Apps[pkg].Rev {
						continue
					}
					for _, kind := range []string{apps.Private, apps.Public} {
						if scope := app.Scope(kind); scope != nil && scope.State == apps.Paused && scope.Reason != "worker-delete-failed" {
							scope.Cleanup = false
						}
					}
					current.Apps[pkg] = app
				}
				current.DropRemovedCustom()
				return nil
			})
		}
	}
	return errors.Join(checkErr, err)
}

func (e *Engine) accessAuthorizer(b apps.PinBinding) func(*http.Request) error {
	if b.Scope != apps.Public || e.cfg.Access == nil {
		return nil
	}
	return func(r *http.Request) error {
		return e.cfg.Access.Verify(r.Context(), b.Authority, r.Header.Get("Cf-Access-Jwt-Assertion"))
	}
}

func (e *Engine) FollowEntries(ctx context.Context) error {
	e.entryMu.Lock()
	defer e.entryMu.Unlock()
	return apps.FollowEntries(ctx, e.Lifecycle(), e.cfg.Discovery)
}

func privateEntryPort(pkg string, state apps.State, dsmPort int) int {
	digest := sha256.Sum256([]byte(pkg))
	start := 30000 + (int(digest[0])<<8|int(digest[1]))%10000
	used := map[int]bool{dsmPort: true}
	for _, b := range apps.PinBindings(state) {
		if b.Scope == apps.Private {
			_, p, e := net.SplitHostPort(b.Authority)
			if e == nil {
				port, _ := strconv.Atoi(p)
				used[port] = true
			}
		}
	}
	for i := 0; i < 10000; i++ {
		port := 30000 + (start-30000+i)%10000
		if !used[port] {
			return port
		}
	}
	return 0
}
