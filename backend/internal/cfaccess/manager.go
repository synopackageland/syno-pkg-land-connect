package cfaccess

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type Inventory struct {
	DSMHost     string
	Account     string
	Hosts       []string
	Unsupported bool
}
type Config struct {
	Context    context.Context
	Activity   func()
	Reconciled func(context.Context)
	Path       string
	API        API
	Credential func() (Credential, error)
	Endpoints  func() (Inventory, error)
	// Changed closes existing public streams after durable restriction changes.
	Changed func()
}
type Request struct {
	SessionDuration string   `json:"sessionDuration,omitempty"`
	Emails          []string `json:"emails"`
	Revision        uint64   `json:"revision"`
}
type Endpoint struct {
	Host      string `json:"host"`
	Protected bool   `json:"protected"`
}
type Status struct {
	SessionDuration string     `json:"sessionDuration"`
	Working         bool       `json:"working"`
	State           string     `json:"state"`
	Code            string     `json:"code,omitempty"`
	Revision        uint64     `json:"revision"`
	Emails          []string   `json:"emails"`
	Endpoints       []Endpoint `json:"endpoints"`
	Checked         int64      `json:"checked,omitempty"`
}
type Manager struct {
	ctx     context.Context
	cancel  context.CancelFunc
	working atomic.Bool
	attempt atomic.Pointer[string]
	wg      sync.WaitGroup
	cfg     Config
	mu      sync.Mutex
	state   state
	view    atomic.Pointer[state]
	invalid atomic.Bool
	keys    keyCache
	wake    chan struct{}
}

