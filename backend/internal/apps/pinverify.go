package apps

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/x509roots/fallback/bundle"
)

type Mode string

const (
	ModeW Mode = "W" // public CA chain + admin-confirmed names N + same key as the DSM HTTPS port
	ModeS Mode = "S" // admin-confirmed SPKI
)

type PinTarget string

const (
	TargetApp PinTarget = "app"
	TargetDSM PinTarget = "dsm"
)

// PinBinding is what the administrator confirmed. Only Confirm creates one.
type PinBinding struct {
	EntryPath  string    `json:"entry_path,omitempty"`
	CertSHA256 string    `json:"certSha256,omitempty"`
	Provider   string    `json:"provider,omitempty"`
	Socket     string    `json:"socket"`
	Scope      string    `json:"scope"`
	Revision   uint64    `json:"rev"`
	Summary    string    `json:"summary"`
	Package    string    `json:"package,omitempty"`
	Target     PinTarget `json:"target"`
	Port       int       `json:"port"`
	Authority  string    `json:"authority"`
	Mode       Mode      `json:"mode"`
	Names      []string  `json:"names,omitempty"` // W: N
	SPKI       string    `json:"spki,omitempty"`  // S
}

// Reasons a handshake is refused. pinMismatch reasons start the 120-second
// timer; reference_unavailable is "waiting" and never writes state.
const (
	ReasonChain       = "chain_untrusted"
	ReasonExpired     = "expired"
	ReasonName        = "name_not_covering_N"
	ReasonKey         = "key_differs_from_reference"
	ReasonRefInvalid  = "reference_invalid"
	ReasonRefMissing  = "reference_unavailable"
	ReasonSPKI        = "spki_differs"
	ReasonPaused      = "paused"
	ReasonRouteBad    = "ping_mismatch"
	ReasonPingMissing = "ping_unavailable"
)

func pinMismatch(reason string) bool {
	switch reason {
	case ReasonChain, ReasonExpired, ReasonName, ReasonKey, ReasonRefInvalid, ReasonSPKI:
		return true
	}
	return false
}

// Reject is returned from VerifyConnection, so no application data follows.
type Reject struct{ Reason string }

func (r *Reject) Error() string { return "pin: refused: " + r.Reason }

// ReasonOf extracts the refusal reason from an error chain.
func ReasonOf(err error) string {
	var r *Reject
	if errors.As(err, &r) {
		return r.Reason
	}
	return ""
}

// ProductRoots is Connect's built-in Mozilla (NSS) root set. A fresh pool on
// every call: test roots added by Roots never reach it.
func ProductRoots() *x509.CertPool {
	pool := x509.NewCertPool()
	for r := range bundle.Roots() {
		c, err := x509.ParseCertificate(r.Certificate)
		if err != nil {
			continue
		}
		if r.Constraint != nil {
			pool.AddCertWithConstraint(c, r.Constraint)
		} else {
			pool.AddCert(c)
		}
	}
	return pool
}

func SPKI(c *x509.Certificate) string {
	s := sha256.Sum256(c.RawSubjectPublicKeyInfo)
	return hex.EncodeToString(s[:])
}

// certSHA256 is the whole certificate's fingerprint, as browsers show it.
func certSHA256(c *x509.Certificate) string {
	s := sha256.Sum256(c.Raw)
	return hex.EncodeToString(s[:])
}

// Options configure one verifier. Roots is injectable for isolated tests.
type Options struct {
	Roots            *x509.CertPool
	Now              func() time.Time
	ReferencePort    func() (int, error) // DatastoreDetector's DSM HTTPS port
	Host             string              // loopback host to dial, default 127.0.0.1
	RefCacheTTL      time.Duration
	PauseAfter       time.Duration
	HandshakeTimeout time.Duration
}

type Verifier struct {
	o Options
	// Product roots are parsed only for the first web-PKI check, never on idle startup.
	roots func() *x509.CertPool

	mu        sync.Mutex
	refPort   int
	refSPKI   string
	refAt     time.Time
	refNames  []string
	refExpiry time.Time
}

func NewVerifier(o Options) *Verifier {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Host == "" {
		o.Host = "127.0.0.1"
	}
	if o.RefCacheTTL == 0 {
		o.RefCacheTTL = 30 * time.Second
	}
	if o.PauseAfter == 0 {
		o.PauseAfter = 120 * time.Second
	}
	if o.HandshakeTimeout == 0 {
		o.HandshakeTimeout = 3 * time.Second
	}
	v := &Verifier{o: o}
	if o.Roots == nil {
		v.roots = sync.OnceValue(ProductRoots)
	}
	return v
}

