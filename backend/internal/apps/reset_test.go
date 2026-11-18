package apps

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResetKeepsTheBrokenRecordsAndStartsClosed(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	broken := []byte(`{"schema":1,"next_rev":42,"apps":{"SynoPkgX":`)
	if e := os.WriteFile(filepath.Join(s.Dir, "apps-state.json"), broken, 0600); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Load(); !errors.Is(e, ErrCorrupt) {
		t.Fatal(e)
	}
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	out, e := s.Reset(context.Background(), now)
	if e != nil || out.Backup != "apps-state.corrupt-20261024T120000Z.json" {
		t.Fatal(out, e)
	}
	kept, e := os.ReadFile(filepath.Join(s.Dir, out.Backup))
	if e != nil || string(kept) != string(broken) {
		t.Fatal("the broken records must be kept as they were", e)
	}
	state, e := s.Load()
	if e != nil || len(state.Apps) != 0 {
		t.Fatal("a reset starts with every App closed", state, e)
	}
	if state.NextRev < uint64(now.Unix())*1000 || state.NextRev >= 1<<53 {
		t.Fatal("revisions must continue past any the old records issued", state.NextRev)
	}
}

func TestReadableRecordsAreNeverReset(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if e := s.Update(context.Background(), func(*State) error { return nil }); e != nil {
		t.Fatal(e)
	}
	if _, e := s.Reset(context.Background(), time.Now()); !errors.Is(e, ErrInvalid) {
		t.Fatal("a readable state must not be reset", e)
	}
}
