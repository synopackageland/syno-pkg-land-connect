package apps

import (
	"context"
	"dsmapp/internal/connector"
	"errors"
	"reflect"
	"sort"
	"strings"
)

type ScopeView struct {
	EntryChange *EntryChange `json:"entryChange,omitempty"`
	Provider    string       `json:"provider,omitempty"`
	State       string       `json:"state"`
	Reason      string       `json:"reason,omitempty"`
	URL         string       `json:"url"`
	Reachable   bool         `json:"reachable"`
	Cleanup     bool         `json:"cleanup,omitempty"`
}
type PrivateAvailability struct {
	Available bool   `json:"available"`
	Code      string `json:"code,omitempty"`
	Host      string `json:"host,omitempty"`
}
type PublicAvailability struct {
	Available      bool   `json:"available"`
	Enabled        bool   `json:"enabled"`
	Connected      bool   `json:"connected"`
	Zone           string `json:"zone,omitempty"`
	Invalid        bool   `json:"invalid,omitempty"`
	OrphanedRoutes int    `json:"orphanedRoutes,omitempty"`
}
type Availability struct {
	HasWorkers bool                `json:"-"`
	Starting   bool                `json:"-"`
	Workers    WorkersStatus       `json:"workers"`
	Private    PrivateAvailability `json:"private"`
	Public     PublicAvailability  `json:"public"`
}
type List struct {
	EntryActivity []EntryActivity     `json:"entryActivity,omitempty"`
	TrustedDSM    *TrustedCertificate `json:"trustedDsm,omitempty"`
	Relay         *WorkersStatus      `json:"relay,omitempty"`
	Workers       WorkersStatus       `json:"workers"`
	Apps          []App               `json:"apps"`
	Private       PrivateAvailability `json:"private"`
	Public        PublicAvailability  `json:"public"`
	StateCorrupt  bool                `json:"stateCorrupt,omitempty"`
	// Starting means connections are still restoring; App URLs wait for them.
	Starting bool `json:"starting,omitempty"`
}
type LoginResult struct {
	LoginURL string `json:"loginUrl,omitempty"`
}

// ProviderAccess is the provider owner's App-page integration. A missing
// implementation reports unavailable and never invents a connected provider.
type ProviderAccess interface {
	Availability(context.Context) (Availability, error)
	CloudflareConnect(context.Context) (LoginResult, error)
	CloudflareDisconnect(context.Context) error
}

type OrphanCleanup struct {
	Removed int `json:"removed"`
}

type OrphanCleanupAPI interface {
	CleanupPublicOrphans(context.Context) (OrphanCleanup, error)
}