func (v *Verifier) Now() time.Time { return v.o.Now() }

// Probe handshakes without SNI or resumption and sends no application data.
func (v *Verifier) Probe(ctx context.Context, port int) ([]*x509.Certificate, error) {
	if port < 1 || port > 65535 || net.ParseIP(v.o.Host) == nil || !net.ParseIP(v.o.Host).IsLoopback() {
		return nil, errors.New("invalid loopback target")
	}
	d := &net.Dialer{Timeout: v.o.HandshakeTimeout}
	raw, err := d.DialContext(ctx, "tcp", net.JoinHostPort(v.o.Host, strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	defer raw.Close()
	raw.SetDeadline(time.Now().Add(v.o.HandshakeTimeout))
	c := tls.Client(raw, &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"http/1.1"}, MinVersion: tls.VersionTLS12})
	if err := c.HandshakeContext(ctx); err != nil {
		return nil, err
	}
	return c.ConnectionState().PeerCertificates, nil
}

func (v *Verifier) webPKI(chain []*x509.Certificate) string {
	if len(chain) == 0 {
		return ReasonChain
	}
	inter := x509.NewCertPool()
	for _, c := range chain[1:] {
		inter.AddCert(c)
	}
	roots := v.o.Roots
	if roots == nil {
		roots = v.roots()
	}
	_, err := chain[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter, CurrentTime: v.o.Now(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
	var inv x509.CertificateInvalidError
	switch {
	case err == nil:
		return ""
	case errors.As(err, &inv) && inv.Reason == x509.Expired:
		return ReasonExpired
	default:
		return ReasonChain
	}
}

func covers(leaf *x509.Certificate, names []string) bool {
	if len(names) == 0 {
		return false
	}
	for _, n := range names {
		if strings.HasPrefix(n, "*.") {
			n = "pinproxy-name-check" + n[1:]
		}
		if leaf.VerifyHostname(n) != nil {
			return false
		}
	}
	return true
}

// reference returns the reference endpoint's SPKI. The leaf must itself pass
// the chain and N checks. force skips the cache (the one refetch on mismatch).
func (v *Verifier) reference(ctx context.Context, names []string, force bool) (string, string) {
	if v.o.ReferencePort == nil {
		return "", ReasonRefMissing
	}
	port, err := v.o.ReferencePort()
	if err != nil || port < 1 || port > 65535 {
		return "", ReasonRefMissing
	}
	v.mu.Lock()
	if !force && port == v.refPort && v.refSPKI != "" && v.o.Now().Sub(v.refAt) < v.o.RefCacheTTL && v.o.Now().Before(v.refExpiry) && slices.Equal(names, v.refNames) {
		s := v.refSPKI
		v.mu.Unlock()
		return s, ""
	}
	v.mu.Unlock()
	chain, err := v.Probe(ctx, port)
	if err != nil {
		return "", ReasonRefMissing
	}
	if r := v.webPKI(chain); r != "" {
		return "", ReasonRefInvalid
	}
	if !covers(chain[0], names) {
		return "", ReasonRefInvalid
	}
	s := SPKI(chain[0])
	v.mu.Lock()
	v.refPort, v.refSPKI, v.refAt = port, s, v.o.Now()
	v.refNames, v.refExpiry = slices.Clone(names), chain[0].NotAfter
	v.mu.Unlock()
	return s, ""
}

// Check is the per-handshake pin check for a confirmed binding; "" accepts.
func (v *Verifier) Check(ctx context.Context, b PinBinding, chain []*x509.Certificate) string {
	if b.Target == TargetDSM && b.CertSHA256 != "" {
		if len(chain) == 0 {
			return ReasonChain
		}
		digest := sha256.Sum256(chain[0].Raw)
		if hex.EncodeToString(digest[:]) != b.CertSHA256 {
			return ReasonSPKI
		}
	}
	if len(chain) == 0 {
		return ReasonChain
	}
	if b.Mode != ModeW && b.Mode != ModeS {
		return ReasonChain
	}
	if b.Mode == ModeS {
		if SPKI(chain[0]) != b.SPKI {
			return ReasonSPKI
		}
		return ""
	}
	if r := v.webPKI(chain); r != "" {
		return r
	}
	if !covers(chain[0], b.Names) {
		return ReasonName
	}
	// The DSM target is the reference endpoint: same key would compare it
	// with itself, so only N and the chain apply (spec R16a, v8 C11).
	if b.Target == TargetDSM {
		return ""
	}
	leaf := SPKI(chain[0])
	ref, r := v.reference(ctx, b.Names, false)
	if r == ReasonRefInvalid || r == "" && ref != leaf {
		ref, r = v.reference(ctx, b.Names, true)
	}
	if r != "" {
		return r
	}
	if ref != leaf {
		return ReasonKey
	}
	return ""
}

// Proposal is what the approval dialog shows. It never becomes a PinBinding on
// its own: Confirm is the administrator's click.
type Proposal struct {
	Target               PinTarget `json:"target"`
	Port                 int       `json:"port"`
	Mode                 Mode      `json:"mode"`
	ModeWhy              string    `json:"mode_why"`
	Names                []string  `json:"names"`
	Issuer               string    `json:"issuer"`
	NotAfter             time.Time `json:"not_after"`
	SPKI                 string    `json:"spki"`
	CertSHA256           string    `json:"cert_sha256"`
	RequiresConfirmation bool      `json:"requires_confirmation"`
	Notice               string    `json:"notice"`
}

const approvalNotice = "Compare names, issuer and the certificate SHA-256 fingerprint with the certificate DSM presents. Names and issuer are not proof. A port taken over during approval can establish the wrong trust. Do not share the DSM private key with apps or containers."

func leafNames(c *x509.Certificate) []string {
	names := slices.Clone(c.DNSNames)
	for _, ip := range c.IPAddresses {
		names = append(names, ip.String())
	}
	if len(names) == 0 && c.Subject.CommonName != "" {
		names = []string{c.Subject.CommonName}
	}
	return names
}

// Propose requires a ready target and decides the mode from what the port presents now (name TOFU for W,
// SPKI TOFU for S). App readiness includes the routing diagnostic.
func (v *Verifier) Propose(ctx context.Context, target PinTarget, port int, pkg, authority string, entries ...string) (Proposal, error) {
	chain, err := v.probeReady(ctx, target, port, pkg, authority, entries...)
	if err != nil {
		return Proposal{}, err
	}
	leaf := chain[0]
	p := Proposal{Target: target, Port: port, Names: leafNames(leaf), Issuer: leaf.Issuer.String(), NotAfter: leaf.NotAfter.UTC(), SPKI: SPKI(leaf), CertSHA256: certSHA256(leaf), RequiresConfirmation: true, Notice: approvalNotice}
	p.Mode, p.ModeWhy = ModeS, "not a public CA chain"
	if r := v.webPKI(chain); r == "" && covers(leaf, p.Names) {
		if target == TargetDSM {
			p.Mode, p.ModeWhy = ModeW, "public CA chain (DSM target)"
		} else if ref, r := v.reference(ctx, p.Names, true); r != "" {
			if r == ReasonRefMissing {
				return Proposal{}, &Reject{r}
			}
			p.ModeWhy = "public CA chain but reference endpoint " + r
		} else if ref != p.SPKI {
			p.ModeWhy = "public CA chain but key differs from the DSM HTTPS port"
		} else {
			p.Mode, p.ModeWhy = ModeW, "public CA chain and same key as the DSM HTTPS port"
		}
	} else {
		p.ModeWhy = "not a public CA chain: " + r
	}
	return p, nil
}

// Confirm is the administrator's approval of a proposal.
func Confirm(p Proposal, pkg, authority string) PinBinding {
	b := PinBinding{Package: pkg, Target: p.Target, Port: p.Port, Authority: authority, Mode: p.Mode}
	if p.Mode == ModeW {
		b.Names = slices.Clone(p.Names)
	} else {
		b.SPKI = p.SPKI
	}
	return b
}

// Reenable is what re-enabling a paused binding shows.
type Reenable struct {
	Action   string   `json:"action"` // reuse | reconfirm | blocked
	Mode     Mode     `json:"mode"`
	Why      string   `json:"why"`
	OldNames []string `json:"old_names,omitempty"`
	NewNames []string `json:"new_names,omitempty"`
	OldSPKI  string   `json:"old_spki,omitempty"`
	NewSPKI  string   `json:"new_spki,omitempty"`
}

// PrepareReenable keeps the mode and never rebuilds N in the background.
func (v *Verifier) PrepareReenable(ctx context.Context, b PinBinding) (Reenable, error) {
	chain, err := v.probeReady(ctx, b.Target, b.Port, b.Package, b.Authority, b.EntryPath)
	if err != nil {
		return Reenable{}, err
	}
	r := Reenable{Mode: b.Mode, OldNames: b.Names, OldSPKI: b.SPKI, NewNames: leafNames(chain[0]), NewSPKI: SPKI(chain[0])}
	if b.Mode == ModeS {
		if r.NewSPKI == b.SPKI {
			r.Action, r.Why = "reuse", "same SPKI"
		} else {
			r.Action, r.Why = "reconfirm", "SPKI changed: confirm the new fingerprint"
		}
		return r, nil
	}
	if bad := v.webPKI(chain); bad != "" {
		r.Action, r.Why = "blocked", "W requirements not met ("+bad+"); no downgrade to S"
		return r, nil
	}
	if !covers(chain[0], b.Names) {
		candidate := b
		candidate.Names = r.NewNames
		if reason := v.Check(ctx, candidate, chain); reason != "" {
			r.Action, r.Why = "blocked", reason
			return r, nil
		}
		r.Action, r.Why = "reconfirm", "leaf no longer covers N: confirm the new names (new name TOFU)"
		return r, nil
	}
	if reason := v.Check(ctx, b, chain); reason != "" {
		r.Action, r.Why = "blocked", "W requirements not met ("+reason+"); no downgrade to S"
		return r, nil
	}
	r.Action, r.Why = "reuse", "leaf still covers N"
	return r, nil
}

// ConfirmReenable is the administrator's click on a reconfirm dialog.
func ConfirmReenable(b PinBinding, r Reenable) (PinBinding, error) {
	switch r.Action {
	case "reuse":
		return b, nil
	case "reconfirm":
		if b.Mode == ModeW {
			b.Names = slices.Clone(r.NewNames)
		} else {
			b.SPKI = r.NewSPKI
		}
		return b, nil
	}
	return b, fmt.Errorf("cannot re-enable: %s", r.Why)
}

// VerifyTarget is the read-only Runtime.Validate hook: one TLS connection,
// VerifyConnection pin validation, then the App routing ping on that connection.
// It never persists a pause, creates a listener, grants access or chooses a mode.
// In particular it is safe inside Lifecycle's state writer transaction.
func (v *Verifier) VerifyTarget(ctx context.Context, b PinBinding) error {
	if !validPort(b.Port) || !(Pin{Mode: string(b.Mode), Names: b.Names, SPKI: b.SPKI}).valid() {
		return ErrInvalid
	}
	if b.Target != TargetApp && b.Target != TargetDSM || b.Target == TargetApp && !packagePattern.MatchString(b.Package) || !SameAuthority(b.Authority, b.Authority) {
		return ErrInvalid
	}
	if net.ParseIP(v.o.Host) == nil || !net.ParseIP(v.o.Host).IsLoopback() {
		return ErrInvalid
	}
	hctx, cancel := context.WithTimeout(ctx, v.o.HandshakeTimeout)
	defer cancel()
	raw, err := (&net.Dialer{}).DialContext(hctx, "tcp", net.JoinHostPort(v.o.Host, strconv.Itoa(b.Port)))
	if err != nil {
		return err
	}
	defer raw.Close()
	c := tls.Client(raw, &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}, VerifyConnection: func(cs tls.ConnectionState) error {
		if reason := v.Check(hctx, b, cs.PeerCertificates); reason != "" {
			return &Reject{reason}
		}
		return nil
	}})
	if err = c.HandshakeContext(hctx); err != nil {
		return err
	}
	if b.Target == TargetApp {
		timeout := v.o.HandshakeTimeout
		if deadline, ok := hctx.Deadline(); ok {
			if left := time.Until(deadline); left < timeout {
				timeout = left
			}
		}
		if timeout <= 0 {
			return &Reject{ReasonPingMissing}
		}
		if reason := ping(c, bufio.NewReader(c), b, timeout); reason != "" {
			return &Reject{reason}
		}
	}
	return nil
}
