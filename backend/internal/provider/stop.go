package provider

import (
	"dsmapp/internal/connector"
	"fmt"
	"time"
)

// stopAttempts bounds retries of a failed child stop: on a loaded NAS a killed
// child can be reaped after the stop gave up, and the next attempt sees it gone.
const stopAttempts = 2

func retryStop(id string, stop func() error) (err error) {
	for i := 0; i < stopAttempts; i++ {
		if i > 0 {
			time.Sleep(500 * time.Millisecond)
		}
		if err = stop(); err == nil {
			return nil
		}
		connector.Logf("%s: stop attempt %d failed: %v", id, i+1, err)
	}
	return err
}

// reason names why a start failed, for the daemon log, and still matches
// ErrUnavailable. It carries fixed package text only.
func reason(text string) error { return fmt.Errorf("%w: %s", ErrUnavailable, text) }
