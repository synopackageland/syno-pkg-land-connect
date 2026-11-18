package apps

import (
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

var approvedNames = []string{"nas.example.com"}

type testClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *testClock) Now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.at }
func (c *testClock) Add(d time.Duration) { c.mu.Lock(); c.at = c.at.Add(d); c.mu.Unlock() }

type pinLab struct {
	t        *testing.T
	ca       *LabCA
	clk      *testClock
	key      *ecdsa.PrivateKey
	app, ref *LabServer
	ap, rp   int
	opts     Options
}

func newPinLab(t *testing.T) *pinLab {
	t.Helper()
	ca, err := NewLabCA("Test CA")
	if err != nil {
		t.Fatal(err)
	}
	l := &pinLab{t: t, ca: ca, clk: &testClock{at: time.Now()}, key: NewKey()}
	cert := l.leaf(l.key, approvedNames, true)
	l.app = &LabServer{Name: "app", Package: "DemoApp"}
	l.ref = &LabServer{Name: "reference"}
	l.app.SetCert(cert)
	l.ref.SetCert(cert)
	l.ap, err = l.app.Start("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	l.rp, err = l.ref.Start("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.app.Close(); l.ref.Close() })
	roots := ProductRoots()
	roots.AddCert(ca.Cert)
	l.opts = Options{Roots: roots, Now: l.clk.Now, ReferencePort: func() (int, error) { return l.rp, nil }}
	return l
}
func (l *pinLab) leaf(k *ecdsa.PrivateKey, n []string, valid bool) tls.Certificate {
	from, to := l.clk.Now().Add(-time.Hour), l.clk.Now().Add(24*time.Hour)
	if !valid {
		from, to = l.clk.Now().Add(-48*time.Hour), l.clk.Now().Add(-24*time.Hour)
	}
	return Leaf(l.ca, k, n, from, to)
}
func (l *pinLab) binding() PinBinding {
	return PinBinding{Target: TargetApp, Package: "DemoApp", Port: l.ap, Authority: fmt.Sprintf("nas.example.com:%d", l.ap), Mode: ModeW, Names: slices.Clone(approvedNames)}
}

// Matrix includes positive controls holding a copy of the current private key.
// Existing N must never silently become the attacker's name. Fresh approvals
// deliberately display that name and still require administrator confirmation.
func TestWMatrix(t *testing.T) {
	l := newPinLab(t)
	legit := l.leaf(l.key, approvedNames, true)
	for _, both := range []bool{false, true} {
		for _, name := range []bool{false, true} {
			for _, key := range []bool{false, true} {
				for _, valid := range []bool{false, true} {
					id := fmt.Sprintf("both=%t/name=%t/key=%t/valid=%t", both, name, key, valid)
					t.Run(id, func(t *testing.T) {
						names := approvedNames
						if !name {
							names = []string{"attacker.example.com"}
						}
						ak, rk := NewKey(), l.key
						if key {
							ak = rk
						}
						if both {
							if key {
								rk = ak
							} else {
								rk = NewKey()
							}
						}
						ac := l.leaf(ak, names, valid)
						l.app.SetCert(ac)
						if both {
							l.ref.SetCert(l.leaf(rk, names, valid))
						} else {
							l.ref.SetCert(legit)
						}
						v := NewVerifier(l.opts)
						b := l.binding()
						// Use the production TLS callback and transport: refused cases send zero
						// application bytes; accepted cases prove the byte counter sees real data.
						l.app.Reset()
						run := startPin(t, l, b, "tailscale", nil)
						res, err := run.get("/", "")
						accept := name && key && valid
						if accept && (err != nil || res.code != 200) {
							t.Fatalf("positive control: %+v %v", res, err)
						}
						if !accept && err == nil && res.code == 200 {
							t.Fatal("W accepted invalid target")
						}
						bytes := l.app.AppBytes()
						if !accept && bytes != 0 {
							t.Fatalf("refused target received %d application bytes", bytes)
						}
						if accept && bytes == 0 {
							t.Fatal("positive byte control failed")
						}
						run.stop()
						p, err := v.Propose(context.Background(), TargetApp, l.ap, "DemoApp", b.Authority)
						if err != nil || !p.RequiresConfirmation || !slices.Equal(p.Names, names) || p.Notice == "" {
							t.Fatalf("first approval: %+v %v", p, err)
						}
						expectedW := valid && key && (both || name)
						if (p.Mode == ModeW) != expectedW {
							t.Fatalf("mode %s, want W=%t", p.Mode, expectedW)
						}
					})
				}
			}
		}
	}
	t.Run("untrusted-same-key", func(t *testing.T) {
		l.app.SetCert(Leaf(nil, l.key, approvedNames, l.clk.Now().Add(-time.Hour), l.clk.Now().Add(time.Hour)))
		v := NewVerifier(l.opts)
		chain, _ := v.Probe(context.Background(), l.ap)
		if got := v.Check(context.Background(), l.binding(), chain); got != ReasonChain {
			t.Fatal(got)
		}
	})
	t.Run("product-roots-isolated", func(t *testing.T) {
		cert := l.leaf(l.key, approvedNames, true)
		if got := NewVerifier(Options{}).webPKI([]*x509.Certificate{cert.Leaf}); got != ReasonChain {
			t.Fatal(got)
		}
	})
}

