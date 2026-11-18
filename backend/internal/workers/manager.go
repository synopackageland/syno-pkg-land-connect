package workers

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"dsmapp/internal/accounttoken"
	"dsmapp/internal/apps"
	"dsmapp/internal/connector"
	"dsmapp/internal/publicname"
	"dsmapp/internal/relay"
)

// Manager belongs to connectd; its mutex serializes credentials/inventory writes.
// Reconcile is local-only. Confirmed removal uses DeleteApp after withdrawal.
type Manager struct {
	Store Store
	API   API
	Run   func(context.Context, relay.Config)
	mu    sync.Mutex
	live  map[string]connection
	wg    sync.WaitGroup
	// checked keeps the last Signal bridge permission check; clock is for tests.
	checked              bridgeCheck
	clock                func() time.Time
	ValidateAccountToken func(context.Context, accounttoken.Credential) error
	KeepAccountToken     func() bool
	// KeepSignalBridge reports whether the self-hosted NetBird server is on:
	// its devices reach Signal only through the bridge this token deploys.
	KeepSignalBridge   func() bool
	CheckBridgeRemoval func() error
}
type connection struct {
	cancel            context.CancelFunc
	socket, authority string
	connected         *atomic.Bool
	started           time.Time
}

func randomHex(n int) (string, error) {
	raw := make([]byte, n)
	_, err := rand.Read(raw)
	return hex.EncodeToString(raw), err
}
func status(v privateState) apps.WorkersStatus {
	out := apps.WorkersStatus{Connected: v.Token != "" && !v.PendingSubdomain && !v.FreeLinkOff, Token: v.Token != "" && !v.Invalid && !v.Disconnecting, Invalid: v.Invalid || v.Disconnecting, AccountID: v.AccountID, Subdomain: v.Subdomain}
	if v.Disconnecting {
		out.Code = "workers_cleanup_required"
	}
	if v.PendingSubdomain && !v.FreeLinkOff {
		out.Code = "workers_subdomain_required"
	}
	return out
}

