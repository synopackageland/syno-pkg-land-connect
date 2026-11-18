//go:build linux

package main

import "syscall"

// The worker and its provider children stop when the serving parent dies.
func workerAttributes() *syscall.SysProcAttr { return &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL} }
