package appaccess

import (
	"context"
	"dsmapp/internal/apps"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The App safety checks have work only while an App URL or a DSM approval is
// recorded, or the records are unreadable. Otherwise they keep no timer.
func TestAppAccessIsInUseOnlyWhileSomethingIsRecorded(t *testing.T) {
	dir := t.TempDir()
	e := New(context.Background(), Config{Store: apps.Store{Dir: dir}})
	defer e.Close()
	if e.InUse() {
		t.Fatal("no records, yet App access reads as in use")
	}
	hex := strings.Repeat("a", 64)
	open := func(s *apps.State) error {
		s.Apps["ExampleApp"] = apps.AppState{Rev: s.NextRev, Private: &apps.Scope{State: apps.Active, Binding: apps.Binding{Port: 8443, Summary: hex, Pin: apps.Pin{Mode: "S", SPKI: hex}}, Socket: "a1", Authority: "nas.example.ts.net:8443"}}
		s.NextRev++
		return nil
	}
	if err := e.cfg.Store.Update(context.Background(), open); err != nil {
		t.Fatal(err)
	}
	if !e.InUse() {
		t.Fatal("an open App URL reads as unused")
	}
	if err := e.cfg.Store.Update(context.Background(), func(s *apps.State) error { delete(s.Apps, "ExampleApp"); return nil }); err != nil {
		t.Fatal(err)
	}
	if e.InUse() {
		t.Fatal("a closed App URL still reads as in use")
	}
	if err := os.WriteFile(filepath.Join(dir, "apps-state.json"), []byte("{"), 0600); err != nil {
		t.Fatal(err)
	}
	if !e.InUse() {
		t.Fatal("unreadable records must still be checked and reported")
	}
}
