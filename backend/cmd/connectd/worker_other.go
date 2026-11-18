//go:build !linux

package main

import "syscall"

func workerAttributes() *syscall.SysProcAttr { return &syscall.SysProcAttr{} }
