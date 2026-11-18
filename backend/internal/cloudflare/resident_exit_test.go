package cloudflare

import (
	"context"
	"github.com/synopackageland/syno-pkg-land-sdk/supervision"
	"testing"
	"time"
)

func TestEnabledCleanExitBacksOffAndRestarts(t *testing.T) {
	for _, mode := range []string{"quick", "domain"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			f.tunnel.cfg.RestartDelay = 40 * time.Millisecond
			if mode == "domain" {
				f.ownDomain()
			} else {
				f.metrics.hostname = "a.trycloudflare.com"
				if err := f.tunnel.Quick(context.Background()); err != nil {
					t.Fatal(err)
				}
				f.waitState("connected")
			}
			f.launcher.mu.Lock()
			p := f.launcher.procs[len(f.launcher.procs)-1]
			before := len(f.launcher.started)
			f.launcher.mu.Unlock()
			p.exit()
			deadline := time.Now().Add(3 * time.Second)
			for {
				s := f.tunnel.Status()
				if s.Supervision != nil && s.Supervision.State == supervision.Backoff {
					x := s.Supervision
					if x.Attempts != 1 || x.LastExit == nil || x.LastExit.Code != 0 || x.NextRetry.IsZero() || len(f.launcher.starts()) != before {
						t.Fatal(x)
					}
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("clean exit did not enter backoff", s)
				}
				time.Sleep(time.Millisecond)
			}
			for len(f.launcher.starts()) == before && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			if len(f.launcher.starts()) != before+1 {
				t.Fatal("no successor")
			}
			f.waitState("connected")
			if err := f.tunnel.Disable(); err != nil {
				t.Fatal(err)
			}
			time.Sleep(100 * time.Millisecond)
			if len(f.launcher.starts()) != before+1 {
				t.Fatal("disabled provider restarted")
			}
		})
	}
}
