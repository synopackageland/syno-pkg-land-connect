package apps

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

type fixtureRuntime struct {
	dir                               string
	invalid, failActivate, failRemove bool
	events                            *[]string
}

func (r *fixtureRuntime) event(s string) {
	if r.events != nil {
		*r.events = append(*r.events, s)
	}
}
func (r *fixtureRuntime) Start(_ context.Context, s State) error {
	r.event("start")
	return os.RemoveAll(filepath.Join(r.dir, "forward"))
}
func (r *fixtureRuntime) Validate(context.Context, Target) error {
	r.event("validate")
	if r.invalid {
		return ErrInvalid
	}
	return nil
}
func (r *fixtureRuntime) PrepareDNS(context.Context, Target) error { r.event("dns"); return nil }
func (r *fixtureRuntime) Activate(_ context.Context, t Target) error {
	r.event("activate")
	if err := os.WriteFile(filepath.Join(r.dir, "route"), []byte(t.Scope.Socket), 0600); err != nil {
		return err
	}
	if r.failActivate {
		return errors.New("provider failed")
	}
	return os.WriteFile(filepath.Join(r.dir, "forward"), []byte(t.Scope.Socket), 0600)
}
func (r *fixtureRuntime) Block(context.Context, Target) error {
	r.event("block")
	e := os.Remove(filepath.Join(r.dir, "forward"))
	if os.IsNotExist(e) {
		return nil
	}
	return e
}
func (r *fixtureRuntime) Remove(context.Context, Target) error {
	r.event("remove")
	if r.failRemove {
		return ErrCleanup
	}
	e := os.Remove(filepath.Join(r.dir, "route"))
	if os.IsNotExist(e) {
		return nil
	}
	return e
}
func (r *fixtureRuntime) Prune(ctx context.Context, s State) error {
	r.event("prune")
	if len(Targets(s)) == 0 {
		return r.Remove(ctx, Target{})
	}
	return nil
}
func fixtureLifecycle(dir string) Lifecycle {
	return Lifecycle{Store: Store{Dir: dir}, ProjectionPath: filepath.Join(dir, "spl", "external.json"), Runtime: &fixtureRuntime{dir: dir}}
}

func TestLifecycleOrdersAndActivationFailure(t *testing.T) {
	l := fixtureLifecycle(t.TempDir())
	var events []string
	l.Runtime.(*fixtureRuntime).events = &events
	l.step = func(s string) { events = append(events, s) }
	target := Target{Package: "ExampleApp", Kind: Public, Scope: *fixtureScope(Public)}
	if e := l.Open(context.Background(), target, false); e != nil {
		t.Fatal(e)
	}
	if want := []string{"validate", "dns", "open-dns", "open-state", "activate", "open-runtime", "open-projection"}; !reflect.DeepEqual(events, want) {
		t.Fatal(events)
	}
	events = nil
	if e := l.Close(context.Background(), "ExampleApp", Public, "metadata-changed"); e != nil {
		t.Fatal(e)
	}
	if want := []string{"close-state", "block", "close-block", "close-projection", "remove", "close-provider"}; !reflect.DeepEqual(events, want) {
		t.Fatal(events)
	}
	l.Runtime.(*fixtureRuntime).failActivate = true
	if e := l.Open(context.Background(), target, true); e == nil {
		t.Fatal("provider failure hidden")
	}
	p, e := l.Store.Load()
	if e != nil {
		t.Fatal(e)
	}
	if s := p.Apps["ExampleApp"].Public; s.State != Paused || s.Reason != "activation-failed" || s.Cleanup {
		t.Fatalf("bad rollback: %+v", s)
	}
	if _, e = os.Stat(filepath.Join(l.Store.Dir, "route")); !os.IsNotExist(e) {
		t.Fatal("partial route retained")
	}
}

func TestCleanupFailureBlocksAndRecoveryPrunes(t *testing.T) {
	l := fixtureLifecycle(t.TempDir())
	if e := l.Open(context.Background(), Target{Package: "ExampleApp", Kind: Public, Scope: *fixtureScope(Public)}, false); e != nil {
		t.Fatal(e)
	}
	l.Runtime.(*fixtureRuntime).failRemove = true
	if e := l.Close(context.Background(), "ExampleApp", Public, ""); !errors.Is(e, ErrCleanup) {
		t.Fatal(e)
	}
	p, e := l.Store.Load()
	if e != nil || len(p.Apps) != 0 {
		t.Fatalf("revocation missing: %+v %v", p, e)
	}
	if _, e = os.Stat(filepath.Join(l.Store.Dir, "forward")); !os.IsNotExist(e) {
		t.Fatal("still forwarding")
	}
	l.Runtime.(*fixtureRuntime).failRemove = false
	if e = l.Recover(context.Background()); e != nil {
		t.Fatal(e)
	}
	if _, e = os.Stat(filepath.Join(l.Store.Dir, "route")); !os.IsNotExist(e) {
		t.Fatal("orphan route retained")
	}
}