func (m *Manager) Status(context.Context) (apps.WorkersStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, err := m.Store.load()
	if err != nil {
		return apps.WorkersStatus{}, failure("workers_state_corrupt")
	}
	out := status(v)
	out.RelayState = "disconnected"
	if len(m.live) > 0 {
		out.RelayState = "connected"
		for _, c := range m.live {
			if !c.connected.Load() {
				out.RelayState = "connecting"
				if time.Since(c.started) >= time.Minute {
					out.RelayState = "reconnecting"
					break
				}
			}
		}
	} else if out.Connected {
		out.RelayState = "reconnecting"
	}
	if v.PendingSubdomain && !v.FreeLinkOff {
		out.Code = "workers_subdomain_required"
	}
	if v.Disconnecting {
		out.Code = "workers_cleanup_required"
	}
	return out, nil
}
func (m *Manager) Connect(ctx context.Context, in apps.WorkersConnectRequest) (apps.WorkersStatus, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !validToken(in.Token) || in.AccountID != "" && !accountPattern.MatchString(in.AccountID) || in.Subdomain != "" && !labelPattern.MatchString(in.Subdomain) {
		return apps.WorkersStatus{}, apps.ErrInvalid
	}
	old, err := m.Store.load()
	if err != nil {
		return apps.WorkersStatus{}, failure("workers_state_corrupt")
	}

	if in.AccountID == "" {
		in.AccountID = old.AccountID
		if in.AccountID == "" {
			in.AccountID, err = m.API.account(ctx, in.Token)
			if err != nil {
				return apps.WorkersStatus{}, err
			}
		}
	}
	if old.Owner != "" && old.AccountID != in.AccountID {
		return apps.WorkersStatus{}, failure("workers_disconnect_required")
	}
	sub, err := m.API.validate(ctx, in.Token, in.AccountID)
	if err != nil {
		return apps.WorkersStatus{}, err
	}
	// An account without a workers.dev name gets the one the administrator
	// chose; without one nothing is saved and the page asks for it.
	missingSub := sub == ""
	if missingSub {
		sub = in.Subdomain
		if sub == "" && old.PendingSubdomain {
			sub = old.Subdomain
		}
		if sub == "" && !in.NoDSM {
			return apps.WorkersStatus{AccountID: in.AccountID, Code: "workers_subdomain_required"}, nil
		}
	}
	if sub != "" && !labelPattern.MatchString(sub) {
		return apps.WorkersStatus{}, failure("workers_subdomain_invalid")
	}
	// A name Cloudflare reports is a rename made in its dashboard: every
	// Worker already answers under it, so the saved name follows. Only a name
	// typed here for an account that lost its own can strand existing Workers.
	if missingSub && old.Token != "" && old.Subdomain != sub && len(old.Records) > 0 {
		return apps.WorkersStatus{}, failure("workers_disconnect_required")
	}
	newInventory := old.Owner == ""
	if old.Owner == "" {
		old.Owner, err = randomHex(16)
		if err != nil {
			return apps.WorkersStatus{}, failure("workers_unavailable")
		}
	}
	if m.ValidateAccountToken != nil {
		if e := m.ValidateAccountToken(ctx, accounttoken.Credential{Account: in.AccountID, Token: in.Token}); e != nil {
			return apps.WorkersStatus{}, e
		}
	}
	if m.Store.Credentials != nil {
		if e := m.Store.Credentials.Save(accounttoken.Credential{Account: in.AccountID, Token: in.Token}); e != nil {
			return apps.WorkersStatus{}, failure("workers_state_corrupt")
		}
	}
	old.Token, old.AccountID, old.Subdomain = in.Token, in.AccountID, sub
	old.PendingSubdomain = missingSub
	old.Invalid = false
	if !in.NoDSM {
		old.FreeLinkOff = false
	} else if newInventory {
		old.FreeLinkOff = true
	}
	if err = m.Store.save(old); err != nil {
		return apps.WorkersStatus{}, failure("workers_state_corrupt")
	}
	if missingSub && sub != "" {
		if err = m.API.ensureSubdomain(ctx, old); err != nil {
			return apps.WorkersStatus{}, err
		}
		old.PendingSubdomain = false
		if m.Store.save(old) != nil {
			return apps.WorkersStatus{}, failure("workers_state_corrupt")
		}
	}
	return status(old), nil
}

// Follow re-reads the account's workers.dev name and saves it when Cloudflare
// reports a new one: a rename in the Cloudflare dashboard moves every Worker
// to <name>.<new>.workers.dev at once, with nothing to redeploy. It runs on a
// status request only. It returns the saved name, empty while not connected.
func (m *Manager) Follow(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, err := m.Store.load()
	if err != nil {
		return "", failure("workers_state_corrupt")
	}
	if v.Token == "" || v.Disconnecting || v.PendingSubdomain || v.FreeLinkOff {
		return "", nil
	}
	if v.Invalid {
		return v.Subdomain, nil
	}
	sub, err := m.API.subdomain(ctx, v)
	if err != nil {
		m.noteFailure(&v, err)
		return v.Subdomain, err
	}
	if sub == "" || sub == v.Subdomain || !labelPattern.MatchString(sub) {
		return v.Subdomain, nil
	}
	v.Subdomain = sub
	if m.Store.save(v) != nil {
		return "", failure("workers_state_corrupt")
	}
	return sub, nil
}

func shortName(pkg string) string { return publicname.ShortName(pkg) }

