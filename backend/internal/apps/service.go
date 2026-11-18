package apps

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"dsmapp/internal/connector"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
)

var (
	ErrNotReady    = errors.New("App is not ready; try again later")
	ErrNeedsUpdate = errors.New("App needs an update")
)

// Certificate is the administrator's TOFU disclosure, populated by the pin
// verifier. Issuer and names are descriptive, never independent proof.
type Certificate struct {
	Previous      *TrustedCertificate `json:"previous,omitempty"`
	Mode          string              `json:"mode"`
	Names         []string            `json:"names,omitempty"`
	PreviousNames []string            `json:"previousNames,omitempty"`
	SPKI          string              `json:"spki,omitempty"`
	PreviousSPKI  string              `json:"previousSpki,omitempty"`
	// CertSHA256 is descriptive for Apps. DSM also pins the exact leaf, so
	// renewal or replacement requires a fresh trust decision.
	CertSHA256 string `json:"certSha256,omitempty"`
	Issuer     string `json:"issuer"`
	Expires    string `json:"notAfter"`
}

type Approval struct {
	ReuseName        *connector.NameReuse `json:"reuseName,omitempty"`
	App              App                  `json:"app"`
	Kind             string               `json:"kind"`
	Scope            Scope                `json:"scope"`
	Certificate      Certificate          `json:"certificate"`
	DSM              *DSMApproval         `json:"dsm,omitempty"`
	DSMTarget        *PinBinding          `json:"-"`
	ExpectedRevision uint64               `json:"-"`
}

// ApprovedRuntime owns compound App/DSM approvals and public provider batches.
type ApprovedRuntime interface {
	OpenApproved(context.Context, []Target, []Approval, bool) (Mutation, error)
}

// DSMApprover approves DSM alone, for a provider that routes DSM without any
// App. InspectDSM returns an Approval with DSM and DSMTarget set, or neither
// when the current approval still verifies. OpenDSM persists it only while
// the global revision still equals ExpectedRevision.
type DSMApprover interface {
	InspectDSM(context.Context, string) (Approval, error)
	OpenDSM(context.Context, Approval) error
}

// Approver belongs to the daemon, alongside Runtime. Inspect must first verify
// that the target is ready and the ping matches, then derive its authority and
// certificate. It preserves the old W/S mode on reenable; a changed W name set
// is disclosed in PreviousNames. It never changes routes or state.
// CheckPublic rejects any hostname collision for the whole batch before DNS.
type Approver interface {
	Inspect(context.Context, App, string, *Scope) (Approval, error)
	CheckPublic(context.Context, []Target) error
}

type Snapshot struct {
	Apps             []App `json:"apps"`
	State            State `json:"state"`
	NeedsReopen      bool  `json:"needs_reopen"`
	RuntimeAvailable bool  `json:"runtime_available"`
}

type Confirmation struct {
	Revision  uint64     `json:"-"`
	Token     string     `json:"token"`
	Approvals []Approval `json:"approvals"`
}

type Result struct {
	Package string `json:"package"`
	State   string `json:"-"`
	Error   string `json:"code,omitempty"`
	OK      bool   `json:"ok"`
}

type Mutation struct {
	Confirmation *Confirmation `json:"confirmation,omitempty"`
	Results      []Result      `json:"results,omitempty"`
}

type Change struct {
	Package      string `json:"package"`
	Enabled      bool   `json:"enabled"`
	Confirmation string `json:"confirmation,omitempty"`
}

type PublicChange struct {
	Packages     []string `json:"packages"`
	Confirmation string   `json:"confirmation,omitempty"`
}

type ScopedChange struct {
	Package      string `json:"package"`
	Kind         string `json:"scope"`
	Confirmation string `json:"confirmation,omitempty"`
}

// Service is daemon-owned; a CGI transports only administrator actions. A list
// or unconfirmed mutation never creates DNS, listeners, state or routes.
type Service struct {
	Discovery Discovery
	Lifecycle Lifecycle
	Approver  Approver
	Provider  ProviderAccess
	// Guard checks custom App targets for the list's reachability line.
	Guard TargetGuard
	mu    sync.Mutex
}