func (s *Service) CleanupPublicOrphans(ctx context.Context) (OrphanCleanup, error) {
	p, ok := s.Provider.(OrphanCleanupAPI)
	if !ok {
		return OrphanCleanup{}, ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return p.CleanupPublicOrphans(ctx)
}

type InspectRequest struct {
	ReuseName   *connector.NameReuse `json:"reuseName,omitempty"`
	DSMHostname string               `json:"dsmHostname,omitempty"`
	Hostname    string               `json:"hostname,omitempty"`
	Provider    string               `json:"provider,omitempty"`
	Package     string               `json:"package"`
	Scope       string               `json:"scope"`
}
type DSMApproval struct {
	Hostname string      `json:"hostname,omitempty"`
	Port     int         `json:"port"`
	Pin      Certificate `json:"pin"`
}
type Inspection struct {
	Entry      *PackageEntry        `json:"entry,omitempty"`
	ReuseName  *connector.NameReuse `json:"reuseName,omitempty"`
	Hostname   string               `json:"hostname,omitempty"`
	Ready      bool                 `json:"ready"`
	Package    string               `json:"package"`
	Name       string               `json:"name"`
	Port       int                  `json:"port"`
	Protection Protection           `json:"protection"`
	Pin        *Certificate         `json:"pin,omitempty"`
	DSM        *DSMApproval         `json:"dsm,omitempty"`
	Token      string               `json:"token,omitempty"`
	Custom     *CustomView          `json:"custom,omitempty"`
}
type Approved struct {
	ReuseName *connector.NameReuse `json:"reuseName,omitempty"`
	Hostname  string               `json:"hostname,omitempty"`
	Port      int                  `json:"port"`
	Pin       Certificate          `json:"pin"`
	DSM       *DSMApproval         `json:"dsm,omitempty"`
	Token     string               `json:"token"`
}
type PrivateRequest struct {
	Package  string    `json:"package"`
	Enabled  bool      `json:"enabled"`
	Approval *Approved `json:"approval,omitempty"`
}
type PublicRequest struct {
	Provider  string              `json:"provider,omitempty"`
	Packages  []string            `json:"packages"`
	Enabled   bool                `json:"enabled"`
	Approvals map[string]Approved `json:"approvals,omitempty"`
}

// DSMInspectRequest asks for the DSM-only approval of one scope.
type DSMInspectRequest struct {
	Hostname string `json:"hostname,omitempty"`
	Provider string `json:"provider,omitempty"`
	Scope    string `json:"scope"`
}

// DSMInspection is the DSM-only approval. DSM is nil when the current
// approval for this scope still verifies and nothing needs review.
type DSMInspection struct {
	Hostname string       `json:"hostname,omitempty"`
	Scope    string       `json:"scope"`
	DSM      *DSMApproval `json:"dsm,omitempty"`
	Token    string       `json:"token,omitempty"`
}

// DSMRequest confirms the DSM certificate the administrator saw.
type DSMRequest struct {
	Hostname string       `json:"hostname,omitempty"`
	Provider string       `json:"provider,omitempty"`
	Scope    string       `json:"scope"`
	DSM      *DSMApproval `json:"dsm,omitempty"`
	Token    string       `json:"token"`
}
type DSMResult struct {
	Approved bool `json:"approved"`
}
type ReenableRequest struct {
	Provider string    `json:"provider,omitempty"`
	Package  string    `json:"package"`
	Scope    string    `json:"scope"`
	Approval *Approved `json:"approval,omitempty"`
}

func (s *Service) List(ctx context.Context) (List, error) {
	snapshot, err := s.snapshot(ctx)
	if err != nil {
		return List{}, err
	}
	out := List{EntryActivity: snapshot.State.EntryActivity, Apps: snapshot.Apps, StateCorrupt: snapshot.NeedsReopen, Private: PrivateAvailability{Code: "tailscale-off"}}
	if snapshot.State.DSM != nil {
		out.TrustedDSM = snapshot.State.DSM.Certificate
	}
	if s.Provider != nil {
		availability, e := s.Provider.Availability(ctx)
		if e != nil {
			return List{}, e
		}
		out.Private = availability.Private
		out.Public = availability.Public
		out.Workers = availability.Workers
		out.Starting = availability.Starting
		if availability.HasWorkers {
			relay := out.Workers
			out.Relay = &relay
		}
	}
	if s.Approver == nil || s.Lifecycle.Runtime == nil || out.StateCorrupt {
		out.Private.Available = false
		out.Public.Available = false
	}
	seen := map[string]bool{}
	overlay := func(app *App, state AppState) {
		for _, kind := range []string{Private, Public} {
			scope := state.Scope(kind)
			if scope == nil {
				continue
			}
			view := &ScopeView{Provider: scope.Provider, State: scope.State, Reason: scope.Reason, URL: "https://" + scope.Authority, Reachable: app.PingStatus == Ready && !app.Removed, Cleanup: scope.Cleanup}
			if app.Entry != nil && (scope.Binding.Port != app.Entry.Port || EntryPath(scope.Binding.Entry) != app.Entry.Path || scope.Binding.Summary != app.Summary) {
				before := PackageEntry{Package: app.Package, Port: scope.Binding.Port}
				if scope.Binding.Entry != nil {
					before = *scope.Binding.Entry
				}
				view.EntryChange = &EntryChange{BeforeProtection: scope.Binding.Protection, AfterProtection: &app.Protection, Before: before, After: *app.Entry, Reason: scope.Reason}
			}
			if kind == Private {
				app.Private = view
			} else {
				if scope.Provider == Workers {
					app.Relay = view
				} else {
					app.Public = view
				}
			}
		}
	}
	for i := range out.Apps {
		app := &out.Apps[i]
		switch strings.ToLower(app.Package) {
		case "synopkgjellyfin":
			app.Media = "high"
		case "synopkgaudiobookshelf":
			app.Media = "medium"
		}
		seen[app.Package] = true
		overlay(app, snapshot.State.Apps[app.Package])
	}
	for pkg, state := range snapshot.State.Apps {
		if seen[pkg] {
			continue
		}
		app := App{Package: pkg, Name: s.Discovery.displayName(pkg), Icon: s.Discovery.icon(pkg), PingStatus: Waiting, Waiting: true}
		if scope := state.Public; scope != nil {
			app.Port = scope.Binding.Port
		} else if scope := state.Private; scope != nil {
			app.Port = scope.Binding.Port
		}
		// Only the runtime's two-poll removal decision is durable evidence.
		// Missing INFO/gateway during an upgrade remains waiting here.
		for _, kind := range []string{Public, Private} {
			if scope := state.Scope(kind); scope != nil && scope.Reason == "removed" {
				app.Removed = true
			}
		}
		if IsCustom(pkg) {
			// Removed by the administrator; the row stays until its cleanup is done.
			app.Name, app.Custom, app.Removed = strings.TrimPrefix(pkg, CustomPrefix), &CustomView{}, true
		} else if gateway, e := s.Discovery.ReadGateway(pkg); e == nil {
			app.Name = s.Discovery.displayName(pkg)
			app.Protection, app.Summary = ProtectionOf(gateway)
		}
		overlay(&app, state)
		out.Apps = append(out.Apps, app)
	}
	if d := snapshot.State.DSM; d != nil && d.Public != nil {
		row := App{DSM: true, Package: "dsm", Name: "DSM", Port: d.Port, PublicCapable: true, PingStatus: Ready, Protection: Protection{UpstreamAccess: "public"}}
		scope := &ScopeView{State: Active, URL: "https://" + d.Public.Authority, Reachable: true}
		if d.Public.Provider == Workers {
			row.Relay = scope
		} else {
			row.Public = scope
		}
		out.Apps = append(out.Apps, row)
	}
	sort.Slice(out.Apps, func(i, j int) bool { return out.Apps[i].Package < out.Apps[j].Package })
	return out, nil
}

func (s *Service) Inspect(ctx context.Context, in InspectRequest) (Inspection, error) {
	if in.Scope == "relay" {
		if in.Provider != "" && in.Provider != Workers {
			return Inspection{}, ErrInvalid
		}
		in.Scope = Public
		in.Provider = Workers
	}
	if !ValidProvider(in.Provider) {
		return Inspection{}, ErrInvalid
	}
	ctx = connector.WithNameReuse(ctx, in.ReuseName)
	ctx = WithPublicProvider(ctx, in.Provider)
	names := map[string]string{in.Package: in.Hostname, "": in.DSMHostname}
	if in.Package == "dsm" {
		names[""] = in.Hostname
	}
	ctx = WithHostnames(ctx, names)
	if !packagePattern.MatchString(in.Package) || (in.Scope != Private && in.Scope != Public) {
		return Inspection{}, ErrInvalid
	}
	if in.Package == "dsm" {
		return s.inspectDSMApp(ctx, in.Scope)
	}
	state, err := s.Lifecycle.Store.Load()
	if err != nil {
		return Inspection{}, err
	}
	old := state.Apps[in.Package].Scope(in.Scope)
	result, err := s.open(ctx, []string{in.Package}, in.Scope, "", old != nil && old.State == Paused)
	if errors.Is(err, ErrNotReady) {
		apps, e := s.Discovery.Scan(ctx)
		if e != nil {
			return Inspection{}, e
		}
		for _, app := range apps {
			if app.Package == in.Package {
				return Inspection{Package: app.Package, Name: app.Name, Port: app.Port, Protection: app.Protection}, nil
			}
		}
	}
	if err != nil {
		return Inspection{}, err
	}
	approval := result.Confirmation.Approvals[0]
	if in.Scope == Public && in.Hostname != "" {
		scope := approval.Scope
		scope.Socket = "preview"
		if err = s.Approver.CheckPublic(ctx, []Target{{Package: in.Package, Kind: Public, Scope: scope}}); err != nil {
			return Inspection{}, err
		}
	}
	return Inspection{Entry: approval.App.Entry, ReuseName: approval.ReuseName, Hostname: approval.Scope.Authority, Ready: true, Package: approval.App.Package, Name: approval.App.Name, Port: approval.App.Port, Protection: approval.App.Protection, Pin: &approval.Certificate, DSM: approval.DSM, Token: result.Confirmation.Token, Custom: approval.App.Custom}, nil
}

// dsmPreview is the runtime's fresh DSM-only proposal and the revision it
// binds to. The caller holds s.mu.
func (s *Service) dsmPreview(ctx context.Context, kind string) (Approval, uint64, DSMApprover, error) {
	runtime, ok := s.Lifecycle.Runtime.(DSMApprover)
	if kind != Private && kind != Public {
		return Approval{}, 0, nil, ErrInvalid
	}
	if !ok || s.Approver == nil {
		return Approval{}, 0, nil, ErrUnavailable
	}
	state, err := s.Lifecycle.Store.Load()
	if err != nil {
		return Approval{}, 0, nil, err
	}
	approval, err := runtime.InspectDSM(ctx, kind)
	approval.Kind = kind
	return approval, state.NextRev, runtime, err
}

// InspectDSM shows the DSM certificate a provider needs approved when no App
// approval carried it, for example DSM-only remote access or an upgrade.
func (s *Service) InspectDSM(ctx context.Context, in DSMInspectRequest) (DSMInspection, error) {
	if in.Scope == "relay" {
		if in.Provider != "" && in.Provider != Workers {
			return DSMInspection{}, ErrInvalid
		}
		in.Scope = Public
		in.Provider = Workers
	}
	if !ValidProvider(in.Provider) {
		return DSMInspection{}, ErrInvalid
	}
	ctx = WithPublicProvider(ctx, in.Provider)
	ctx = WithHostnames(ctx, map[string]string{"": in.Hostname})
	s.mu.Lock()
	defer s.mu.Unlock()
	approval, revision, _, err := s.dsmPreview(ctx, in.Scope)
	if err != nil {
		return DSMInspection{Scope: in.Scope}, err
	}
	if approval.DSM == nil {
		if r, ok := s.Approver.(interface {
			RefreshDSM(context.Context, string) error
		}); ok {
			err = r.RefreshDSM(ctx, in.Scope)
		}
		return DSMInspection{Scope: in.Scope}, err
	}
	return DSMInspection{Hostname: approval.DSM.Hostname, Scope: in.Scope, DSM: approval.DSM, Token: confirmationToken(revision, []Approval{approval})}, nil
}

// SetDSM persists exactly the DSM approval that was displayed, bound to the
// global revision; a changed certificate, port or revision needs a new review.
func (s *Service) SetDSM(ctx context.Context, in DSMRequest) (DSMResult, error) {
	if in.Scope == "relay" {
		if in.Provider != "" && in.Provider != Workers {
			return DSMResult{}, ErrInvalid
		}
		in.Scope = Public
		in.Provider = Workers
	}
	if !ValidProvider(in.Provider) {
		return DSMResult{}, ErrInvalid
	}
	ctx = WithPublicProvider(ctx, in.Provider)
	ctx = WithHostnames(ctx, map[string]string{"": in.Hostname})
	if in.DSM == nil || in.Token == "" {
		return DSMResult{}, ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	approval, revision, runtime, err := s.dsmPreview(ctx, in.Scope)
	if err != nil {
		return DSMResult{}, err
	}
	if approval.DSM == nil || !reflect.DeepEqual(*in.DSM, *approval.DSM) || in.Token != confirmationToken(revision, []Approval{approval}) {
		return DSMResult{}, ErrConfirmationChanged
	}
	approval.ExpectedRevision = revision
	if err = runtime.OpenDSM(ctx, approval); err != nil {
		return DSMResult{}, err
	}
	return DSMResult{Approved: true}, nil
}

func (s *Service) approved(ctx context.Context, packages []string, kind string, approved map[string]Approved, reenable bool) (Mutation, error) {
	names := map[string]string{}
	for pkg, a := range approved {
		names[pkg] = a.Hostname
		ctx = connector.WithNameReuse(ctx, a.ReuseName)
		if a.DSM != nil && a.DSM.Hostname != "" {
			if names[""] != "" && names[""] != a.DSM.Hostname {
				return Mutation{}, ErrInvalid
			}
			names[""] = a.DSM.Hostname
		}
	}
	ctx = WithHostnames(ctx, names)
	preview, err := s.open(ctx, packages, kind, "", reenable)
	if err != nil {
		return Mutation{}, err
	}
	if len(approved) != len(packages) {
		return Mutation{}, ErrInvalid
	}
	// The batch token includes all Apps; individual inspections use one-App
	// tokens. Revalidate each independent snapshot before the batch is opened.
	for _, item := range preview.Confirmation.Approvals {
		given, ok := approved[item.App.Package]
		if !ok || given.Port != item.App.Port || !reflect.DeepEqual(given.Pin, item.Certificate) || !reflect.DeepEqual(given.DSM, item.DSM) {
			return Mutation{}, ErrConfirmationChanged
		}
		if given.Token == "" || given.Token != confirmationToken(preview.Confirmation.Revision, []Approval{item}) {
			return Mutation{}, ErrConfirmationChanged
		}
	}
	return s.open(ctx, packages, kind, preview.Confirmation.Token, reenable)
}

func (s *Service) SetPrivate(ctx context.Context, in PrivateRequest) (Mutation, error) {
	if !in.Enabled {
		return Mutation{}, s.clearScope(ctx, ScopedChange{Package: in.Package, Kind: Private})
	}
	if in.Approval == nil {
		return Mutation{}, ErrInvalid
	}
	out, err := s.approved(ctx, []string{in.Package}, Private, map[string]Approved{in.Package: *in.Approval}, false)
	return singleResult(out, err)
}
func (s *Service) SetPublic(ctx context.Context, in PublicRequest) (Mutation, error) {
	if !ValidProvider(in.Provider) {
		return Mutation{}, ErrInvalid
	}
	ctx = WithPublicProvider(ctx, in.Provider)
	if len(in.Packages) == 1 && in.Packages[0] == "dsm" {
		return s.setDSMApp(ctx, in)
	}
	if !in.Enabled {
		if owner, ok := s.Lifecycle.Runtime.(interface {
			ClosePublic(context.Context, []string) (Mutation, error)
		}); ok {
			s.mu.Lock()
			defer s.mu.Unlock()
			state, err := s.Lifecycle.Store.Load()
			if err != nil {
				return Mutation{}, err
			}
			if len(in.Packages) == 0 || len(in.Packages) > 64 {
				return Mutation{}, ErrInvalid
			}
			for _, pkg := range in.Packages {
				if !packagePattern.MatchString(pkg) {
					return Mutation{}, ErrInvalid
				}
			}
			out, err := owner.ClosePublic(ctx, in.Packages)
			if err != nil {
				return out, err
			}
			if runtime, ok := s.Lifecycle.Runtime.(interface {
				ClearWorker(context.Context, string, *Scope) error
			}); ok {
				for _, pkg := range in.Packages {
					previous := state.Apps[pkg].Public
					if in.Provider == Workers || previous != nil && previous.Provider == Workers {
						if e := runtime.ClearWorker(ctx, pkg, previous); e != nil {
							err = errors.Join(err, e)
							for i := range out.Results {
								if out.Results[i].Package == pkg {
									out.Results[i].OK = false
									out.Results[i].Error = ErrorCode(e)
								}
							}
						}
					}
				}
			}
			return out, err
		}
		out := Mutation{Results: []Result{}}
		if len(in.Packages) == 0 || len(in.Packages) > 64 {
			return out, ErrInvalid
		}
		for _, pkg := range in.Packages {
			err := s.clearScope(ctx, ScopedChange{Package: pkg, Kind: Public})
			r := Result{Package: pkg, OK: err == nil}
			if err != nil {
				r.Error = ErrorCode(err)
			}
			out.Results = append(out.Results, r)
		}
		return out, nil
	}
	return s.approved(ctx, in.Packages, Public, in.Approvals, false)
}
func (s *Service) Reenable(ctx context.Context, in ReenableRequest) (Mutation, error) {
	if in.Scope == "relay" {
		if in.Provider != "" && in.Provider != Workers {
			return Mutation{}, ErrInvalid
		}
		in.Scope = Public
		in.Provider = Workers
	}
	if !ValidProvider(in.Provider) {
		return Mutation{}, ErrInvalid
	}
	ctx = WithPublicProvider(ctx, in.Provider)
	if in.Approval == nil {
		return Mutation{}, ErrInvalid
	}
	out, err := s.approved(ctx, []string{in.Package}, in.Scope, map[string]Approved{in.Package: *in.Approval}, true)
	return singleResult(out, err)
}

func singleResult(out Mutation, err error) (Mutation, error) {
	if err == nil && len(out.Results) == 1 && !out.Results[0].OK {
		return out, ErrUnavailable
	}
	return out, err
}
func (s *Service) Clear(ctx context.Context, in ScopedChange) error {
	if !packagePattern.MatchString(in.Package) || (in.Kind != "" && in.Kind != Public && in.Kind != Private) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.Lifecycle.Store.Load()
	if err != nil {
		return err
	}
	previous := state.Apps[in.Package].Public
	if in.Kind == "" {
		err = errors.Join(s.Lifecycle.Close(ctx, in.Package, Private, ""), s.Lifecycle.Close(ctx, in.Package, Public, ""))
	} else {
		err = s.Lifecycle.Close(ctx, in.Package, in.Kind, "")
	}
	if err != nil {
		return err
	}
	if in.Kind != Private {
		if runtime, ok := s.Lifecycle.Runtime.(interface {
			ClearWorker(context.Context, string, *Scope) error
		}); ok {
			return runtime.ClearWorker(ctx, in.Package, previous)
		}
	}
	return nil
}

func (s *Service) CloudflareConnect(ctx context.Context) (LoginResult, error) {
	if s.Provider == nil {
		return LoginResult{}, ErrUnavailable
	}
	return s.Provider.CloudflareConnect(ctx)
}
func (s *Service) CloudflareDisconnect(ctx context.Context) error {
	if s.Provider == nil {
		return ErrUnavailable
	}
	return s.Provider.CloudflareDisconnect(ctx)
}
