// Package daemon holds the lightweight files shared by the serving parent and worker.
package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// StoppedMarker records a worker crash loop until the next successful start.
const StoppedMarker = "stopped-after-crashes.json"

// Lock takes the exclusive lock that makes one daemon own the socket.
func Lock(dir string) (*os.File, error) {
	lock, e := os.OpenFile(filepath.Join(dir, "daemon.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return nil, e
	}
	if e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); e != nil {
		lock.Close()
		return nil, e
	}
	return lock, nil
}

// HeldLock accepts only the inherited daemon lock: the same file, still held.
func HeldLock(lock *os.File, dir string) error {
	held, e := lock.Stat()
	if e != nil {
		return e
	}
	path, e := os.Lstat(filepath.Join(dir, "daemon.lock"))
	if e != nil || !os.SameFile(held, path) {
		return errors.New("invalid daemon lock")
	}
	// The lock is shared with the parent's open file, so this cannot block.
	return syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}