// defaultName checks local reservations and exact cloud ownership at each rung.
func (m *Manager) defaultName(ctx context.Context, v privateState, pkg string) (string, error) {
	name, err := publicname.Choose(ctx, shortName(pkg), publicname.NASName(), func(name string) (bool, error) {
		if recorded(v, pkg, name) {
			return false, nil
		}
		if _, err := m.API.owned(ctx, v, Record{Name: name}); err != nil {
			if connector.ErrorCode(err) == "workers_name_conflict" {
				return false, nil
			}
			return false, sanitize(err)
		}
		return true, nil
	})
	if err == publicname.ErrTaken {
		return "", failure("workers_name_conflict")
	}
	return name, err
}

func recorded(v privateState, pkg, name string) bool {
	for key, r := range v.Records {
		if key != pkg && (r.Name == name || slices.Contains(r.Previous, name)) {
			return true
		}
	}
	return false
}
func (m *Manager) Authority(ctx context.Context, pkg string) (string, error) {
	v, err := m.Store.load()
	if err != nil || v.Token == "" || v.Disconnecting || v.PendingSubdomain || v.FreeLinkOff {
		return "", failure("workers_not_connected")
	}
	name, err := m.selectedName(ctx, v, pkg)
	if err != nil {
		return "", err
	}
	return name + "." + v.Subdomain + ".workers.dev", ctx.Err()
}

