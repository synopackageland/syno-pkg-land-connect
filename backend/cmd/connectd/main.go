// Command connectd keeps the package worker alive without linking App or
// provider code. All control actions replace this process with connect-worker.
package main

import (
	"dsmapp/internal/daemon"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, "SynoPkgConnect: package service unavailable")
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) != 4 {
		return fmt.Errorf("usage: connectd start|serve|stop|status target var")
	}
	bin, e := os.Executable()
	if e != nil {
		return e
	}
	worker := filepath.Join(filepath.Dir(bin), "connect-worker")
	if os.Args[1] != "serve" {
		return syscall.Exec(worker, append([]string{worker}, os.Args[1:]...), os.Environ())
	}
	log.SetPrefix("connectd: ")
	dir := filepath.Join(os.Args[3], "connect")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	if e := os.Chmod(dir, 0700); e != nil {
		return e
	}
	lock, e := daemon.Lock(dir)
	if e != nil {
		return e
	}
	defer lock.Close()
	// Only this process holds the write end: the worker sees EOF when it dies.
	parent, alive, e := os.Pipe()
	if e != nil {
		return e
	}
	defer parent.Close()
	defer alive.Close()
	return superviseWorker(restartPolicy, dir, func() *exec.Cmd {
		cmd := exec.Command(worker, workerAction, os.Args[2], os.Args[3])
		cmd.Env = os.Environ()
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		cmd.ExtraFiles = []*os.File{lock, parent}
		cmd.SysProcAttr = workerAttributes()
		return cmd
	})
}
