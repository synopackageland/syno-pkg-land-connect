package helper

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestParentDeathSetupChecksBeforeAndAfterPrctl(t *testing.T) {
	for _, tc := range []struct {
		before, after int
		setErr        error
		ok            bool
	}{
		{42, 42, nil, true}, {1, 1, nil, false}, {42, 1, nil, false}, {42, 42, syscall.EPERM, false},
	} {
		calls, sets := 0, 0
		err := armParentDeath(42, func() error { sets++; return tc.setErr }, func() int {
			calls++
			if calls == 1 {
				return tc.before
			}
			return tc.after
		})
		if (err == nil) != tc.ok {
			t.Fatalf("%+v: %v", tc, err)
		}
		if tc.before != 42 && sets != 0 {
			t.Fatal("already reparented child armed")
		}
		if tc.ok && calls != 2 {
			t.Fatal("parent race not checked")
		}
	}
}

type childFixture struct {
	reaped  bool
	err     error
	signals []syscall.Signal
	exitOn  syscall.Signal
}

func (c *childFixture) exited() (bool, error) { return c.reaped, c.err }
func (c *childFixture) signal(s syscall.Signal) error {
	if c.reaped {
		panic("signalled a reaped child")
	}
	c.signals = append(c.signals, s)
	if s == c.exitOn {
		c.reaped = true
	}
	return nil
}
func TestOnlyUnreapedOwnedChildCanBeSignalled(t *testing.T) {
	for _, tc := range []struct {
		reaped bool
		err    error
		exit   syscall.Signal
		count  int
		ok     bool
	}{
		{true, nil, 0, 0, true}, {false, syscall.ECHILD, 0, 0, false}, {false, nil, syscall.SIGTERM, 1, true}, {false, nil, syscall.SIGKILL, 2, true}, {false, nil, 0, 2, false},
	} {
		c := &childFixture{reaped: tc.reaped, err: tc.err, exitOn: tc.exit}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		err := stopChild(ctx, c, time.Millisecond)
		cancel()
		if (err == nil) != tc.ok || len(c.signals) != tc.count {
			t.Fatalf("%+v: err=%v signals=%v", tc, err, c.signals)
		}
		if c.reaped {
			n := len(c.signals)
			if stopChild(context.Background(), c, time.Millisecond) != nil || len(c.signals) != n {
				t.Fatal("reaped PID reused")
			}
		}
	}
}
func TestCancelledStopCannotSignal(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := &childFixture{}
	if stopChild(ctx, c, time.Millisecond) == nil || len(c.signals) != 0 {
		t.Fatal("cancelled operation signalled")
	}
}

func TestSupervisorReplyRequiresRealEOFAndStrictBoundedAcknowledgement(t *testing.T) {
	i := fixture(t).Expected
	nonce := strings.Repeat("c", 64)
	good, _ := json.Marshal(Response{Inventory: i, Nonce: nonce, OK: true})
	for _, tc := range []struct {
		data []byte
		ok   bool
	}{
		{good, true}, {append(append([]byte{}, good...), []byte(" {}")...), false},
		{bytes.Replace(good, []byte(nonce), []byte(strings.Repeat("d", 64)), 1), false},
		{append(append([]byte{}, good[:len(good)-1]...), []byte(",\"unknown\":true}")...), false},
		{append(append([]byte{}, good...), bytes.Repeat([]byte(" "), 4097)...), false},
	} {
		if (readSupervisorReply(bytes.NewReader(tc.data), i, nonce) == nil) != tc.ok {
			t.Fatalf("reply accepted=%v expected=%v", !tc.ok, tc.ok)
		}
	}
	if readSupervisorReply(&brokenReplyReader{data: good}, i, nonce) == nil {
		t.Fatal("read error treated as cooperative exit")
	}
}

type brokenReplyReader struct{ data []byte }

func (r *brokenReplyReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, syscall.ECONNRESET
	}
	n := copy(p, r.data)
	r.data = r.data[n:]
	return n, nil
}
