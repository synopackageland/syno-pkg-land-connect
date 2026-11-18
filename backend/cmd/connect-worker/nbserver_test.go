package main

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"dsmapp/internal/binaries"
	"dsmapp/internal/connector"
)

type joinController struct {
	mu   sync.Mutex
	reqs []connector.Request
	err  error
}

func (c *joinController) Status() connector.Status { return connector.Status{} }
func (c *joinController) Connect(_ context.Context, r connector.Request) (connector.Status, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.reqs = append(c.reqs, r)
	return connector.Status{State: "connecting"}, c.err
}
func (c *joinController) Disconnect() (connector.Status, error) { return connector.Status{}, nil }
func (c *joinController) Stop() error                           { return nil }

func TestJoinSwitchesNetBirdToTheServerWithTheOneOffKey(t *testing.T) {
	serverPoll = time.Millisecond
	c := &joinController{}
	polls := 0
	status := func(id string) connector.Status {
		if id != "netbird" {
			t.Fatal(id)
		}
		polls++
		if polls < 3 {
			return connector.Status{State: "connecting", ManagementURL: "https://netbird.example.com"}
		}
		return connector.Status{State: "connected", ManagementURL: "https://netbird.example.com"}
	}
	if e := joinServer(context.Background(), c, status, "https://netbird.example.com", "KEY"); e != nil {
		t.Fatal(e)
	}
	r := c.reqs[0]
	if len(c.reqs) != 1 || r.Provider != "netbird" || r.ManagementURL != "https://netbird.example.com" || r.Token != "KEY" || !r.ConfirmManagementPolicy || !r.Replace || len(r.LoginOrigins) != 0 {
		t.Fatalf("%+v", c.reqs)
	}
	failed := func(string) connector.Status { return connector.Status{State: "failed", Code: "netbird_login_failed"} }
	if e := joinServer(context.Background(), c, failed, "https://netbird.example.com", "KEY"); connector.ErrorCode(e) != "netbird_login_failed" {
		t.Fatal(e)
	}
	// Connected to another server is not joined.
	other := func(string) connector.Status {
		return connector.Status{State: "connected", ManagementURL: "https://api.netbird.io:443"}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if e := joinServer(ctx, c, other, "https://netbird.example.com", "KEY"); e == nil {
		t.Fatal("joined another server")
	}
	c.err = connector.Coded("binary_required", "x")
	if e := joinServer(context.Background(), c, status, "https://netbird.example.com", "KEY"); connector.ErrorCode(e) != "binary_required" {
		t.Fatal(e)
	}
}

type jobs struct {
	calls  []string
	status []binaries.Status
}

func (j *jobs) Call(_ context.Context, action, product string) (binaries.Status, error) {
	j.calls = append(j.calls, action+" "+product)
	if action == "update" {
		return binaries.Status{Busy: true}, nil
	}
	s := j.status[0]
	if len(j.status) > 1 {
		j.status = j.status[1:]
	}
	return s, nil
}

// versions reports a verified binary from the given call on; 0 never.
type versions struct{ from, calls int }

func (v *versions) Version(context.Context, string) (string, error) {
	v.calls++
	if v.from > 0 && v.calls >= v.from {
		return "0.81.5", nil
	}
	return "", errors.New("verified binary unavailable")
}

func TestInstallBinaryUsesTheBinaryJobOnlyWhenMissing(t *testing.T) {
	serverPoll = time.Millisecond
	j := &jobs{}
	if e := installBinary(context.Background(), j, &versions{from: 1}, "netbird-server"); e != nil || len(j.calls) != 0 {
		t.Fatal(e, j.calls)
	}
	j = &jobs{status: []binaries.Status{{Busy: true}, {Busy: true}, {}}}
	if e := installBinary(context.Background(), j, &versions{from: 2}, "netbird-server"); e != nil || len(j.calls) != 4 || j.calls[0] != "update netbird-server" {
		t.Fatal(e, j.calls)
	}
	j = &jobs{status: []binaries.Status{{Error: "checksum mismatch"}}}
	if e := installBinary(context.Background(), j, &versions{}, "netbird-server"); e == nil || e.Error() != "checksum mismatch" {
		t.Fatal(e)
	}
}

type fakeDisconnecter struct{ calls []string }

func (f *fakeDisconnecter) DisconnectProvider(id string) (connector.Status, error) {
	f.calls = append(f.calls, id)
	return connector.Status{}, nil
}

func TestLeaveServerDisconnectsOnlyNetBirdOnThatServer(t *testing.T) {
	for _, c := range []struct {
		s    connector.Status
		want int
	}{
		{connector.Status{State: "connected", ManagementURL: "https://netbird.example.com"}, 1},
		{connector.Status{State: "failed", ManagementURL: "https://netbird.example.com"}, 1},
		{connector.Status{State: "connected", ManagementURL: "https://api.netbird.io"}, 0},
		{connector.Status{State: "disconnected", ManagementURL: "https://netbird.example.com"}, 0},
	} {
		f := &fakeDisconnecter{}
		if e := leaveServer(f, func(string) connector.Status { return c.s }, "https://netbird.example.com"); e != nil || len(f.calls) != c.want || c.want == 1 && f.calls[0] != "netbird" {
			t.Fatal(c.s, f.calls, e)
		}
	}
}
