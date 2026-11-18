//go:build linux

package connector

import (
	"syscall"
)

func childAttributes() *syscall.SysProcAttr { return &syscall.SysProcAttr{Pdeathsig: syscall.SIGTERM} }