func New(cfg Config) *Manager {
	parent := cfg.Context
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	m := &Manager{cfg: cfg, wake: make(chan struct{}, 1), ctx: ctx, cancel: cancel}
	v, e := load(cfg.Path)
	m.state = v
	if e != nil {
		m.invalid.Store(true)
	}
	// A previous cloud check never authorizes a restored origin binding.
	m.state.Synced = false
	m.publish()
	return m
}
func (m *Manager) publish() {
	raw, _ := json.Marshal(m.state)
	var v state
	_ = json.Unmarshal(raw, &v)
	m.view.Store(&v)
}
func (m *Manager) persist() error {
	if e := save(m.cfg.Path, m.state); e != nil {
		m.invalid.Store(true)
		m.publish()
		m.changed()
		return e
	}
	m.publish()
	return nil
}
func (m *Manager) changed() {
	if m.cfg.Changed != nil {
		m.cfg.Changed()
	}
}
func (m *Manager) InUse() bool {
	v := m.view.Load()
	return m.invalid.Load() || v != nil && (len(v.Emails) > 0 || len(v.Records) > 0 || v.OwnedOTP)
}
func (m *Manager) Enabled() bool {
	v := m.view.Load()
	return m.invalid.Load() || v != nil && len(v.Emails) > 0
}
func (m *Manager) Status() Status {
	v := m.view.Load()
	out := Status{SessionDuration: DefaultSessionDuration, State: "off", Emails: []string{}, Endpoints: []Endpoint{}}
	if m.invalid.Load() {
		out.State = "error"
		out.Code = "access_state_corrupt"
		return out
	}
	if v == nil {
		return out
	}
	out.SessionDuration = sessionDuration(v.Session)
	out.Revision = v.Revision
	out.Emails = append([]string{}, v.Emails...)
	out.Checked = v.Checked
	out.Code = v.Code
	if len(v.Emails) > 0 || len(v.Records) > 0 || v.OwnedOTP {
		out.State = "pending"
		if v.Synced {
			out.State = "synced"
		}
		if v.Code != "" {
			out.State = "error"
		}
	}
	for host, r := range v.Records {
		out.Endpoints = append(out.Endpoints, Endpoint{Host: host, Protected: v.Synced && r.AUD != "" && len(v.Emails) > 0})
	}
	sort.Slice(out.Endpoints, func(i, j int) bool { return out.Endpoints[i].Host < out.Endpoints[j].Host })
	out.Working = m.working.Load()
	if code := m.attempt.Load(); code != nil {
		out.Code = *code
		out.State = "error"
	}
	return out
}
func (m *Manager) preflight(ctx context.Context, hosts []string) (Credential, inventory, error) {
	var cloud inventory
	c, e := m.cfg.Credential()
	if e != nil {
		return c, cloud, e
	}
	if m.state.Account != "" && c.Account != m.state.Account {
		return c, cloud, failure("access_account_changed")
	}
	cloud, e = m.cfg.API.Preflight(ctx, c)
	if e != nil {
		return c, cloud, e
	}
	for _, host := range hosts {
		if !validHost(host) {
			return c, cloud, failure("access_invalid_request")
		}
		for _, a := range cloud.Apps {
			if !appOverlaps(a, host) {
				continue
			}
			r, known := m.state.Records[host]
			if !known || a.Name != r.Name || a.Domain != host || a.Type != "self_hosted" || !ownedDestination(a, host) || r.ID != "" && a.ID != r.ID {
				return c, cloud, failure("access_conflict")
			}
		}
	}
	return c, cloud, nil
}
func appOverlaps(a Application, host string) bool {
	if overlaps(a.Domain, host) {
		return true
	}
	for _, d := range a.Destinations {
		if h, ok := d["uri"].(string); ok && overlaps(h, host) {
			return true
		}
	}
	return false
}
func (m *Manager) Apply(ctx context.Context, in Request) (Status, error) {
	if in.SessionDuration != "" && !sessionChoice(in.SessionDuration) {
		return m.Status(), failure("access_invalid_request")
	}
	emails, e := Emails(in.Emails)
	if e != nil {
		return m.Status(), e
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.invalid.Load() {
		return m.Status(), failure("access_state_corrupt")
	}
	if in.Revision != m.state.Revision {
		return m.Status(), failure("access_revision_changed")
	}
	endpoints, e := m.endpoints()
	if e != nil {
		return m.Status(), e
	}
	if len(emails) > 0 && endpoints.Unsupported {
		return m.Status(), failure("access_alias_unsupported")
	}
	if len(emails) == 0 && !m.InUse() && in.SessionDuration == "" {
		return m.Status(), nil
	}
	if len(emails) == 0 && !m.InUse() {
		if m.state.Owner == "" {
			m.state.Owner, e = randomID()
			if e != nil {
				return m.Status(), e
			}
		}
		m.state.Session = in.SessionDuration
		m.state.Revision++
		e = m.persist()
		return m.Status(), e
	}
	c, cloud, e := m.preflight(ctx, endpoints.Hosts)
	if e != nil {
		return m.Status(), e
	}
	if endpoints.Account != "" && endpoints.Account != c.Account {
		return m.Status(), failure("access_account_changed")
	}
	if m.state.Owner == "" {
		m.state.Owner, e = randomID()
		if e != nil {
			return m.Status(), e
		}
	}
	removed := false
	for _, email := range m.state.Emails {
		removed = removed || !slices.Contains(emails, email)
	}
	policyChanged := !slices.Equal(m.state.Emails, emails)
	m.state.DSMHost = endpoints.DSMHost
	if in.SessionDuration != "" {
		m.state.Session = in.SessionDuration
		for host, r := range m.state.Records {
			r.PendingSession = in.SessionDuration
			m.state.Records[host] = r
		}
	}
	m.state.Revision++
	m.state.Account = c.Account
	m.state.Emails = emails
	if policyChanged {
		m.state.Synced = false
	}
	m.state.Code = ""
	if removed {
		for host, r := range m.state.Records {
			r.Revoke = true
			m.state.Records[host] = r
		}
	}
	if e = m.persist(); e != nil {
		return m.Status(), e
	}
	if policyChanged {
		m.changed()
	}
	e = m.reconcile(ctx, c, cloud, endpoints.Hosts)
	if e == nil {
		e = m.checkInventory(endpoints)
	}
	e = m.finish(e)
	return m.Status(), e
}
func (m *Manager) Sync(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.InUse() {
		return nil
	}
	if m.invalid.Load() {
		return failure("access_state_corrupt")
	}
	endpoints, e := m.endpoints()
	if e != nil {
		m.finish(e)
		return e
	}
	if len(m.state.Emails) > 0 && endpoints.Unsupported {
		e = failure("access_alias_unsupported")
		m.finish(e)
		return e
	}
	m.state.DSMHost = endpoints.DSMHost
	c, cloud, e := m.preflight(ctx, endpoints.Hosts)
	if e == nil && endpoints.Account != "" && endpoints.Account != c.Account {
		e = failure("access_account_changed")
	}
	if e == nil {
		e = m.reconcile(ctx, c, cloud, endpoints.Hosts)
		if e == nil {
			e = m.checkInventory(endpoints)
		}
	}
	return m.finish(e)
}

// Prepare is called with a trusted hostname before a new route is published.
// Unknown hostnames also refuse at the origin until their readback succeeds.
func (m *Manager) Prepare(ctx context.Context, hosts []string) error {
	if !m.Enabled() {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.invalid.Load() {
		return failure("access_state_corrupt")
	}
	existing, e := m.endpoints()
	if e != nil {
		return e
	}
	if existing.Unsupported {
		return failure("access_alias_unsupported")
	}
	for host := range m.state.Records {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	hosts = slices.Compact(hosts)
	c, cloud, e := m.preflight(ctx, hosts)
	if e == nil {
		e = m.reconcile(ctx, c, cloud, hosts)
	}
	return m.finish(e)
}
func (m *Manager) finish(e error) error {
	if e != nil {
		was := m.state.Synced
		m.state.Synced = false
		m.state.Code = Code(e)
		persistErr := m.persist()
		if was {
			m.changed()
		}
		if persistErr != nil {
			return persistErr
		}
		return e
	}
	m.state.Synced = true
	m.state.Code = ""
	m.state.Checked = time.Now().Unix()
	return m.persist()
}
func (m *Manager) block() error {
	if m.state.Synced {
		m.state.Synced = false
		if e := m.persist(); e != nil {
			return e
		}
		m.changed()
	}
	return nil
}
func (m *Manager) reconcile(ctx context.Context, c Credential, cloud inventory, hosts []string) error {
	wanted := map[string]bool{}
	if len(m.state.Emails) > 0 {
		for _, h := range hosts {
			wanted[h] = true
		}
	}
	// Withdrawn hostnames are removed only from the exact private ledger.
	for host, r := range m.state.Records {
		if wanted[host] {
			continue
		}
		if e := m.block(); e != nil {
			return e
		}
		a, e := m.resolve(ctx, c, cloud, host, r)
		if e != nil {
			return e
		}
		if a.ID != "" {
			if e = m.cfg.API.DeleteApp(ctx, c, a.ID); e != nil {
				return e
			}
			if _, e = m.cfg.API.GetApp(ctx, c, a.ID); Code(e) != "access_not_found" {
				if e == nil {
					return failure("access_readback_failed")
				}
				return e
			}
		}
		delete(m.state.Records, host)
		if e = m.persist(); e != nil {
			return e
		}
	}
	if len(wanted) == 0 {
		return m.cleanupOTP(ctx, c, cloud)
	}
	m.state.Issuer = "https://" + cloud.Organization.Domain
	if e := m.ensureOTP(ctx, c, cloud); e != nil {
		return e
	}
	for _, host := range hosts {
		r, ok := m.state.Records[host]
		if !ok {
			if e := m.block(); e != nil {
				return e
			}
			nonce, e := randomID()
			if e != nil {
				return e
			}
			r.Name = "Connect " + m.state.Owner + " " + nonce
			m.state.Records[host] = r
			if e = m.persist(); e != nil {
				return e
			}
		}
		a, e := m.resolve(ctx, c, cloud, host, r)
		if e != nil {
			return e
		}
		want := desiredApp(r.Name, host, m.state.OTP, m.state.Emails, r.Policy)
		want.Session = sessionDuration(m.state.Session)
		if a.ID != "" {
			if validSessionDuration(a.Session) {
				r.Session = a.Session
			}
			want.Session = sessionDuration(r.Session)
		}
		if r.PendingSession != "" {
			want.Session = r.PendingSession
		}
		// Cache observations even if a later policy operation fails.
		m.state.Records[host] = r
		if host == m.state.DSMHost && r.PendingSession == "" && validSessionDuration(r.Session) {
			m.state.Session = r.Session
		}
		if a.ID != "" {
			want.ID = a.ID
			if len(a.Policies) == 1 {
				want.Policies[0].ID = a.Policies[0].ID
			}
		}
		policyDrift := a.ID == "" || canonical(a) != canonical(want)
		sessionChange := r.PendingSession != "" && a.Session != want.Session
		if policyDrift || sessionChange {
			if policyDrift {
				if e = m.block(); e != nil {
					return e
				}
			}
			if a.ID != "" && policyDrift {
				r.Revoke = true
				m.state.Records[host] = r
				if e = m.persist(); e != nil {
					return e
				}
			}
			if a.ID == "" {
				r.ID = ""
				r.AUD = ""
				r.Policy = ""
				want.Policies[0].ID = ""
				m.state.Records[host] = r
				if e = m.persist(); e != nil {
					return e
				}
			}
			if a.ID == "" {
				r.Creating = true
				m.state.Records[host] = r
				if e = m.persist(); e != nil {
					return e
				}
			}
			a, e = m.cfg.API.SaveApp(ctx, c, want)
			if e != nil {
				if r.Creating && definiteCreateRefusal(e) {
					r.Creating = false
					m.state.Records[host] = r
					if pe := m.persist(); pe != nil {
						return pe
					}
				}
				return e
			}
			r.Creating = false
			if a.ID == "" {
				return failure("access_readback_failed")
			}
			r.ID = a.ID
			m.state.Records[host] = r
			if e = m.persist(); e != nil {
				return e
			}
		}
		a, e = m.cfg.API.GetApp(ctx, c, a.ID)
		if e != nil {
			return e
		}
		if a.Name != r.Name || a.Domain != host || a.AUD == "" || len(a.Policies) != 1 || a.Policies[0].ID == "" || canonical(a) != canonical(want) {
			return failure("access_readback_failed")
		}
		if r.PendingSession != "" && a.Session != r.PendingSession {
			return failure("access_readback_failed")
		}
		r.PendingSession = ""
		if validSessionDuration(a.Session) {
			r.Session = a.Session
		}
		if host == m.state.DSMHost && validSessionDuration(r.Session) {
			m.state.Session = r.Session
		}
		r.Creating = false
		r.ID = a.ID
		r.Policy = a.Policies[0].ID
		r.AUD = a.AUD
		digest := sha256.Sum256([]byte(canonical(a)))
		r.Digest = hex.EncodeToString(digest[:])
		if r.Revoke {
			if e = m.cfg.API.Revoke(ctx, c, a.ID); e != nil {
				return e
			}
			r.Revoke = false
		}
		m.state.Records[host] = r
		if e = m.persist(); e != nil {
			return e
		}
	}
	return nil
}
func (m *Manager) resolve(ctx context.Context, c Credential, cloud inventory, host string, r record) (Application, error) {
	if r.ID != "" {
		a, e := m.cfg.API.GetApp(ctx, c, r.ID)
		if Code(e) == "access_not_found" {
			r.ID = ""
		} else {
			if e != nil {
				return a, e
			}
			if a.ID != r.ID || a.Name != r.Name || a.Domain != host || a.Type != "self_hosted" || !ownedDestination(a, host) {
				return a, failure("access_conflict")
			}
			return a, nil
		}
	}
	var found Application
	for _, a := range cloud.Apps {
		if a.Name == r.Name {
			if found.ID != "" || a.Domain != host || a.Type != "self_hosted" || !ownedDestination(a, host) {
				return a, failure("access_conflict")
			}
			found = a
		}
	}
	if found.ID == "" && r.Creating {
		return found, failure("access_conflict")
	}
	return found, nil
}
func (m *Manager) ensureOTP(ctx context.Context, c Credential, cloud inventory) error {
	if m.state.OTP != "" {
		for _, p := range cloud.IDPs {
			if p.ID == m.state.OTP && p.Type == "onetimepin" {
				if m.state.OwnedOTP && p.Name != m.state.OTPName {
					return failure("access_conflict")
				}
				return nil
			}
		}
		return failure("access_conflict")
	}
	if m.state.OTPName != "" {
		var found IdentityProvider
		for _, p := range cloud.IDPs {
			if p.Name == m.state.OTPName {
				if found.ID != "" || p.Type != "onetimepin" {
					return failure("access_conflict")
				}
				found = p
			}
		}
		if found.ID != "" {
			m.state.OTP = found.ID
			m.state.OTPPending = false
			m.state.OwnedOTP = true
			return m.persist()
		}
	}
	if m.state.OTPPending {
		return failure("access_conflict")
	}
	if m.state.OTPName == "" {
		for _, p := range cloud.IDPs {
			if p.Type == "onetimepin" && p.ID != "" {
				m.state.OTP = p.ID
				return m.persist()
			}
		}
		nonce, e := randomID()
		if e != nil {
			return e
		}
		m.state.OTPName = "Connect " + m.state.Owner + " " + nonce + " email PIN"
		m.state.OwnedOTP = true
		if e = m.persist(); e != nil {
			return e
		}
	}
	m.state.OTPPending = true
	if e := m.persist(); e != nil {
		return e
	}
	p, e := m.cfg.API.CreateOTP(ctx, c, m.state.OTPName)
	if e != nil {
		if definiteCreateRefusal(e) {
			m.state.OTPPending = false
			if pe := m.persist(); pe != nil {
				return pe
			}
		}
		return e
	}
	if p.ID == "" || p.Name != m.state.OTPName || p.Type != "onetimepin" {
		return failure("access_readback_failed")
	}
	m.state.OTP = p.ID
	m.state.OTPPending = false
	return m.persist()
}
func (m *Manager) cleanupOTP(ctx context.Context, c Credential, cloud inventory) error {
	if !m.state.OwnedOTP {
		m.state.OTP = ""
		return m.persist()
	}
	// A lost create response must be resolved before cleanup too.
	if m.state.OTP == "" {
		for _, p := range cloud.IDPs {
			if p.Name == m.state.OTPName {
				if m.state.OTP != "" || p.Type != "onetimepin" {
					return failure("access_conflict")
				}
				m.state.OTP = p.ID
			}
		}
		if m.state.OTP == "" && m.state.OTPPending {
			return failure("access_conflict")
		}
		if e := m.persist(); e != nil {
			return e
		}
	}
	if m.state.OTP != "" {
		apps, e := m.cfg.API.Apps(ctx, c)
		if e != nil {
			return e
		}
		for _, a := range apps {
			if len(a.AllowedIDPs) == 0 || slices.Contains(a.AllowedIDPs, m.state.OTP) {
				return failure("access_cleanup_required")
			}
		}
		owned := false
		for _, p := range cloud.IDPs {
			if p.ID == m.state.OTP {
				if p.Name != m.state.OTPName || p.Type != "onetimepin" {
					return failure("access_conflict")
				}
				owned = true
			}
		}
		if owned {
			if e = m.cfg.API.DeleteOTP(ctx, c, m.state.OTP); e != nil {
				return e
			}
			rows, e := m.cfg.API.IDPs(ctx, c)
			if e != nil {
				return e
			}
			for _, p := range rows {
				if p.ID == m.state.OTP {
					return failure("access_cleanup_required")
				}
			}
		}
	}
	m.state.OTP = ""
	m.state.OTPName = ""
	m.state.OTPPending = false
	m.state.OwnedOTP = false
	return m.persist()
}

// CheckCredential is read-only; failed replacement never changes the live secret.
func (m *Manager) CheckCredential(ctx context.Context, c Credential) error {
	if !m.InUse() {
		return nil
	}
	v := m.view.Load()
	if v.Account != "" && v.Account != c.Account {
		return failure("access_account_changed")
	}
	_, e := m.cfg.API.Preflight(ctx, c)
	return e
}

// Run has no ticker when unused; the daemon starts it through connector.Demand.
func (m *Manager) Run(ctx context.Context) {
	delay := time.Duration(0)
	backoff := 30 * time.Second
	for {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
			case <-m.wake:
				timer.Stop()
			}
		}
		bounded, cancel := context.WithTimeout(ctx, 25*time.Second)
		e := m.Sync(bounded)
		cancel()
		if ctx.Err() != nil {
			return
		}
		if e == nil && m.cfg.Reconciled != nil {
			m.cfg.Reconciled(ctx)
		}
		if !m.InUse() {
			return
		}
		delay = 30 * time.Second
		if e != nil {
			delay = backoff
			backoff = min(5*time.Minute, backoff*2)
			var ae *apiError
			if errors.As(e, &ae) && ae.retry > delay {
				delay = min(24*time.Hour, ae.retry)
			}
			var b [1]byte
			_, _ = rand.Read(b[:])
			delay += time.Duration(b[0]) * time.Millisecond * 20
		} else {
			backoff = 30 * time.Second
		}
	}
}

func (m *Manager) ValidateToken(ctx context.Context, c Credential) error {
	_, e := m.cfg.API.Preflight(ctx, c)
	return e
}
func (m *Manager) PrepareAccount(ctx context.Context, account, host string) error {
	v := m.view.Load()
	if m.Enabled() && v.Account != account {
		return failure("access_account_changed")
	}
	return m.Prepare(ctx, []string{host})
}

func (m *Manager) Trigger() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

func (m *Manager) endpoints() (Inventory, error) {
	v, e := m.cfg.Endpoints()
	v.Hosts = append([]string(nil), v.Hosts...)
	sort.Strings(v.Hosts)
	v.Hosts = slices.Compact(v.Hosts)
	if len(v.Hosts) > 1024 {
		return v, failure("access_inventory_incomplete")
	}
	return v, e
}
func (m *Manager) checkInventory(before Inventory) error {
	after, e := m.endpoints()
	if e != nil {
		return e
	}
	if before.DSMHost != after.DSMHost || before.Account != after.Account || before.Unsupported != after.Unsupported || !slices.Equal(before.Hosts, after.Hosts) {
		return failure("access_revision_changed")
	}
	return nil
}

// Unrestricted serializes direct-origin connector admission with restriction
// acceptance. It cannot start a Quick or legacy token tunnel behind a new list.
func (m *Manager) Unrestricted(run func() error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Enabled() {
		return failure("access_alias_unsupported")
	}
	return run()
}