func (s *Service) snapshot(ctx context.Context) (Snapshot, error) {
	if owner, ok := s.Lifecycle.Runtime.(interface{ FollowEntries(context.Context) error }); ok {
		_ = owner.FollowEntries(ctx) // The list must remain readable for recovery after a failed reconciliation.
	}
	apps, err := s.Discovery.ScanList(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	state, loadErr := s.Lifecycle.Store.Load()
	apps = append(apps, s.customApps(ctx, state, true)...)
	return Snapshot{Apps: apps, State: state, NeedsReopen: loadErr != nil, RuntimeAvailable: s.Approver != nil && s.Lifecycle.Runtime != nil}, nil
}

func (s *Service) changePrivate(ctx context.Context, in Change) (Mutation, error) {
	if !in.Enabled {
		return Mutation{}, s.clearScope(ctx, ScopedChange{Package: in.Package, Kind: Private})
	}
	return s.open(ctx, []string{in.Package}, Private, in.Confirmation, false)
}
func (s *Service) changePublic(ctx context.Context, in PublicChange) (Mutation, error) {
	return s.open(ctx, in.Packages, Public, in.Confirmation, false)
}
func (s *Service) changeReenable(ctx context.Context, in ScopedChange) (Mutation, error) {
	return s.open(ctx, []string{in.Package}, in.Kind, in.Confirmation, true)
}
func (s *Service) clearScope(ctx context.Context, in ScopedChange) error {
	if !packagePattern.MatchString(in.Package) || (in.Kind != Private && in.Kind != Public) {
		return ErrInvalid
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Lifecycle.Close(ctx, in.Package, in.Kind, "")
}

func (s *Service) open(ctx context.Context, packages []string, kind, token string, reenable bool) (Mutation, error) {
	if kind != Private && kind != Public || len(packages) == 0 || len(packages) > 64 {
		return Mutation{}, ErrInvalid
	}
	seen := map[string]bool{}
	for _, pkg := range packages {
		if !packagePattern.MatchString(pkg) || seen[pkg] {
			return Mutation{}, ErrInvalid
		}
		seen[pkg] = true
	}
	if s.Approver == nil || s.Lifecycle.Runtime == nil {
		return Mutation{}, ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.Lifecycle.Store.Load()
	if err != nil {
		return Mutation{}, err
	}
	apps, err := s.Discovery.Scan(ctx)
	if err != nil {
		return Mutation{}, err
	}
	apps = append(apps, s.customApps(ctx, state, false)...)
	byName := map[string]App{}
	for _, app := range apps {
		byName[app.Package] = app
	}
	approvals := []Approval{}
	for _, pkg := range packages {
		app, ok := byName[pkg]
		if !ok {
			return Mutation{}, ErrInvalid
		}
		if app.Custom != nil && kind != Public {
			return Mutation{}, errCustomPrivate
		}
		if app.PingStatus == Waiting && app.Custom == nil {
			return Mutation{}, ErrNotReady
		}
		if app.NeedsAppUpdate || kind == Public && app.PublicNeedsAppUpdate {
			return Mutation{}, ErrNeedsUpdate
		}
		previous := state.Apps[pkg].Scope(kind)
		if reenable && (previous == nil || previous.State != Paused) || !reenable && previous != nil {
			return Mutation{}, ErrInvalid
		}
		approval, e := s.Approver.Inspect(ctx, app, kind, previous)
		if e != nil {
			return Mutation{}, e
		}
		approval.ReuseName = connector.ReuseFor(ctx, approval.Scope.Authority)
		approval.App = app
		approval.Kind = kind
		approval.Scope.State = Active
		approval.Scope.Reason = ""
		approval.Scope.Cleanup = false
		// Socket IDs are allocated only after confirmation, not in previews.
		approval.Scope.Socket = ""
		if approval.Scope.Binding.Port != app.Port || approval.Scope.Binding.Summary != app.Summary {
			return Mutation{}, ErrInvalid
		}
		if previous != nil && previous.Binding.Pin.Mode != approval.Scope.Binding.Pin.Mode {
			return Mutation{}, ErrInvalid
		}
		approvals = append(approvals, approval)
	}
	// Bind the confirmation to exactly what was displayed and to the global
	// revision, so a stale browser cannot silently approve changed settings.
	confirmation := &Confirmation{Revision: state.NextRev, Token: confirmationToken(state.NextRev, approvals), Approvals: approvals}
	if token == "" {
		return Mutation{Confirmation: confirmation}, nil
	}
	if token != confirmation.Token {
		return Mutation{Confirmation: confirmation}, ErrConfirmationChanged
	}
	targets := make([]Target, len(approvals))
	for i, approval := range approvals {
		var id [16]byte
		if _, err = rand.Read(id[:]); err != nil {
			return Mutation{}, err
		}
		scope := approval.Scope
		scope.Socket = "a" + hex.EncodeToString(id[:])
		targets[i] = Target{Package: approval.App.Package, Kind: kind, Scope: scope}
	}
	if runtime, ok := s.Lifecycle.Runtime.(ApprovedRuntime); ok {
		for i := range approvals {
			approvals[i].ExpectedRevision = state.NextRev
		}
		return runtime.OpenApproved(ctx, targets, approvals, reenable)
	}
	if kind == Public && !reenable {
		if err = s.Approver.CheckPublic(ctx, targets); err != nil {
			return Mutation{}, err
		}
	}
	out := Mutation{Results: []Result{}}
	for _, target := range targets {
		err = s.Lifecycle.Open(ctx, target, reenable)
		r := Result{Package: target.Package, State: Active, OK: true}
		if err != nil {
			r.State = "failed"
			r.OK = false
			r.Error = "activation-failed"
			if p, e := s.Lifecycle.Store.Load(); e == nil {
				if scope := p.Apps[target.Package].Scope(kind); scope != nil {
					r.State = scope.State
				}
			}
		}
		out.Results = append(out.Results, r)
		// DNS may partially succeed; each successful target remains approved.
		// Return per-App results without retrying or changing the selected IDs.
	}
	return out, nil
}

func confirmationToken(revision uint64, approvals []Approval) string {
	raw, _ := json.Marshal(struct {
		Revision  uint64
		Approvals []Approval
	}{revision, approvals})
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
