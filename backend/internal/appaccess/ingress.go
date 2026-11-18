package appaccess

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net"
	"strconv"
	"time"

	"dsmapp/internal/apps"
	"dsmapp/internal/connector"
)

func ingressID(subject apps.IngressSubject, key string) string {
	sum := sha256.Sum256([]byte(subject.Package + "\x00" + key))
	return fmt.Sprintf("custom.ingress-%x", sum[:12])
}

func (e *Engine) verifyIngressIdentity(pkg string, uid int) error {
	if e.cfg.IngressIdentity != nil {
		return e.cfg.IngressIdentity(pkg, uid)
	}
	_, err := apps.VerifyIngressSubject("/var/packages", pkg, uid)
	return err
}

func (e *Engine) validateIngress(def apps.CustomApp, scope apps.Scope) error {
	g := def.Ingress
	if g == nil || g.Revoked || scope.Provider == apps.Workers || scope.Authority != g.Hostname {
		return apps.ErrInvalid
	}
	if e.cfg.Access != nil && e.cfg.Access.Enabled() {
		return connector.Coded("ingress_access_incompatible", "machine ingress requires the email gate to be off")
	}
	if err := e.verifyIngressIdentity(g.Package, g.UID); err != nil {
		return err
	}
	if !g.ValidDefinition(def) {
		return apps.ErrInvalid
	}
	if g.Socket != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		c, err := apps.DialIngressUnix(ctx, g.UID, g.Socket)
		if err == nil {
			c.Close()
		}
		return err
	}
	return apps.CheckIngressListener(g.UID, def.Port())
}

func (e *Engine) RequestIngress(ctx context.Context, subject apps.IngressSubject, in apps.IngressRequest) (apps.IngressStatus, error) {
	if subject.UID <= 0 || len(in.Key) == 0 || len(in.Key) > 64 || apps.CustomLabel(in.Key) != in.Key {
		return apps.IngressStatus{}, apps.ErrInvalid
	}
	def := apps.CustomApp{Name: subject.Package + " " + in.Key, URL: "http://127.0.0.1:" + strconv.Itoa(in.Port), AppSignIn: true,
		Ingress: &apps.IngressGrant{Package: subject.Package, UID: subject.UID, Path: in.Path, Hostname: in.Hostname, Socket: in.Socket, AdditionalPaths: append([]string(nil), in.AdditionalPaths...)}}
	if in.Socket != "" {
		if in.Port != 0 {
			return apps.IngressStatus{}, apps.ErrInvalid
		}
		def.URL = "http://package.invalid"
	}
	id := ingressID(subject, in.Key)
	err := e.cfg.Store.Update(ctx, func(state *apps.State) error {
		if old, ok := state.IngressRevokedKeys[id]; ok {
			if old != subject {
				return apps.ErrInvalid
			}
			return nil
		}
		if old, ok := state.Custom[id]; ok {
			// Revocation is a tombstone. Repeating the original request never
			// restores it; a changed tuple under the same key is refused.
			if old.Ingress == nil {
				return apps.ErrInvalid
			}
			copy := *old.Ingress
			copy.Revoked = false
			old.Ingress = &copy
			if old.Digest() != def.Digest() {
				return apps.ErrConfirmationChanged
			}
			return nil
		}
		if err := e.validateIngress(def, apps.Scope{Authority: in.Hostname}); err != nil {
			return err
		}
		if len(state.Custom) >= apps.MaxCustom {
			return apps.ErrInvalid
		}
		if state.Custom == nil {
			state.Custom = map[string]apps.CustomApp{}
		}
		state.Custom[id] = def
		_, err := state.Revision()
		return err
	})
	if err != nil {
		return apps.IngressStatus{}, err
	}
	return e.StatusIngress(ctx, subject, apps.IngressRef{ID: id})
}

func (e *Engine) ingressDefinition(subject apps.IngressSubject, ref apps.IngressRef) (apps.State, apps.CustomApp, error) {
	state, err := e.cfg.Store.Load()
	if err != nil {
		return state, apps.CustomApp{}, err
	}
	def, ok := state.Custom[ref.ID]
	if !ok || def.Ingress == nil || subject.UID <= 0 || def.Ingress.Package != subject.Package || def.Ingress.UID != subject.UID {
		return state, def, apps.ErrInvalid
	}
	return state, def, nil
}

