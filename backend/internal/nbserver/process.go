package nbserver

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"dsmapp/internal/connector"
	"github.com/synopackageland/syno-pkg-land-sdk/supervision"
)

// Process is one running server.
type Process interface {
	Stop() error
	Alive() bool
	Pid() int
}

// Command is one start of the official server. Env is its whole environment.
type Command struct {
	Path string
	Args []string
	Env  []string
	Dir  string
}

type Launcher interface {
	Start(context.Context, Command) (Process, error)
}

// ExecLauncher runs the server as the package's own user. Its output is not
// kept: it can name accounts and peers.
type ExecLauncher struct{}

type owned struct {
	connector.Process
	pid int
}

func (o owned) Exit() supervision.Exit {
	if p, ok := o.Process.(interface{ Exit() supervision.Exit }); ok {
		return p.Exit()
	}
	return supervision.Exit{Code: -1, Reason: "daemon_exit"}
}

func (o owned) Pid() int { return o.pid }

func (ExecLauncher) Start(_ context.Context, c Command) (Process, error) {
	cmd := exec.Command(c.Path, c.Args...)
	cmd.Env, cmd.Dir = c.Env, c.Dir
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	cmd.WaitDelay = 10 * time.Second
	p, e := connector.StartOwned(cmd)
	if e != nil {
		return nil, e
	}
	return owned{p, cmd.Process.Pid}, nil
}

// residentBytes reads a process's resident memory; 0 when unknown.
func residentBytes(pid int) int64 {
	f, e := os.Open(fmt.Sprintf("/proc/%d/status", pid))
	if e != nil {
		return 0
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		if rest, ok := strings.CutPrefix(s.Text(), "VmRSS:"); ok {
			fields := strings.Fields(rest)
			if len(fields) == 2 && fields[1] == "kB" {
				n, e := strconv.ParseInt(fields[0], 10, 64)
				if e == nil {
					return n * 1024
				}
			}
		}
	}
	return 0
}

// diskBytes is the size of the files under dir.
func diskBytes(dir string) int64 {
	var n int64
	filepath.WalkDir(dir, func(_ string, d fs.DirEntry, e error) error {
		if e == nil && d.Type().IsRegular() {
			if info, e := d.Info(); e == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}