// Ensure is explicit administrator intent, after the approval token is checked.
// Record the exact Worker and secret before the first possibly delivered upload.
func (m *Manager) Ensure(ctx context.Context, pkgs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, err := m.Store.load()
	if err != nil || v.Token == "" || v.Disconnecting || v.PendingSubdomain || v.FreeLinkOff {
		return failure("workers_not_connected")
	}
	if err = m.check(ctx, v, pkgs); err != nil {
		return err
	}
	for _, pkg := range pkgs {
		r, ok := v.Records[pkg]
		selected, e := m.selectedName(ctx, v, pkg)
		if e != nil {
			return e
		}
		if ok && r.Name != selected {
			r.Previous = append(r.Previous, r.Name)
			r.Name = selected
			r.Deployed = false
			v.Records[pkg] = r
			if m.Store.save(v) != nil {
				return failure("workers_state_corrupt")
			}
		}
		if !ok {
			r.Name, err = m.selectedName(ctx, v, pkg)
			if err != nil {
				return err
			}
			for key, other := range v.Records {
				if key != pkg && other.Name == r.Name {
					return failure("workers_name_conflict")
				}
			}
			r.Secret, err = randomHex(32)
			if err != nil {
				return failure("workers_unavailable")
			}
			v.Records[pkg] = r
			if m.Store.save(v) != nil {
				return failure("workers_state_corrupt")
			}
		}
		if r.Deployed {
			owned, e := m.API.owned(ctx, v, r)
			if e != nil {
				e = sanitize(e)
				m.noteFailure(&v, e)
				return e
			}
			if owned {
				continue
			}
			r.Deployed = false
			v.Records[pkg] = r
			if m.Store.save(v) != nil {
				return failure("workers_state_corrupt")
			}
		}
		if err = m.API.deploy(ctx, v, r); err != nil {
			m.noteFailure(&v, err)
			return err
		}
		r.Deployed = true
		v.Records[pkg] = r
		if m.Store.save(v) != nil {
			return failure("workers_state_corrupt")
		}
	}
	return nil
}
func (m *Manager) Reconcile(ctx context.Context, bindings []apps.PinBinding) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.live == nil {
		m.live = map[string]connection{}
	}
	v, err := m.Store.load()
	wanted := map[string]apps.PinBinding{}
	if err == nil && !v.Disconnecting && !v.PendingSubdomain && !v.FreeLinkOff {
		for _, b := range bindings {
			if b.Scope == apps.Public && b.Provider == apps.Workers {
				key := b.Package
				if b.Target == apps.TargetDSM {
					key = ""
				}
				r, ok := v.Records[key]
				if ok && r.Deployed && b.Authority == r.Name+"."+v.Subdomain+".workers.dev" {
					wanted[key] = b
				}
			}
		}
	}
	for key, live := range m.live {
		b, ok := wanted[key]
		if !ok || live.socket != b.Socket || live.authority != b.Authority {
			live.cancel()
			delete(m.live, key)
		}
	}
	if err != nil {
		return failure("workers_state_corrupt")
	}
	for key, b := range wanted {
		if _, ok := m.live[key]; ok {
			continue
		}
		r := v.Records[key]
		h := hmac.New(sha256.New, []byte(r.Secret))
		h.Write([]byte("connect:default"))
		token := base64.RawURLEncoding.EncodeToString(h.Sum(nil))
		connCtx, cancel := context.WithCancel(ctx)
		connected := &atomic.Bool{}
		m.live[key] = connection{cancel: cancel, socket: b.Socket, authority: b.Authority, connected: connected, started: time.Now()}
		cfg := relay.Config{URL: "wss://" + b.Authority + "/_spl/relay/connect", Token: token, Socket: apps.PinSocket(b.Socket), Authority: b.Authority, Connected: connected.Store}
		run := m.Run
		if run == nil {
			run = relay.Run
		}
		m.wg.Add(1)
		go func() { defer m.wg.Done(); run(connCtx, cfg) }()
	}
	for key := range wanted {
		if err = m.finishRename(ctx, &v, key); err != nil {
			return err
		}
	}
	return nil
}
func (m *Manager) Disconnect(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, err := m.Store.load()
	if err != nil {
		return failure("workers_state_corrupt")
	}
	if v.Token == "" {
		return nil
	}
	keepBridge := m.KeepSignalBridge != nil && m.KeepSignalBridge()
	v.Disconnecting = true
	if m.Store.save(v) != nil {
		return failure("workers_state_corrupt")
	}
	for key, c := range m.live {
		c.cancel()
		delete(m.live, key)
	}
	if !keepBridge {
		if err = m.removeBridge(ctx, &v); err != nil {
			return err
		}
	}
	for key, r := range v.Records {
		for _, name := range r.Previous {
			if err = m.API.delete(ctx, v, Record{Name: name, Secret: r.Secret}); err != nil {
				return err
			}
		}
		if err = m.API.delete(ctx, v, r); err != nil {
			m.noteFailure(&v, err)
			return err
		}
		delete(v.Records, key)
		if m.Store.save(v) != nil {
			return failure("workers_state_corrupt")
		}
	}
	// The account is shared with Signal and Access; neither is a Free link.
	if keepBridge || m.KeepAccountToken != nil && m.KeepAccountToken() {
		v.FreeLinkOff = true
		v.Disconnecting = false
		v.Invalid = false
		if m.Store.save(v) != nil {
			return failure("workers_state_corrupt")
		}
		return nil
	}
	if m.Store.Credentials != nil {
		if m.Store.Credentials.Remove() != nil {
			return failure("workers_state_corrupt")
		}
	}
	if err = m.Store.remove(); err != nil {
		return failure("workers_state_corrupt")
	}
	return nil
}
func (m *Manager) Close() {
	m.mu.Lock()
	for key, c := range m.live {
		c.cancel()
		delete(m.live, key)
	}
	m.mu.Unlock()
	m.wg.Wait()
}

