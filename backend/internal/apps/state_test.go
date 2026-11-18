package apps

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

func fixtureScope(kind string) *Scope {
	s := &Scope{State: Active, Binding: Binding{Port: 28443, Summary: strings.Repeat("a", 64), Pin: Pin{Mode: "S", SPKI: strings.Repeat("b", 64)}}, Socket: "a1", Authority: "app.example.com"}
	if kind == Private {
		s.Socket = "a2"
		s.Authority = "nas.example.ts.net:28443"
	} else {
		s.Origin = "https://app.example.com"
	}
	return s
}

func approve(s *State, kind string) error {
	rev, err := s.Revision()
	if err != nil {
		return err
	}
	a := s.Apps["ExampleApp"]
	a.Rev = rev
	a.SetScope(kind, fixtureScope(kind))
	s.Apps["ExampleApp"] = a
	return nil
}

func TestStateRevisionSurvivesRevokeAndRestart(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if err := s.Update(context.Background(), func(p *State) error { return approve(p, Public) }); err != nil {
		t.Fatal(err)
	}
	p, err := s.Load()
	if err != nil {
		t.Fatal(err)
	}
	first := p.Apps["ExampleApp"].Rev
	if err = s.Update(context.Background(), func(p *State) error {
		if _, e := p.Revision(); e != nil {
			return e
		}
		delete(p.Apps, "ExampleApp")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	s = Store{Dir: s.Dir}
	if err = s.Update(context.Background(), func(p *State) error { return approve(p, Public) }); err != nil {
		t.Fatal(err)
	}
	p, err = s.Load()
	if err != nil || p.Apps["ExampleApp"].Rev <= first {
		t.Fatalf("revision reset: %+v %v", p, err)
	}
	st, _ := os.Stat(filepath.Join(s.Dir, "apps-state.json"))
	if st.Mode().Perm() != 0600 {
		t.Fatal(st.Mode())
	}
}

func TestStateCorruptionAndUnsafeFilesFailClosed(t *testing.T) {
	for _, test := range []string{"invalid-json", "future-schema", "unsafe-mode", "symlink", "trailing", "invalid-binding"} {
		t.Run(test, func(t *testing.T) {
			s := Store{Dir: t.TempDir()}
			p := EmptyState()
			_ = approve(&p, Public)
			raw, _ := json.Marshal(p)
			path := filepath.Join(s.Dir, "apps-state.json")
			switch test {
			case "invalid-json":
				raw = []byte("{")
			case "future-schema":
				p.Schema = 2
				raw, _ = json.Marshal(p)
			case "trailing":
				raw = append(raw, []byte("{}")...)
			case "invalid-binding":
				p.Apps["ExampleApp"].Public.Authority = "localhost"
				raw, _ = json.Marshal(p)
			}
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			if test == "unsafe-mode" {
				_ = os.Chmod(path, 0644)
			}
			if test == "symlink" {
				_ = os.Rename(path, path+".real")
				_ = os.Symlink(path+".real", path)
			}
			got, err := s.Load()
			if !errors.Is(err, ErrCorrupt) || len(got.Apps) != 0 {
				t.Fatalf("not fail closed: %+v %v", got, err)
			}
			if err = s.Update(context.Background(), func(p *State) error { return approve(p, Private) }); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("overwrote corruption: %v", err)
			}
		})
	}
}

func TestConcurrentStoreUpdatesKeepGlobalRevisions(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if e := s.Update(context.Background(), func(p *State) error { return approve(p, Public) }); e != nil {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	p, e := s.Load()
	if e != nil || p.NextRev != 21 || p.Apps["ExampleApp"].Rev != 20 {
		t.Fatalf("lost update: %+v %v", p, e)
	}
}

func TestAtomicWriteKilledBetweenDurabilitySteps(t *testing.T) {
	if dir := os.Getenv("APPS_CRASH_DIR"); dir != "" {
		p := EmptyState()
		_ = approve(&p, Public)
		raw, _ := json.Marshal(p)
		err := atomicWrite(filepath.Join(dir, "apps-state.json"), raw, 0600, func(stage string) error {
			if stage == os.Getenv("APPS_CRASH_STAGE") {
				_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)
			}
			return nil
		})
		t.Fatal("child survived", err)
	}
	for _, stage := range []string{"temporary", "file-sync", "rename", "directory-sync"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			raw, _ := json.Marshal(EmptyState())
			if e := os.WriteFile(filepath.Join(dir, "apps-state.json"), raw, 0600); e != nil {
				t.Fatal(e)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestAtomicWriteKilledBetweenDurabilitySteps$")
			cmd.Env = append(os.Environ(), "APPS_CRASH_DIR="+dir, "APPS_CRASH_STAGE="+stage)
			if e := cmd.Run(); e == nil {
				t.Fatal("expected SIGKILL")
			}
			p, e := (Store{Dir: dir}).Load()
			if e != nil {
				t.Fatal(e)
			}
			if want := stage == "rename" || stage == "directory-sync"; (len(p.Apps) > 0) != want {
				t.Fatalf("partial write visible at %s: %+v", stage, p)
			}
		})
	}
}

func TestDirectorySyncFailureIsNotSuccess(t *testing.T) {
	want := errors.New("directory fsync failed")
	e := atomicWrite(filepath.Join(t.TempDir(), "apps-state.json"), []byte(`{}`), 0600, func(stage string) error {
		if stage == "rename" {
			return want
		}
		return nil
	})
	if !errors.Is(e, want) {
		t.Fatalf("reported durable success: %v", e)
	}
}

func TestProjectionOnlyContainsPublicContractAndIsRebuilt(t *testing.T) {
	p := EmptyState()
	_ = approve(&p, Public)
	_ = approve(&p, Private)
	p.Apps["ExampleApp"].Public.State = Paused
	path := filepath.Join(t.TempDir(), "spl", "external.json")
	for range 2 {
		if e := WriteProjection(path, p); e != nil {
			t.Fatal(e)
		}
		raw, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		for _, secret := range []string{"nas.example.ts.net", "spki", "binding", "socket", "paused", "cleanup"} {
			if strings.Contains(string(raw), secret) {
				t.Fatalf("projection leaked %s", secret)
			}
		}
		var v Projection
		if e = json.Unmarshal(raw, &v); e != nil || v.Apps["ExampleApp"].Rev != 2 || v.Apps["ExampleApp"].Public.Origin != "https://app.example.com" {
			t.Fatalf("bad projection: %s %v", raw, e)
		}
		st, _ := os.Stat(path)
		if st.Mode().Perm() != 0644 {
			t.Fatal(st.Mode())
		}
		_ = os.Remove(path)
	}
}
