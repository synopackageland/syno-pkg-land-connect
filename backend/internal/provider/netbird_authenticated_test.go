package provider

import (
	"context"
	"io"
	"slices"
	"testing"
	"time"
)

// A registered peer completes login without a URL, then up can wait on Signal.
// The login URL deadline must not kill that already authenticated network start.
type nbAuthenticatedCommands struct {
	nbStreaming
	up            chan struct{}
	loginObserved chan struct{}
	loginRelease  chan struct{}
}

func (c *nbAuthenticatedCommands) Stream(ctx context.Context, _ string, out io.Writer, args ...string) error {
	if slices.Contains(args, "login") {
		if _, err := io.WriteString(out, c.output); err != nil {
			return err
		}
		close(c.loginObserved)
		select {
		case <-c.loginRelease:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	close(c.up)
	<-ctx.Done()
	return ctx.Err()
}

func TestNetBirdAuthenticatedStartDoesNotWaitForLoginURL(t *testing.T) {
	for _, output := range []string{"Login successful\n", "Use this URL to log in:\nhttps://login.netbird.io/activate?user_code=ABCD-EFGH\n"} {
		t.Run(output, func(t *testing.T) { testNetBirdAuthenticatedStart(t, output) })
	}
}

func testNetBirdAuthenticatedStart(t *testing.T, output string) {
	c := &nbAuthenticatedCommands{nbStreaming: nbStreaming{daemon: &nbSafeProcess{}, output: output}, up: make(chan struct{}), loginObserved: make(chan struct{}), loginRelease: make(chan struct{})}
	session, err := (NetBirdExec{Dir: t.TempDir(), Binaries: fakeBinaries{}, Commands: c, URLWindow: 30 * time.Millisecond, LoginWindow: time.Second}).Open(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Stop() })
	if err = session.Login(context.Background(), "https://management.example.test", ""); err != nil {
		t.Fatal(err)
	}
	select {
	case <-c.loginObserved:
	case <-time.After(time.Second):
		t.Fatal("authentication never started")
	}
	close(c.loginRelease)
	select {
	case <-c.up:
	case <-time.After(time.Second):
		t.Fatal("authenticated peer never started networking")
	}
	// Let the old deadline expire while up is still pending.
	time.Sleep(60 * time.Millisecond)
	s := session.(*netbirdExecSession)
	s.mu.Lock()
	code, deadline := s.challenge.Code, s.challenge.Deadline
	loginURL, userCode := s.challenge.LoginURL, s.challenge.UserCode
	s.mu.Unlock()
	if code != "" || !deadline.IsZero() {
		t.Fatalf("authenticated peer still has a login deadline: code=%q deadline=%v", code, deadline)
	}
	if loginURL != "" || userCode != "" {
		t.Fatal("completed authentication kept a stale sign-in challenge")
	}
	select {
	case <-s.done:
		t.Fatal("login URL timeout killed an authenticated network start")
	default:
	}
}
