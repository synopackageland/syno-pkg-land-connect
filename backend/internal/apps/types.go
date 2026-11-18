// Package apps owns administrator-approved external App bindings. Provider
// observations and the public projection never grant authority.
package apps

import (
	"errors"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const (
	Workers = "workers"
	Schema  = 1
	Active  = "active"
	Paused  = "paused"
	Public  = "public"
	Private = "private"
)

var (
	ErrCorrupt             = errors.New("App approvals are unavailable; reopen access explicitly")
	ErrInvalid             = errors.New("invalid App binding")
	ErrUnavailable         = errors.New("App access is busy or not running; try again")
	ErrCleanup             = errors.New("App access blocked; provider cleanup required")
	ErrConfirmationChanged = errors.New("App approval changed; review the current confirmation")
	packagePattern         = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)
	socketPattern          = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	hexPattern             = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

type Pin struct {
	CertSHA256 string   `json:"certSha256,omitempty"`
	Mode       string   `json:"mode"`
	Names      []string `json:"names,omitempty"`
	SPKI       string   `json:"spki,omitempty"`
}

type Binding struct {
	Protection *Protection   `json:"protection,omitempty"`
	Entry      *PackageEntry `json:"entry,omitempty"`
	Port       int           `json:"port"`
	Summary    string        `json:"summary"`
	Pin        Pin           `json:"pin"`
}

type Scope struct {
	Provider  string  `json:"provider,omitempty"`
	State     string  `json:"state"`
	Reason    string  `json:"reason"`
	Origin    string  `json:"origin,omitempty"`
	Binding   Binding `json:"binding"`
	Socket    string  `json:"socket"`
	Authority string  `json:"authority"`
	Cleanup   bool    `json:"cleanup"`
}

type AppState struct {
	Rev     uint64 `json:"rev"`
	Public  *Scope `json:"public,omitempty"`
	Private *Scope `json:"private,omitempty"`
}

type Endpoint struct {
	Provider  string `json:"provider,omitempty"`
	Socket    string `json:"socket"`
	Authority string `json:"authority"`
}

type TrustedCertificate struct {
	Issuer     string `json:"issuer"`
	Expires    string `json:"notAfter"`
	CertSHA256 string `json:"certSha256,omitempty"`
}

type DSMState struct {
	Certificate *TrustedCertificate `json:"certificate,omitempty"`
	Origin      string              `json:"origin"`
	Port        int                 `json:"port"`
	Pin         Pin                 `json:"pin"`
	Public      *Endpoint           `json:"public,omitempty"`
	Private     *Endpoint           `json:"private,omitempty"`
}

type State struct {
	EntryActivity      []EntryActivity           `json:"entry_activity,omitempty"`
	IngressRevokedKeys map[string]IngressSubject `json:"ingress_revoked_keys,omitempty"`
	Schema             int                       `json:"schema"`
	NextRev            uint64                    `json:"next_rev"`
	Apps               map[string]AppState       `json:"apps"`
	DSM                *DSMState                 `json:"dsm,omitempty"`
	// Custom holds the Apps an administrator added by URL, keyed by ID.
	Custom map[string]CustomApp `json:"custom,omitempty"`
}

func EmptyState() State { return State{Schema: Schema, NextRev: 1, Apps: map[string]AppState{}} }

// Revision reserves a global revision. Deleting the last scope must preserve
// NextRev; a removed and re-added origin must never redeem an old resume token.
func (s *State) Revision() (uint64, error) {
	if s.NextRev == 0 || s.NextRev >= 9999999999999999999 {
		return 0, ErrInvalid
	}
	rev := s.NextRev
	s.NextRev++
	return rev, nil
}

func (a AppState) Scope(kind string) *Scope {
	if kind == Public {
		return a.Public
	}
	if kind == Private {
		return a.Private
	}
	return nil
}

func (a *AppState) SetScope(kind string, scope *Scope) {
	if kind == Public {
		a.Public = scope
	} else if kind == Private {
		a.Private = scope
	}
}

func validPort(port int) bool { return port > 0 && port <= 65535 }

func validAuthority(authority string, private bool) bool {
	u, err := url.Parse("https://" + authority)
	if err != nil || u.Host != authority || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || authority != strings.ToLower(authority) {
		return false
	}
	host := u.Hostname()
	if host == "" || len(host) > 253 || host == "localhost" || strings.HasSuffix(host, ".") || net.ParseIP(host) != nil {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	if private {
		port, err := strconv.Atoi(u.Port())
		return err == nil && validPort(port) && strconv.Itoa(port) == u.Port()
	}
	return u.Port() == ""
}

func (p Pin) valid() bool {
	if p.Mode == string(ModeCustom) {
		return p.SPKI == "" && len(p.Names) == 0
	}
	if p.Mode == "S" {
		return hexPattern.MatchString(p.SPKI) && len(p.Names) == 0
	}
	if p.Mode != "W" || p.SPKI != "" || len(p.Names) == 0 || len(p.Names) > 100 {
		return false
	}
	for _, name := range p.Names {
		if !validAuthority(strings.TrimPrefix(name, "*."), false) {
			return false
		}
	}
	return true
}

func (s State) Validate() error {
	if len(s.EntryActivity) > 64 {
		return ErrInvalid
	}
	if len(s.IngressRevokedKeys) > MaxCustom {
		return ErrCorrupt
	}
	for id, subject := range s.IngressRevokedKeys {
		if !customIDPattern.MatchString(id) || !ingressPackageName.MatchString(subject.Package) || subject.UID <= 0 {
			return ErrCorrupt
		}
	}
	if s.Schema != Schema || s.NextRev == 0 || s.NextRev > 9999999999999999999 || s.Apps == nil || len(s.Apps) > 1024 {
		return ErrInvalid
	}
	publicMode := ""
	if s.DSM != nil && s.DSM.Public != nil {
		publicMode = ProviderMode(s.DSM.Public.Provider)
	}
	sockets := map[string]bool{}
	endpoint := func(socket, authority string, private bool) bool {
		if !socketPattern.MatchString(socket) || sockets[socket] || !validAuthority(authority, private) {
			return false
		}
		sockets[socket] = true
		return true
	}
	if len(s.Custom) > MaxCustom {
		return ErrInvalid
	}
	for id, c := range s.Custom {
		if !customIDPattern.MatchString(id) || !c.valid() {
			return ErrInvalid
		}
	}
	for pkg, app := range s.Apps {
		if !packagePattern.MatchString(pkg) || app.Rev == 0 || app.Rev >= s.NextRev || app.Public == nil && app.Private == nil {
			return ErrInvalid
		}
		if !s.validCustomScopes(pkg, app) {
			return ErrInvalid
		}
		for _, kind := range []string{Public, Private} {
			scope := app.Scope(kind)
			if scope == nil {
				continue
			}
			if e := scope.Binding.Entry; e != nil && (!e.Valid() || e.Package != pkg || e.Port != scope.Binding.Port) {
				return ErrInvalid
			}
			if !ValidProvider(scope.Provider) || kind == Private && scope.Provider != "" {
				return ErrInvalid
			}
			if (scope.State != Active && scope.State != Paused) || len(scope.Reason) > 1024 || !validPort(scope.Binding.Port) || !hexPattern.MatchString(scope.Binding.Summary) || !scope.Binding.Pin.valid() || !endpoint(scope.Socket, scope.Authority, kind == Private) {
				return ErrInvalid
			}
			if kind == Public {
				mode := ProviderMode(scope.Provider)
				if publicMode != "" && mode != publicMode {
					return ErrInvalid
				}
				publicMode = mode
			}
			if kind == Public && scope.Origin != "https://"+scope.Authority || kind == Private && scope.Origin != "" {
				return ErrInvalid
			}
		}
	}
	if d := s.DSM; d != nil {
		if d.Pin.CertSHA256 != "" && !hexPattern.MatchString(d.Pin.CertSHA256) {
			return ErrInvalid
		}
		if !validPort(d.Port) || !d.Pin.valid() || d.Pin.Mode == string(ModeCustom) {
			return ErrInvalid
		}
		if d.Public != nil && (!ValidProvider(d.Public.Provider) || !endpoint(d.Public.Socket, d.Public.Authority, false) || d.Origin != "https://"+d.Public.Authority) {
			return ErrInvalid
		}
		if d.Public == nil && d.Origin != "" {
			return ErrInvalid
		}
		if d.Private != nil && (d.Private.Provider != "" || !endpoint(d.Private.Socket, d.Private.Authority, true)) {
			return ErrInvalid
		}
	}
	return nil
}

// validCustomScopes keeps pinless bindings to custom Apps: public only, bound
// to the saved definition, or paused as removed until its cleanup finishes.
func (s State) validCustomScopes(pkg string, app AppState) bool {
	custom := IsCustom(pkg)
	for _, scope := range []*Scope{app.Public, app.Private} {
		if scope != nil && (scope.Binding.Pin.Mode == string(ModeCustom)) != custom {
			return false
		}
	}
	if !custom {
		return true
	}
	if !customIDPattern.MatchString(pkg) || app.Private != nil {
		return false
	}
	def, ok := s.Custom[pkg]
	if !ok {
		return app.Public.State == Paused && app.Public.Reason == "removed"
	}
	return app.Public.Binding.Summary == def.Digest() && app.Public.Binding.Port == def.Port() &&
		(def.Ingress == nil || app.Public.Provider != Workers && app.Public.Authority == def.Ingress.Hostname)
}

func ValidProvider(provider string) bool {
	return provider == "" || provider == "cloudflare" || provider == Workers
}

func ProviderMode(provider string) string {
	if provider == Workers {
		return Workers
	}
	return "cloudflare"
}