func (e *Engine) StatusIngress(ctx context.Context, subject apps.IngressSubject, ref apps.IngressRef) (apps.IngressStatus, error) {
	ref, err := ingressReference(subject, ref)
	if err != nil {
		return apps.IngressStatus{}, err
	}
	state, err := e.cfg.Store.Load()
	if err != nil {
		return apps.IngressStatus{}, err
	}
	if old, ok := state.IngressRevokedKeys[ref.ID]; ok {
		if old != subject {
			return apps.IngressStatus{}, apps.ErrInvalid
		}
		return apps.IngressStatus{ID: ref.ID, State: "revoked", ConnectorState: "disabled"}, nil
	}
	state, def, err := e.ingressDefinition(subject, ref)
	if err != nil {
		return apps.IngressStatus{}, err
	}
	out := apps.IngressStatus{ID: ref.ID, State: "pending", ConnectorState: "disabled"}
	if e.cfg.IngressConnector != nil {
		out.ConnectorState = e.cfg.IngressConnector().State
	}
	if def.Ingress.Revoked {
		out.State = "revoked"
		return out, nil
	}
	if scope := state.Apps[ref.ID].Public; scope != nil {
		out.State = scope.State
		if scope.State == apps.Active {
			out.URL = "https://" + def.Ingress.Hostname + def.Ingress.Path
		}
	}
	if err := e.verifyIngressIdentity(def.Ingress.Package, subject.UID); err != nil {
		return out, nil
	}
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	u := "127.0.0.1:" + strconv.Itoa(def.Port())
	dial := func() (net.Conn, error) {
		if def.Ingress.Socket != "" {
			return apps.DialIngressUnix(bounded, subject.UID, def.Ingress.Socket)
		}
		return apps.DialIngress(bounded, subject.UID, u)
	}
	if c, err := dial(); err == nil {
		out.ListenerHealthy = true
		c.Close()
	}
	if scope := state.Apps[ref.ID].Public; scope != nil && scope.State == apps.Active {
		e.mu.Lock()
		_, live := e.live[scope.Socket]
		out.Ready = live && e.allowed[scope.Socket]
		e.mu.Unlock()
		out.Ready = out.Ready && out.ListenerHealthy && out.ConnectorState == "connected" && (e.cfg.Access == nil || !e.cfg.Access.Enabled())
	}
	return out, nil
}

func (e *Engine) RevokeIngress(ctx context.Context, subject apps.IngressSubject, ref apps.IngressRef) (apps.IngressStatus, error) {
	byKey := ref.Key != ""
	ref, err := ingressReference(subject, ref)
	if err != nil {
		return apps.IngressStatus{}, err
	}
	var target *apps.Target
	err = e.cfg.Store.Update(ctx, func(state *apps.State) error {
		def, exists := state.Custom[ref.ID]
		if old, ok := state.IngressRevokedKeys[ref.ID]; ok {
			if old != subject {
				return apps.ErrInvalid
			}
			return nil
		}
		if !exists && byKey {
			if subject.UID <= 0 || len(state.IngressRevokedKeys) >= apps.MaxCustom {
				return apps.ErrInvalid
			}
			if state.IngressRevokedKeys == nil {
				state.IngressRevokedKeys = map[string]apps.IngressSubject{}
			}
			state.IngressRevokedKeys[ref.ID] = subject
			_, err := state.Revision()
			return err
		}
		if def.Ingress == nil || def.Ingress.Package != subject.Package || def.Ingress.UID != subject.UID {
			return apps.ErrInvalid
		}
		if def.Ingress.Revoked {
			return nil
		}
		copy := *def.Ingress
		copy.Revoked = true
		def.Ingress = &copy
		state.Custom[ref.ID] = def
		rev, err := state.Revision()
		if err != nil {
			return err
		}
		app := state.Apps[ref.ID]
		if app.Public != nil {
			app.Rev = rev
			app.Public.State, app.Public.Reason, app.Public.Cleanup = apps.Paused, "ingress-revoked", true
			app.Public.Binding.Summary = def.Digest()
			state.Apps[ref.ID] = app
			t := apps.Target{Package: ref.ID, Kind: apps.Public, Scope: *app.Public}
			target = &t
		}
		return nil
	})
	if err != nil {
		return apps.IngressStatus{}, err
	}
	if target != nil {
		_ = e.Block(ctx, *target)
	}
	// The existing safety owner makes its final reconciliation on withdrawal.
	// A revoked grant never keeps a polling loop alive just for cloud cleanup.
	return e.StatusIngress(ctx, subject, ref)
}

// IngressOnly permits a domain tunnel without implicitly publishing DSM.
// An existing explicit DSM approval keeps its own independent meaning.
func (e *Engine) IngressOnly() bool {
	state, err := e.cfg.Store.Load()
	if err != nil || state.DSM != nil && state.DSM.Public != nil {
		return false
	}
	found := false
	for _, def := range state.Custom {
		found = found || def.Ingress != nil
	}
	for pkg, app := range state.Apps {
		if app.Public == nil || app.Public.State != apps.Active {
			continue
		}
		if state.Custom[pkg].Ingress == nil {
			return false
		}
		found = true
	}
	return found
}

func ingressReference(subject apps.IngressSubject, ref apps.IngressRef) (apps.IngressRef, error) {
	if (ref.ID == "") == (ref.Key == "") {
		return ref, apps.ErrInvalid
	}
	if ref.Key != "" {
		if len(ref.Key) > 40 || apps.CustomLabel(ref.Key) != ref.Key {
			return ref, apps.ErrInvalid
		}
		ref.ID = ingressID(subject, ref.Key)
		ref.Key = ""
	}
	return ref, nil
}
