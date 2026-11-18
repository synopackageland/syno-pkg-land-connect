package cloudflare

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"

	"dsmapp/internal/connector"
)

type Process = connector.Process

// Command is one run of cloudflared. Home is a private HOME, so cloudflared
// reads and writes its sign-in files only there. Output is never stored or
// logged: it can name accounts and tunnels.
type Command struct {
	Path   string
	Args   []string
	Home   string
	Output io.Writer
}

// Launcher starts cloudflared: Start for a tunnel or sign-in that keeps
// running, Run for a short command whose exit status matters.
type Launcher interface {
	Start(context.Context, Command) (Process, error)
	Run(context.Context, Command) error
}

type ExecLauncher struct{}

func command(ctx context.Context, c Command) *exec.Cmd {
	cmd := exec.CommandContext(ctx, c.Path, c.Args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + c.Home}
	out := c.Output
	if out == nil {
		out = io.Discard
	}
	cmd.Stdout, cmd.Stderr = out, out
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

func (ExecLauncher) Start(_ context.Context, c Command) (Process, error) {
	return connector.StartOwned(command(context.Background(), c))
}
func (ExecLauncher) Run(ctx context.Context, c Command) error { return command(ctx, c).Run() }

// lines keeps only the first value find picks out of whole output lines.
type lines struct {
	find  func(string) string
	mu    sync.Mutex
	part  []byte
	long  bool
	value string
}

const maxLine = 4096

func (l *lines) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(b)
	for len(b) > 0 {
		i := bytes.IndexByte(b, '\n')
		chunk := b
		if i >= 0 {
			chunk = b[:i]
		}
		if len(l.part)+len(chunk) > maxLine {
			l.long, l.part = true, l.part[:0]
		} else if !l.long {
			l.part = append(l.part, chunk...)
		}
		if i < 0 {
			break
		}
		if !l.long && l.value == "" && l.find != nil {
			l.value = l.find(string(l.part))
		}
		l.long, l.part, b = false, l.part[:0], b[i+1:]
	}
	return n, nil
}

func (l *lines) found() string { l.mu.Lock(); defer l.mu.Unlock(); return l.value }

// Retain only a known refusal code, never cloudflared's raw account/tunnel output.
func missingTunnelFromOutput(line string) string {
	if strings.Contains(line, "Unauthorized: Tunnel not found") {
		return "cloudflare_tunnel_missing"
	}
	return ""
}