// DeleteApp follows durable local withdrawal on Clear or confirmed removal.
// The shared DSM Worker stays until its approval is cleared or Disconnect.
func (m *Manager) DeleteApp(ctx context.Context, pkg string) error {
	if pkg == "" {
		return apps.ErrInvalid
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	v, err := m.Store.load()
	if err != nil {
		return failure("workers_state_corrupt")
	}
	r, ok := v.Records[pkg]
	if !ok {
		return nil
	}
	if err = m.API.delete(ctx, v, r); err != nil {
		m.noteFailure(&v, err)
		return err
	}
	delete(v.Records, pkg)
	if m.Store.save(v) != nil {
		return failure("workers_state_corrupt")
	}
	return nil
}

func (m *Manager) noteFailure(v *privateState, err error) {
	code := connector.ErrorCode(err)
	if code == "workers_token_invalid" || code == "workers_permission_denied" {
		v.Invalid = true
		_ = m.Store.save(*v)
	}
}

// selectedName keeps a saved name, then takes the approved one, else the default.
func (m *Manager) selectedName(ctx context.Context, v privateState, pkg string) (string, error) {
	if host := apps.Hostname(ctx, pkg); host != "" {
		label, ok := strings.CutSuffix(host, "."+v.Subdomain+".workers.dev")
		if !ok || !labelPattern.MatchString(label) {
			return "", apps.ErrInvalid
		}

		if old, ok := v.Records[pkg]; ok && old.Name != label && pkg != "" {
			return "", failure("workers_name_conflict")
		}
		return label, nil
	}
	if old, ok := v.Records[pkg]; ok {
		return old.Name, nil
	}
	return m.defaultName(ctx, v, pkg)
}
func (m *Manager) check(ctx context.Context, v privateState, pkgs []string) error {
	names := map[string]string{}
	for pkg, r := range v.Records {
		names[r.Name] = pkg
		for _, name := range r.Previous {
			names[name] = pkg
		}
	}
	for _, pkg := range pkgs {
		name, err := m.selectedName(ctx, v, pkg)
		if err != nil {
			return err
		}
		if other, ok := names[name]; ok && other != pkg {
			return m.nameConflict(ctx, v, name)
		}
		names[name] = pkg
		if _, err = m.API.owned(ctx, v, Record{Name: name}); err != nil {
			if connector.ErrorCode(err) == "workers_name_conflict" {
				return m.nameConflict(ctx, v, name)
			}
			return sanitize(err)
		}
	}
	return nil
}
func (m *Manager) Check(ctx context.Context, pkgs []string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, err := m.Store.load()
	if err != nil || v.Token == "" || v.FreeLinkOff {
		return failure("workers_not_connected")
	}
	return m.check(ctx, v, pkgs)
}

func (m *Manager) nameConflict(ctx context.Context, v privateState, name string) error {
	d := connector.NameConflict{Reason: "other", ConflictName: name + "." + v.Subdomain + ".workers.dev", DNSURL: "https://dash.cloudflare.com/" + v.AccountID + "/workers-and-pages"}
	for i := 2; i <= 100; i++ {
		suffix := "-" + strconv.Itoa(i)
		candidate := strings.TrimRight(name[:min(len(name), 63-len(suffix))], "-") + suffix
		if recorded(v, "", candidate) {
			continue
		}
		owned, err := m.API.owned(ctx, v, Record{Name: candidate})
		if connector.ErrorCode(err) == "workers_name_conflict" {
			continue
		}
		if err != nil {
			break
		}
		if !owned {
			d.SuggestedName = candidate + "." + v.Subdomain + ".workers.dev"
			break
		}
	}
	return connector.WithNameConflict(failure("workers_name_conflict"), d)
}

// FinishRename runs after durable DSM publication and stream reconciliation.
// Previous names remain in the private inventory after a failed cloud delete.
func (m *Manager) FinishRename(ctx context.Context, pkg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, err := m.Store.load()
	if err != nil {
		return failure("workers_state_corrupt")
	}
	return m.finishRename(ctx, &v, pkg)
}
func (m *Manager) finishRename(ctx context.Context, v *privateState, pkg string) error {
	r := v.Records[pkg]
	for len(r.Previous) > 0 {
		name := r.Previous[0]
		if name != r.Name {
			if err := m.API.delete(ctx, *v, Record{Name: name, Secret: r.Secret}); err != nil {
				m.noteFailure(v, err)
				return err
			}
		}
		r.Previous = r.Previous[1:]
		v.Records[pkg] = r
		if m.Store.save(*v) != nil {
			return failure("workers_state_corrupt")
		}
	}
	return nil
}
