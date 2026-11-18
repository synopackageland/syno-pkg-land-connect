package ports

import (
	"context"
	"os"
	"testing"
)

func TestObservationNeverWritesPersistentDataDirectory(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	v := mustList(t, s, goodDetector())
	v.MappingState = "applied"
	for i := 0; i < 3; i++ {
		if err := s.RecordObservation(context.Background(), v); err != nil {
			t.Fatal(err)
		}
	}
	files, err := os.ReadDir(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("observation wrote persistent data: %v", files)
	}
	if got := mustList(t, s, goodDetector()).MappingState; got != "applied" {
		t.Fatal(got)
	}
}
