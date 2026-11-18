package connector

import (
	"context"
	"github.com/synopackageland/syno-pkg-land-sdk/supervision"
	"os/exec"
	"time"
)

// StartOwned delegates process-group ownership and reap ordering to the SDK.
// Short commands and long-lived daemons share cleanup, but only Restorer or
// the cloudflared watch own restart policy; this function never retries.
func StartOwned(cmd *exec.Cmd) (Process, error) {
	release, err := supervision.AcquireStart(context.Background())
	if err != nil {
		return nil, err
	}
	p, err := supervision.Start(cmd)
	if err != nil {
		release()
		if supervision.PermanentStart(err) {
			return nil, ErrBinaryRequired
		}
		return nil, err
	}
	go func() {
		timer := time.NewTimer(time.Second)
		defer timer.Stop()
		defer release()
		select {
		case <-p.Done():
		case <-timer.C:
		}
	}()
	return &child{p}, nil
}

type child struct{ *supervision.Process }

func (p *child) Stop() error { return p.Process.Stop(3 * time.Second) }