func TestWGuardWitness(t *testing.T) {
	l := newPinLab(t)
	v := NewVerifier(l.opts)
	for _, name := range []string{"name", "key"} {
		t.Run(name, func(t *testing.T) {
			cert := l.leaf(l.key, []string{"attacker.example.com"}, true)
			if name == "key" {
				cert = l.leaf(NewKey(), approvedNames, true)
			}
			chain := []*x509.Certificate{cert.Leaf}
			if got := v.Check(context.Background(), l.binding(), chain); got == "" {
				t.Fatalf("%s guard accepted invalid certificate", name)
			}
		})
	}
}

// Delete guards in isolated source copies, rerun the same production verifier
// witnesses, and require the intended assertions to fail. No bypass exists in
// product Options or in the product binary.
func TestWGuardNegativeControls(t *testing.T) {
	for _, guard := range []string{"name", "key"} {
		t.Run(guard, func(t *testing.T) {
			root := t.TempDir()
			dst := filepath.Join(root, "internal", "apps")
			if err := os.MkdirAll(dst, 0700); err != nil {
				t.Fatal(err)
			}
			for _, name := range []string{"go.mod", "go.sum"} {
				data, err := os.ReadFile(filepath.Join("..", "..", name))
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(root, name), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			files, err := filepath.Glob("*.go")
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, "ingress_login.js")
			for _, name := range files {
				// Copy the verifier and its durable-state dependencies, without
				// unrelated App discovery/provider packages. The production guard
				// and its original witnesses remain identical in this mutation.
				if !strings.HasPrefix(name, "pin") && name != "entry_unavailable.go" && name != "entry.go" && name != "ingress_login.js" && name != "ingress_login.go" && name != "ingress_request.go" && name != "types.go" && name != "customdef.go" && name != "ingressdef.go" && name != "state.go" && name != "state_test.go" && name != "projection.go" && name != "launch.go" && name != "lifecycle.go" && name != "socket.go" {
					continue
				}
				data, err := os.ReadFile(name)
				if err != nil {
					t.Fatal(err)
				}
				if name == "pinverify.go" {
					old := "if !covers(chain[0], b.Names) {"
					if guard == "key" {
						old = "if ref != leaf {"
					}
					if !strings.Contains(string(data), old) {
						t.Fatal("mutation anchor missing")
					}
					data = []byte(strings.ReplaceAll(string(data), old, "if false {"))
				}
				if err = os.WriteFile(filepath.Join(dst, name), data, 0600); err != nil {
					t.Fatal(err)
				}
			}
			for _, dep := range []string{"localipc", "ipctest", "publicname"} {
				files, err := filepath.Glob(filepath.Join("..", dep, "*.go"))
				if err != nil {
					t.Fatal(err)
				}
				for _, source := range files {
					dir := filepath.Join(root, "internal", dep)
					if err := os.MkdirAll(dir, 0700); err != nil {
						t.Fatal(err)
					}
					data, err := os.ReadFile(source)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(filepath.Join(dir, filepath.Base(source)), data, 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			// Custom App errors carry connector codes in the product; the copy
			// keeps the definition rules without the connector package.
			stub := "package apps\n\nimport \"errors\"\n\nfunc customCode(_, message string) error { return errors.New(message) }\n"
			if err = os.WriteFile(filepath.Join(dst, "customcode.go"), []byte(stub), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			args := []string{"test", "./internal/apps", "-run", "^TestWGuardWitness$", "-count=1"}
			// Offline package-account gates vendor the same pinned modules.
			// Carry that tree into compiled mutation fixtures as well.
			vendor := filepath.Join("..", "..", "vendor")
			if st, err := os.Stat(vendor); err == nil && st.IsDir() {
				if err := os.CopyFS(filepath.Join(root, "vendor"), os.DirFS(vendor)); err != nil {
					t.Fatal(err)
				}
				args = append(args, "-mod=vendor")
			}
			cmd := exec.CommandContext(ctx, "go", args...)
			cmd.Dir = root
			out, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(out), guard+" guard accepted invalid certificate") {
				t.Fatalf("guard mutation did not trigger expected assertion: %v\n%s", err, out)
			}
		})
	}
}

func TestPinModesReenableAndDSM(t *testing.T) {
	l := newPinLab(t)
	v := NewVerifier(l.opts)
	ctx := context.Background()
	b := l.binding()
	t.Run("S-different-key", func(t *testing.T) {
		c := Leaf(nil, l.key, approvedNames, l.clk.Now().Add(-time.Hour), l.clk.Now().Add(time.Hour))
		l.app.SetCert(c)
		p, err := v.Propose(ctx, TargetApp, l.ap, b.Package, b.Authority)
		if err != nil || p.Mode != ModeS {
			t.Fatalf("%+v %v", p, err)
		}
		// The page shows the certificate fingerprint browsers show, beside the pinned SPKI.
		if sum := sha256.Sum256(c.Leaf.Raw); p.CertSHA256 != hex.EncodeToString(sum[:]) || p.CertSHA256 == p.SPKI {
			t.Fatalf("certificate fingerprint %q", p.CertSHA256)
		}
		sb := Confirm(p, b.Package, b.Authority)
		wrong := l.leaf(NewKey(), approvedNames, true)
		if v.Check(ctx, sb, []*x509.Certificate{wrong.Leaf}) != ReasonSPKI {
			t.Fatal("S accepted new key")
		}
		l.app.SetCert(wrong)
		r, err := v.PrepareReenable(ctx, sb)
		if err != nil || r.Action != "reconfirm" {
			t.Fatalf("%+v %v", r, err)
		}
		next, err := ConfirmReenable(sb, r)
		if err != nil || next.Mode != ModeS || next.SPKI != SPKI(wrong.Leaf) {
			t.Fatalf("%+v %v", next, err)
		}
	})
	t.Run("W-new-name", func(t *testing.T) {
		c := l.leaf(NewKey(), []string{"renewed.example.com"}, true)
		l.app.SetCert(c)
		l.ref.SetCert(c)
		r, err := v.PrepareReenable(ctx, b)
		if err != nil || r.Action != "reconfirm" || !slices.Equal(b.Names, approvedNames) {
			t.Fatalf("%+v %v", r, err)
		}
		next, err := ConfirmReenable(b, r)
		if err != nil || next.Mode != ModeW || !slices.Equal(next.Names, []string{"renewed.example.com"}) {
			t.Fatalf("%+v %v", next, err)
		}
	})
	t.Run("W-no-downgrade", func(t *testing.T) {
		c := Leaf(nil, l.key, approvedNames, l.clk.Now().Add(-time.Hour), l.clk.Now().Add(time.Hour))
		l.app.SetCert(c)
		r, err := v.PrepareReenable(ctx, b)
		if err != nil || r.Action != "blocked" || r.Mode != ModeW {
			t.Fatalf("%+v %v", r, err)
		}
	})
	t.Run("DSM-name", func(t *testing.T) {
		d := b
		d.Target = TargetDSM
		d.Port = l.rp
		wrong := l.leaf(NewKey(), []string{"attacker.example.com"}, true)
		if got := v.Check(ctx, d, []*x509.Certificate{wrong.Leaf}); got != ReasonName {
			t.Fatal(got)
		}
		good := l.leaf(NewKey(), approvedNames, true)
		if got := v.Check(ctx, d, []*x509.Certificate{good.Leaf}); got != "" {
			t.Fatal(got)
		}
	})
	t.Run("not-ready-does-not-propose", func(t *testing.T) {
		l.app.SetCert(l.leaf(l.key, approvedNames, true))
		l.app.PingStatus.Store(502)
		if _, err := v.Propose(ctx, TargetApp, l.ap, b.Package, b.Authority); ReasonOf(err) != ReasonPingMissing {
			t.Fatal(err)
		}
		if _, err := v.PrepareReenable(ctx, b); ReasonOf(err) != ReasonPingMissing {
			t.Fatal(err)
		}
		l.app.PingStatus.Store(0)
	})
	t.Run("missing-reference-refuses-proposal", func(t *testing.T) {
		opts := l.opts
		opts.ReferencePort = func() (int, error) { return 0, errors.New("unavailable") }
		if _, err := NewVerifier(opts).Propose(ctx, TargetApp, l.ap, b.Package, b.Authority); ReasonOf(err) != ReasonRefMissing {
			t.Fatal(err)
		}
	})
}

func TestReferenceCacheAndRenewal(t *testing.T) {
	l := newPinLab(t)
	v := NewVerifier(l.opts)
	ctx := context.Background()
	b := l.binding()
	old := l.leaf(l.key, approvedNames, true)
	if r := v.Check(ctx, b, []*x509.Certificate{old.Leaf}); r != "" {
		t.Fatal(r)
	}
	if l.ref.Hellos.Load() != 1 {
		t.Fatal("initial reference probe")
	}
	next := l.leaf(NewKey(), approvedNames, true)
	// App updates first: cached mismatch triggers one refetch, still refuses.
	if r := v.Check(ctx, b, []*x509.Certificate{next.Leaf}); r != ReasonKey {
		t.Fatal(r)
	}
	if l.ref.Hellos.Load() != 2 {
		t.Fatal("missing mismatch refetch")
	}
	l.ref.SetCert(next)
	if r := v.Check(ctx, b, []*x509.Certificate{next.Leaf}); r != "" {
		t.Fatal(r)
	}
	l.clk.Add(31 * time.Second)
	if r := v.Check(ctx, b, []*x509.Certificate{next.Leaf}); r != "" {
		t.Fatal(r)
	}
	// Cached reference for one N cannot validate a second binding's different N.
	other := b
	other.Names = []string{"other.example.com"}
	otherCert := l.leaf(next.PrivateKey.(*ecdsa.PrivateKey), other.Names, true)
	if r := v.Check(ctx, other, []*x509.Certificate{otherCert.Leaf}); r != ReasonRefInvalid {
		t.Fatal(r)
	}
	l.ref.Close()
	l.clk.Add(31 * time.Second)
	if r := v.Check(ctx, b, []*x509.Certificate{next.Leaf}); r != ReasonRefMissing {
		t.Fatal(r)
	}
}

func TestReadOnlyRuntimeVerificationUsesPinThenSameConnectionPing(t *testing.T) {
	l := newPinLab(t)
	v := NewVerifier(l.opts)
	b := l.binding()
	sc := Scope{Socket: "scope", Authority: b.Authority, Binding: Binding{Port: b.Port, Summary: strings.Repeat("a", 64), Pin: Pin{Mode: "W", Names: slices.Clone(b.Names)}}}
	mapped := PinBindingFor(Target{Package: b.Package, Kind: Private, Scope: sc})
	if mapped.Scope != Private || mapped.Socket != "scope" || mapped.Port != b.Port {
		t.Fatal(mapped)
	}
	sc.Binding.Pin.Names[0] = "changed.example.com"
	if mapped.Names[0] != approvedNames[0] {
		t.Fatal("binding retained mutable state names")
	}
	if err := v.VerifyTarget(context.Background(), mapped); err != nil {
		t.Fatal(err)
	}
	for _, rec := range l.app.Records() {
		if rec.UserRequest {
			t.Fatal("read-only validation sent user data")
		}
	}
	l.app.Reset()
	l.app.SetCert(l.leaf(NewKey(), approvedNames, true))
	if err := v.VerifyTarget(context.Background(), mapped); ReasonOf(err) != ReasonKey {
		t.Fatal(err)
	}
	if l.app.AppBytes() != 0 {
		t.Fatal("ping sent before pin verification")
	}
	l.app.SetCert(l.leaf(l.key, approvedNames, true))
	l.app.PingStatus.Store(502)
	if err := v.VerifyTarget(context.Background(), mapped); ReasonOf(err) != ReasonPingMissing {
		t.Fatal(err)
	}
}

// An idle App/provider must not parse the full product CA bundle. Measure the
// constructor's allocations rather than relying on one implementation field.
func TestIdleVerifierDoesNotParseProductRoots(t *testing.T) {
	var v *Verifier
	allocations := testing.AllocsPerRun(3, func() { v = NewVerifier(Options{}) })
	if allocations > 20 {
		t.Fatalf("idle verifier allocated %.0f objects before any TLS verification", allocations)
	}
	if v.webPKI(nil) != ReasonChain {
		t.Fatal("empty chains must still fail closed")
	}
}

func TestLazyVerifierRootsStayPrivateOnConcurrentFirstUse(t *testing.T) {
	ca, err := NewLabCA("Private CA")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	leaf := Leaf(ca, NewKey(), approvedNames, now.Add(-time.Hour), now.Add(time.Hour))
	cert, err := x509.ParseCertificate(leaf.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)
	if reason := NewVerifier(Options{Roots: roots}).webPKI([]*x509.Certificate{cert}); reason != "" {
		t.Fatal("explicit private roots must still work", reason)
	}
	v := NewVerifier(Options{})
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() {
			if reason := v.webPKI([]*x509.Certificate{cert}); reason != ReasonChain {
				t.Errorf("default roots trusted a private CA: %s", reason)
			}
		})
	}
	wg.Wait()
}
