package helper

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"syscall"
	"time"
)

// Also runs after exec: the parent can die between fork's prctl and exec.
func armParentDeath(expected int, set func() error, parent func() int) error {
	if expected <= 1 || parent() != expected {
		return errTrust
	}
	if err := set(); err != nil {
		return err
	}
	if parent() != expected {
		return errTrust
	}
	return nil
}

type supervisedChild interface {
	exited() (bool, error)
	signal(syscall.Signal) error
}

// Caller is the sole reaper and signal owner. A zombie retains its PID until
// exited() reaps it; a background Wait goroutine would invalidate this proof.
func stopChild(ctx context.Context, child supervisedChild, grace time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	done, err := child.exited()
	if err != nil || done {
		return err
	}
	if err := child.signal(syscall.SIGTERM); err != nil {
		return err
	}
	escalate := time.Now().Add(grace)
	killed := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		done, err := child.exited()
		if err != nil || done {
			return err
		}
		if !killed && !time.Now().Before(escalate) {
			if err := child.signal(syscall.SIGKILL); err != nil {
				return err
			}
			killed = true
		}
		timer := time.NewTimer(time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// Read to actual connection EOF. A LimitReader's synthetic EOF at the size
// cap can never certify supervisor exit, so reaching that cap is a refusal.
func readSupervisorReply(reader io.Reader, want Inventory, nonce string) error {
	b, err := io.ReadAll(io.LimitReader(reader, 4097))
	if err != nil || len(b) > 4096 {
		return errTrust
	}
	var res Response
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&res) != nil || d.Decode(new(any)) != io.EOF || !res.OK || res.Error != "" || res.Inventory != want || res.Nonce != nonce {
		return errTrust
	}
	return nil
}