func TestRecoverCorruptStateAndInvalidBindingNeverForward(t *testing.T) {
	for _, corrupt := range []bool{true, false} {
		l := fixtureLifecycle(t.TempDir())
		if e := l.Store.Update(context.Background(), func(p *State) error { return approve(p, Public) }); e != nil {
			t.Fatal(e)
		}
		if corrupt {
			_ = os.WriteFile(filepath.Join(l.Store.Dir, "apps-state.json"), []byte("{"), 0600)
		} else {
			l.Runtime.(*fixtureRuntime).invalid = true
		}
		_ = os.WriteFile(filepath.Join(l.Store.Dir, "route"), []byte("old"), 0600)
		e := l.Recover(context.Background())
		if corrupt && !errors.Is(e, ErrCorrupt) {
			t.Fatal(e)
		}
		for _, name := range []string{"forward", "route"} {
			if _, e = os.Stat(filepath.Join(l.Store.Dir, name)); !os.IsNotExist(e) {
				t.Fatal("recovered unapproved route")
			}
		}
	}
}

func TestLifecycleSIGKILLRecovery(t *testing.T) {
	if dir := os.Getenv("APPS_LIFECYCLE_DIR"); dir != "" {
		l := fixtureLifecycle(dir)
		stage := os.Getenv("APPS_LIFECYCLE_STAGE")
		l.step = func(s string) {
			if s == stage {
				_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
			}
		}
		var err error
		switch {
		case strings.HasPrefix(stage, "open-"):
			err = l.Open(context.Background(), Target{Package: "ExampleApp", Kind: Public, Scope: *fixtureScope(Public)}, false)
		case strings.HasPrefix(stage, "close-"):
			err = l.Close(context.Background(), "ExampleApp", Public, "")
		default:
			err = l.Recover(context.Background())
		}
		t.Fatal("child survived", err)
	}
	stages := []string{"open-dns", "open-state", "open-runtime", "open-projection", "close-state", "close-block", "close-projection", "close-provider", "startup-read", "startup-proxy", "startup-routes", "startup-prune", "startup-projection"}
	for _, stage := range stages {
		t.Run(stage, func(t *testing.T) {
			l := fixtureLifecycle(t.TempDir())
			if !strings.HasPrefix(stage, "open-") {
				if e := l.Store.Update(context.Background(), func(p *State) error { return approve(p, Public) }); e != nil {
					t.Fatal(e)
				}
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestLifecycleSIGKILLRecovery$")
			cmd.Env = append(os.Environ(), "APPS_LIFECYCLE_DIR="+l.Store.Dir, "APPS_LIFECYCLE_STAGE="+stage)
			if e := cmd.Run(); e == nil {
				t.Fatal("child not killed")
			}
			if e := l.Recover(context.Background()); e != nil {
				t.Fatal(e)
			}
			p, e := l.Store.Load()
			if e != nil {
				t.Fatal(e)
			}
			scope := p.Apps["ExampleApp"].Public
			want := scope != nil && scope.State == Active
			_, e = os.Stat(filepath.Join(l.Store.Dir, "forward"))
			if (e == nil) != want {
				t.Fatalf("forward iff active violated after %s", stage)
			}
			_, e = os.Stat(filepath.Join(l.Store.Dir, "route"))
			if (e == nil) != want {
				t.Fatalf("route cleanup violated after %s", stage)
			}
		})
	}
}

type callbackRuntime struct {
	fixtureRuntime
	store Store
}

func (r *callbackRuntime) Prune(ctx context.Context, _ State) error {
	return r.store.Update(ctx, func(s *State) error { _, err := s.Revision(); return err })
}
func TestRecoveryProviderCallbackCanReenterStoreAndProjectsFreshState(t *testing.T) {
	dir := t.TempDir()
	l := fixtureLifecycle(dir)
	l.Runtime = &callbackRuntime{fixtureRuntime: fixtureRuntime{dir: dir}, store: l.Store}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := l.Recover(ctx); err != nil {
		t.Fatal("provider callback waited on recovery's approval lock", err)
	}
	state, err := l.Store.Load()
	if err != nil || state.NextRev != 2 {
		t.Fatal("callback did not persist", err)
	}
}
