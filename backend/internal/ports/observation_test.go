package ports

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestDaemonObservationOnlyMatchesFreshCurrentDesiredPlan(t *testing.T) {
	ctx := context.Background()
	s := Store{Dir: t.TempDir()}
	d := goodDetector()
	v := mustList(t, s, d)
	v.MappingState = "cleanup-required"
	if e := s.RecordObservation(ctx, v); e != nil {
		t.Fatal(e)
	}
	if mustList(t, s, d).MappingState != "cleanup-required" {
		t.Fatal("daemon failure not visible")
	}
	s.SetDSM(ctx, d, DSMHTTP, true, true)
	if mustList(t, s, d).MappingState != "pending-provider" {
		t.Fatal("old observation was reused after mutation")
	}
	v = mustList(t, s, d)
	v.MappingState = "applied"
	if e := s.RecordObservation(ctx, v); e != nil {
		t.Fatal(e)
	}
	if mustList(t, s, d).MappingState != "applied" {
		t.Fatal("matching observation hidden")
	}
	moved := detectorFunc(func(context.Context) (DSMPorts, error) { return DSMPorts{6000, 6001}, nil })
	if mustList(t, s, moved).MappingState != "pending-provider" {
		t.Fatal("old port marked applied")
	}
	path, err := s.observationPath(false)
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(path, []byte(`{"schema":1,"checkedAt":"2000-01-01T00:00:00Z","state":"applied","revision":1,"plan":{"mappings":[]}}`), 0600)
	if mustList(t, s, d).MappingState != "needs-attention" {
		t.Fatal("stale observation accepted")
	}
	os.Remove(path)
	os.Symlink("/dev/null", path)
	if mustList(t, s, d).MappingState != "needs-attention" {
		t.Fatal("observation symlink accepted")
	}
}

func TestObservationWriterCapsBytesAndKeepsLatest(t *testing.T) {
	ctx := context.Background()
	s := Store{Dir: t.TempDir()}
	v := mustList(t, s, goodDetector())
	v.MappingState = "applied"
	if err := s.RecordObservation(ctx, v); err != nil {
		t.Fatal(err)
	}
	path, err := s.observationPath(false)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	v.Plan.Mappings = []Mapping{{ID: strings.Repeat("x", maxObservationBytes)}}
	if err := s.RecordObservation(ctx, v); err != ErrSettings {
		t.Fatal("oversized observation accepted", err)
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatal("failed update replaced latest state")
	}
	if err := s.ClearObservation(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("stop retained observation", err)
	}
}

// With no connection on, the monitor stops and clears what it published, so
// the page reads "waiting for a connection" rather than a stale warning.
func TestAClearedObservationReadsAsPendingProvider(t *testing.T) {
	ctx := context.Background()
	s := Store{Dir: t.TempDir()}
	d := goodDetector()
	if e := s.ClearObservation(); e != nil {
		t.Fatal("nothing to clear is not an error:", e)
	}
	v := mustList(t, s, d)
	v.MappingState = "needs-attention"
	if e := s.RecordObservation(ctx, v); e != nil {
		t.Fatal(e)
	}
	if e := s.ClearObservation(); e != nil {
		t.Fatal(e)
	}
	if got := mustList(t, s, d).MappingState; got != "pending-provider" {
		t.Fatal("cleared observation still shown:", got)
	}
}
