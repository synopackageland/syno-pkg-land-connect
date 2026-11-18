package cloudflare

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"dsmapp/internal/connector"
	"dsmapp/internal/dependency"
)

var errReconnect = connector.Coded("cloudflare_reconnect_required", "reconnect Cloudflare authorization")
var errAuthorizationBusy = connector.Coded("cloudflare_busy", "finish the pending Cloudflare change first")

// Authorization describes local retained authorization, never its token.
type Authorization struct {
	Connected bool   `json:"connected"`
	Zone      string `json:"zone,omitempty"`
}

func (t *Tunnel) Authorization(ctx context.Context) (Authorization, error) {
	t.ops.Lock()
	defer t.ops.Unlock()
	signed, e := readCert(t.certFile())
	if e != nil {
		return Authorization{}, errReconnect
	}
	zone, e := t.cfg.API.ZoneName(ctx, signed)
	if e != nil {
		return Authorization{}, errReconnect
	}
	return Authorization{Connected: true, Zone: zone}, nil
}

func (t *Tunnel) stopAuthorization() {
	if t.authCancel != nil {
		t.authCancel()
		<-t.authDone
		t.authCancel = nil
	}
}

// DisconnectAuthorization removes only login authorization. Existing ingress,
// tunnel credentials and DNS remain usable. Async DNS and rollback use ops too.
func (t *Tunnel) DisconnectAuthorization() error {
	if e := t.CheckRemoval(dependency.Authorization); e != nil {
		return e
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.safe != nil {
		return errAuthorizationBusy
	}
	t.stopAuthorization()
	t.ops.Lock()
	defer t.ops.Unlock()
	if e := os.Remove(t.certFile()); e != nil && !errors.Is(e, os.ErrNotExist) {
		return storageError(e)
	}
	return nil
}

// ReconnectAuthorization starts explicit browser authorization beside the live
// tunnel. It never changes ingress, DNS or the tunnel's credentials.
func (t *Tunnel) ReconnectAuthorization(ctx context.Context) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.safe != nil {
		return errAuthorizationBusy
	}
	status := t.Status()
	if status.State == "needs-login" {
		return errAuthorizationBusy
	}
	t.stopAuthorization()
	bin, e := t.cfg.Cloudflared(ctx)
	if e != nil {
		return connector.ErrBinaryRequired
	}
	saved, e := t.savedDomain()
	if e != nil && !errors.Is(e, errNoDomain) {
		return e
	}
	if e := os.MkdirAll(t.home(), 0700); e != nil {
		return storageError(e)
	}
	gen := t.beside(connector.Status{State: "needs-login", Mode: "domain", Hostname: saved.Hostname})
	job, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	t.authCancel, t.authDone = cancel, done
	go func() {
		defer close(done)
		t.ops.Lock()
		defer t.ops.Unlock()
		_, zone, e := t.signedIn(job, gen, bin)
		if e == nil && saved.Hostname != "" && !strings.HasSuffix(saved.Hostname, "."+zone) {
			e = errZone
		}
		if e != nil {
			_ = os.Remove(t.certFile())
		}
		t.smu.Lock()
		if t.aside != nil && t.asideGen == gen {
			t.aside = nil
			t.status.Code = ""
			if e != nil {
				t.status.Code = failCode(e)
			}
		}
		t.smu.Unlock()
	}()
	return nil
}

// AuthorizationResult matches apps.cloudflareConnect's response. The route
// owner passes only {}; authorization never accepts a browser token or zone.
type AuthorizationResult struct {
	LoginURL string `json:"loginUrl,omitempty"`
}

func (t *Tunnel) ConnectAuthorization(ctx context.Context) (AuthorizationResult, error) {
	if e := t.ReconnectAuthorization(ctx); e != nil {
		return AuthorizationResult{}, e
	}
	wait, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		s := t.Status()
		if s.LoginURL != "" {
			return AuthorizationResult{LoginURL: s.LoginURL}, nil
		}
		t.mu.Lock()
		done := t.authDone
		t.mu.Unlock()
		select {
		case <-done:
			s = t.Status()
			if s.Code != "" {
				return AuthorizationResult{}, connector.Coded(s.Code, "Cloudflare authorization failed")
			}
			return AuthorizationResult{}, nil
		case <-wait.Done():
			t.mu.Lock()
			t.stopAuthorization()
			t.mu.Unlock()
			return AuthorizationResult{}, errLoginTimeout
		case <-tick.C:
		}
	}
}
