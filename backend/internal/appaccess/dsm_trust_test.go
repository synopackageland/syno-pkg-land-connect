package appaccess

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"dsmapp/internal/apps"
	"dsmapp/internal/cloudflare"
	"dsmapp/internal/connector"
	"dsmapp/internal/provider"
)

// Activation runs under the lifecycle's state lock. An already-bound DSM
// endpoint must verify fresh TLS without trying to acquire that lock again.
func TestBoundDSMValidationUnderLifecycleLockDoesNotWrite(t *testing.T) {
	f := newFixture(t, false)
	ctx := context.Background()
	if err := f.owner.TrustDSMFirstUse(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.owner.BindTrustedDSM(ctx, apps.Public, "nas.example.com"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.store.Dir, "apps-state.json")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	err = f.store.Update(ctx, func(s *apps.State) error {
		check, cancel := context.WithTimeout(ctx, 250*time.Millisecond)
		defer cancel()
		if err := f.owner.BindTrustedDSM(check, apps.Public, "nas.example.com"); err != nil {
			return fmt.Errorf("bound endpoint reentered lifecycle lock: %w", err)
		}
		after, err := os.Stat(path)
		if err != nil {
			return err
		}
		if !after.ModTime().Equal(before.ModTime()) {
			return errors.New("validation rewrote approval")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestDSMAdminFirstUseAndChangedCertificate(t *testing.T) {
	for _, web := range []bool{false, true} {
		t.Run(map[bool]string{false: "self-signed", true: "public-CA"}[web], func(t *testing.T) {
			ctx := context.Background()
			f := newFixture(t, web)
			public := "nas.example.com"
			private := "nas.example.ts.net:5443"
			if err := f.owner.BindTrustedDSM(ctx, apps.Public, public); connector.ErrorCode(err) != "pin_proxy_required" {
				t.Fatal("restore established trust", err)
			}
			if err := f.owner.TrustDSMFirstUse(ctx); err != nil {
				t.Fatal(err)
			}
			for kind, authority := range map[string]string{apps.Public: public, apps.Private: private} {
				if err := f.owner.BindTrustedDSM(ctx, kind, authority); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := f.store.Load()
			if before.DSM.Pin.CertSHA256 == "" || before.DSM.Certificate == nil || before.DSM.Certificate.Issuer == "" || before.DSM.Certificate.Expires == "" {
				t.Fatalf("missing trusted details: %+v", before.DSM)
			}
			if _, err := cloudflare.ApprovedDSMTarget(f.store, f.owner.cfg.DSMPort)(ctx, public); err != nil {
				t.Fatal(err)
			}
			resolver := provider.ApprovedPrivateTargets{Store: f.store, DSMPort: f.owner.cfg.DSMPort, EnsureDSM: f.owner.BindTrustedDSM}
			input := []provider.PortMapping{{Protocol: "https", LocalPort: f.dsm.port, PublicPort: 5443}}
			if _, err := resolver.ResolvePrivateTargets(ctx, input, "nas.example.ts.net"); err != nil {
				t.Fatal(err)
			}
			// Renew with the SAME key. A CA chain or unchanged SPKI is not consent
			// to a new DSM certificate. Both pin modes must refuse it immediately.
			old := f.dsm.cert.Load()
			template := *old.Leaf
			template.SerialNumber = big.NewInt(time.Now().UnixNano())
			template.NotAfter = template.NotAfter.Add(time.Hour)
			key := old.PrivateKey.(crypto.Signer)
			der, err := x509.CreateCertificate(rand.Reader, &template, &template, key.Public(), key)
			if err != nil {
				t.Fatal(err)
			}
			leaf, _ := x509.ParseCertificate(der)
			renewed := &tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
			f.roots.AddCert(leaf)
			f.dsm.cert.Store(renewed)
			for kind, authority := range map[string]string{apps.Public: public, apps.Private: private} {
				if err := f.owner.BindTrustedDSM(ctx, kind, authority); connector.ErrorCode(err) != "pin_proxy_required" {
					t.Fatal("renewal silently trusted", err)
				}
			}
			if _, err := resolver.ResolvePrivateTargets(ctx, input, "nas.example.ts.net"); connector.ErrorCode(err) != "pin_proxy_required" {
				t.Fatal("private bypass", err)
			}
			if code, _, err := request(t, f.store.Dir, before.DSM.Public.Socket, public); err == nil && code == 200 {
				t.Fatal("old proxy served renewed certificate")
			}
			if err := f.owner.TrustDSMFirstUse(ctx); err != nil {
				t.Fatal(err)
			}
			unchanged, _ := f.store.Load()
			if !reflect.DeepEqual(before.DSM.Pin, unchanged.DSM.Pin) {
				t.Fatal("reconnect replaced pin")
			}
			review, err := f.api.InspectDSM(ctx, apps.DSMInspectRequest{Scope: apps.Public})
			if err != nil || review.DSM == nil || review.DSM.Pin.Previous == nil {
				t.Fatal("no changed review", review, err)
			}
			if _, err = f.api.SetDSM(ctx, apps.DSMRequest{Scope: apps.Public, DSM: review.DSM, Token: review.Token}); err != nil {
				t.Fatal(err)
			}
			after, _ := f.store.Load()
			if after.DSM.Public == nil || after.DSM.Private == nil || after.DSM.Pin.CertSHA256 == before.DSM.Pin.CertSHA256 {
				t.Fatal("trust did not restore both scopes", after.DSM)
			}
			if review, err = f.api.InspectDSM(ctx, apps.DSMInspectRequest{Scope: apps.Public}); err != nil || review.DSM != nil {
				t.Fatal("public review remained", review, err)
			}
			if _, err = resolver.ResolvePrivateTargets(ctx, input, "nas.example.ts.net"); err != nil {
				t.Fatal("private did not recover", err)
			}
		})
	}
}

func TestDSMRetainedTrustAfterEndpointWithdrawal(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, false)
	if err := f.owner.TrustDSMFirstUse(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.owner.BindTrustedDSM(ctx, apps.Public, "nas.example.com"); err != nil {
		t.Fatal(err)
	}
	state, _ := f.store.Load()
	binding := apps.PinBindings(state)[0]
	if err := (apps.StorePinState{Store: f.store}).Suspend(ctx, binding, "cert_mismatch", false); err != nil {
		t.Fatal(err)
	}
	f.dsm.cert.Store(testCertificate(t))
	if err := f.owner.TrustDSMFirstUse(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.owner.BindTrustedDSM(ctx, apps.Public, "nas.example.com"); connector.ErrorCode(err) != "pin_proxy_required" {
		t.Fatal("withdrawal lost trust history", err)
	}
	review, err := f.api.InspectDSM(ctx, apps.DSMInspectRequest{Scope: apps.Public})
	if err != nil || review.DSM == nil || review.DSM.Pin.PreviousSPKI == "" {
		t.Fatal("missing previous pin", review, err)
	}
}

func TestUnchangedDSMTrustRepairsWithdrawnEndpointWithoutReview(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, false)
	if err := f.owner.TrustDSMFirstUse(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.owner.BindTrustedDSM(ctx, apps.Public, "nas.example.com"); err != nil {
		t.Fatal(err)
	}
	state, _ := f.store.Load()
	binding := apps.PinBindings(state)[0]
	if err := (apps.StorePinState{Store: f.store}).Suspend(ctx, binding, "cert_mismatch", false); err != nil {
		t.Fatal(err)
	}
	review, err := f.api.InspectDSM(ctx, apps.DSMInspectRequest{Scope: apps.Public})
	if err != nil || review.DSM != nil {
		t.Fatal("unchanged cert asked for trust again", review, err)
	}
	if _, err = cloudflare.ApprovedDSMTarget(f.store, f.owner.cfg.DSMPort)(ctx, "nas.example.com"); err != nil {
		t.Fatal("valid trust stayed stuck", err)
	}
}

func TestQuickDSMUsesPinnedHTTPSAndOnlyCurrentAuthority(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, false)
	if err := f.owner.TrustDSMFirstUse(ctx); err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"first.trycloudflare.com", "second.trycloudflare.com"} {
		if err := f.owner.BindTrustedDSM(ctx, apps.Public, host); err != nil {
			t.Fatal(err)
		}
		state, err := f.store.Load()
		if err != nil || state.DSM.Public.Socket != apps.QuickDSMSocketID {
			t.Fatal("unstable Quick socket", err)
		}
		if code, _, err := request(t, f.store.Dir, apps.QuickDSMSocketID, host); err != nil || code != 200 {
			t.Fatal("pinned HTTPS failed", code, err)
		}
		if code, _, err := request(t, f.store.Dir, apps.QuickDSMSocketID, "other.trycloudflare.com"); err == nil && code == 200 {
			t.Fatal("foreign authority forwarded")
		}
	}
	state, _ := f.store.Load()
	f.dsm.cert.Store(testCertificate(t))
	f.dsm.server.CloseClientConnections()
	if err := f.owner.BindTrustedDSM(ctx, apps.Public, "second.trycloudflare.com"); connector.ErrorCode(err) != "pin_proxy_required" {
		t.Fatal("changed certificate trusted", err)
	}
	if code, _, err := request(t, f.store.Dir, apps.QuickDSMSocketID, "second.trycloudflare.com"); err == nil && code == 200 {
		t.Fatal("replacement certificate served")
	}
	after, _ := f.store.Load()
	if !reflect.DeepEqual(state.DSM.Pin, after.DSM.Pin) {
		t.Fatal("background rebind changed trust")
	}
}
