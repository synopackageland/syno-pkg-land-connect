package supervise

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func TestRestoreStopsAfterBoundedFailuresAndOnlyExplicitBeginRearms(t *testing.T) {
	var mu sync.Mutex
	d := &fakeDaemon{openErr: errors.New("temporary")}
	r := restorer(&mu)
	mu.Lock()
	_ = r.Begin(context.Background(), d)
	mu.Unlock()
	waitDone(t, &mu, r)
	opens, _, _ := d.counts()
	if opens != 8 {
		t.Fatalf("unbounded starts: %d", opens)
	}
	mu.Lock()
	s := r.Snapshot()
	mu.Unlock()
	if s.State != "failed" || !s.NextRetry.IsZero() {
		t.Fatal(s)
	}
	time.Sleep(20 * time.Millisecond)
	if n, _, _ := d.counts(); n != opens {
		t.Fatal("terminal timer restarted child")
	}
	d.mu.Lock()
	d.openErr = nil
	d.mu.Unlock()
	mu.Lock()
	_ = r.Begin(context.Background(), d)
	mu.Unlock()
	waitDone(t, &mu, r)
	if n, _, _ := d.counts(); n != opens+1 {
		t.Fatal("explicit retry did not rearm")
	}
}
