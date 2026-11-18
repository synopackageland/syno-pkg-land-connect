package cloudflare

import (
	"context"
	"dsmapp/internal/connector"
	"dsmapp/internal/publicnet"
	"dsmapp/internal/supervise"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"strconv"
)

var errUnsafeUpdate = connector.Coded("apply_unavailable", "this change cannot preserve the existing connection")
var errUpdateRecovery = connector.Coded("apply_recovery_required", "Cloudflare rollback could not be verified")

// domainUpdate is an accepted change to the running own domain. Every change
// the page can make keeps the same ingress, so it holds no work of its own;
// while it is set, App routes and authorization leave the domain alone.
type domainUpdate struct{}

func (t *Tunnel) CheckHealth(ctx context.Context) error {
	s := t.Status()
	t.smu.Lock()
	attempt, addr := t.gen, t.healthAddr
	t.smu.Unlock()
	if s.State == "connecting" {
		return supervise.Wait("starting", attempt, errors.New("cloudflared is still connecting"))
	}
	if s.Hostname == "" {
		return supervise.Step("status", attempt, errNoDomain)
	}
	if t.cfg.AccessProtected != nil && t.cfg.AccessProtected() {
		if answered, ready := metricsReady(ctx, addr); answered && ready {
			return nil
		}
		return supervise.Wait("access-origin", attempt, connector.Coded("access_login_required", "email sign-in required; origin not externally verified"))
	}
	if t.cfg.CheckWeb != nil {
		return t.publicHealth(ctx, attempt, addr, t.cfg.CheckWeb(ctx, "https://"+s.Hostname))
	}
	if e := t.publicHealth(ctx, attempt, addr, (Probe{}).Check(ctx, "https://"+s.Hostname)); e != nil {
		return e
	}
	transport := (publicnet.Dialer{}).Transport()
	defer transport.CloseIdleConnections()
	if e := connector.CheckDSMPage(ctx, &http.Client{Transport: transport}, "https://"+s.Hostname); e != nil {
		return supervise.Wait("dsm-page", attempt, e)
	}
	return nil
}

// A DNS lookup failure is not evidence about the retained Cloudflare login.
// Wait for reachability rather than exhausting repairs and asking to sign in.
func (t *Tunnel) publicHealth(ctx context.Context, attempt uint64, addr string, e error) error {
	if errors.Is(e, errDNSPending) {
		return supervise.Wait("dns", attempt, e)
	}
	if e != nil {
		code := connector.ErrorCode(e)
		if code == "cloudflare_tunnel_pending" || code == "cloudflare_not_dsm" {
			// cloudflared's own metrics tell a lost connection to Cloudflare
			// from routing the administrator must authorize.
			switch answered, ready := metricsReady(ctx, addr); {
			case !answered:
				return supervise.Wait("metrics", attempt, e)
			case ready:
				return supervise.Step("dns", attempt, connector.Coded("apply_cloudflare_authorization_required", "verify Cloudflare DNS, ingress and Access policy; sign in again to authorize repair"))
			default:
				return supervise.Step("network", attempt, e)
			}
		}
		return probeHealth("web", attempt, e)
	}
	return nil
}

// probeHealth classifies a failed check like every provider: what a busy NAS
// could not answer in time waits, and what Cloudflare reported counts.
func probeHealth(step string, attempt uint64, e error) error {
	switch {
	case e == nil:
		return nil
	case connector.ErrorCode(e) == "", errors.Is(e, errAddressUnreached), errors.Is(e, errOriginDown):
		return supervise.Wait(step, attempt, e)
	}
	return supervise.Step(step, attempt, e)
}
func (t *Tunnel) checkPort(ctx context.Context, port int) error {
	if t.cfg.CheckPort != nil {
		return t.cfg.CheckPort(ctx, port)
	}
	c, e := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if e != nil {
		return connector.Coded("apply_target_unreachable", "a selected local service is not listening")
	}
	c.Close()
	return nil
}
func (t *Tunnel) CheckUpdate(ctx context.Context, r Request) error {
	if e := Validate(r); e != nil {
		return e
	}
	if r.Mode != "domain" || r.Subdomain != "" || r.Token != "" {
		return errUnsafeUpdate
	}
	if t.Status().State != "connected" {
		return connector.Coded("apply_recovery_required", "restore the existing Cloudflare connection first")
	}
	if _, e := t.savedDomain(); e != nil {
		return e
	}
	bin, port, e := t.prepareReadOnly(ctx)
	if e != nil {
		return e
	}
	if e = t.launcher().Run(ctx, Command{Path: bin, Args: []string{"--version"}, Home: t.home()}); e != nil {
		return errStart
	}
	return t.checkPort(ctx, port)
}
func (t *Tunnel) StageUpdate(ctx context.Context, r Request) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.safe != nil || t.cancel == nil {
		return errUnsafeUpdate
	}
	if _, e := t.savedDomain(); e != nil {
		return e
	}
	t.safe = &domainUpdate{}
	return nil
}
func (t *Tunnel) VerifyUpdate(ctx context.Context) (bool, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.safe == nil {
		return false, errUnsafeUpdate
	}
	return true, ctx.Err()
}
func (t *Tunnel) CommitUpdate(context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.safe == nil {
		return errUnsafeUpdate
	}
	return nil
}
func (t *Tunnel) UndoUpdate(context.Context) error { return nil }
func (t *Tunnel) FinishUpdate(committed bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.finishUpdate(committed)
}
func (t *Tunnel) finishUpdate(bool) { t.safe = nil }

func (t *Tunnel) SnapshotUpdate() ([]byte, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	d, e := t.savedDomain()
	if e != nil {
		return nil, e
	}
	return json.Marshal(d)
}
func (t *Tunnel) RestoreUpdate(raw []byte) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ops.Lock()
	defer t.ops.Unlock()
	var d domain
	if json.Unmarshal(raw, &d) != nil || !validHostname(d.Hostname) || !tunnelUUID.MatchString(d.TunnelID) {
		return errUpdateRecovery
	}
	if e := t.saveRecord(d); e != nil {
		return e
	}
	t.dropTCPLeftovers()
	return nil
}

// dropTCPLeftovers removes the DNS journal older versions kept while adding
// TCP service names. The names stay in the user's Cloudflare zone.
func (t *Tunnel) dropTCPLeftovers() {
	_ = os.Remove(t.domainFile("apply-dns.json"))
}

// Repair only resumes this locally-managed tunnel with its retained credentials.
// DNS/remote policy repair needs an explicit authorization action.
func (t *Tunnel) Repair(ctx context.Context) error {
	if e := t.Stop(); e != nil {
		return e
	}
	return t.Restore(ctx)
}
