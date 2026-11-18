package main

import (
	"context"
	"dsmapp/internal/daemon"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/synopackageland/syno-pkg-land-sdk/supervision"
	"log"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

const workerAction = "work"
const stoppedMarker = daemon.StoppedMarker

var restartPolicy = supervision.Defaults()

// The worker's only restart owner. Policy and group cleanup are SDK contracts;
// this adapter owns the existing lock/pipe and visible stopped marker.
func superviseWorker(cfg supervision.Config, dir string, worker func() *exec.Cmd) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()
	policy := supervision.New(cfg, nil)
	for {
		release, err := supervision.AcquireStart(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		policy.Started(time.Now())
		proc, err := supervision.Start(worker())
		if err == nil {
			timer := time.NewTimer(min(time.Second, cfg.Normalize().Initial))
			select {
			case <-ctx.Done():
				timer.Stop()
				release()
				return proc.Stop(3 * time.Second)
			case <-proc.Done():
				timer.Stop()
			case <-timer.C:
			}
			release()
			select {
			case <-ctx.Done():
				return proc.Stop(3 * time.Second)
			case <-proc.Done():
				err = proc.Wait()
			}
		} else {
			release()
		}
		if ctx.Err() != nil {
			return nil
		}
		snapshot := policy.Exited(time.Now(), supervision.ExitOf(err))
		snapshot.Log = "daemon.log"
		log.Printf("worker state=%s attempts=%d next=%s reason=%s", snapshot.State, snapshot.Attempts, snapshot.NextRetry.UTC().Format(time.RFC3339), snapshot.Reason)
		if snapshot.State != supervision.Backoff {
			marker, _ := json.Marshal(snapshot)
			if err := daemon.WritePrivate(filepath.Join(dir, stoppedMarker), marker); err != nil {
				return err
			}
			return fmt.Errorf("worker failed: %s", snapshot.Reason)
		}
		if err := supervision.Wait(ctx, time.Until(snapshot.NextRetry)); errors.Is(err, context.Canceled) {
			return nil
		}
	}
}
