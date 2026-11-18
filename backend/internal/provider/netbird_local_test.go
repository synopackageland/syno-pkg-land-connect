package provider

import (
	"context"
	"dsmapp/internal/connector"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestNetBirdLocalCheckDoesNotDialItsOwnOverlay(t *testing.T) {
	for _, valid := range []bool{true, false} {
		t.Run(strconv.FormatBool(valid), func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Path != "/webapi/query.cgi" || r.URL.Query().Get("query") != "SYNO.API.Auth" {
					t.Error("unexpected DSM probe", r.URL)
				}
				if valid {
					w.Write([]byte(`{"success":true,"data":{"SYNO.API.Auth":{"path":"entry.cgi","minVersion":1,"maxVersion":7}}}`))
				} else {
					w.Write([]byte(`<html>Not DSM API discovery</html>`))
				}
			}))
			defer server.Close()
			port := server.Listener.Addr().(*net.TCPAddr).Port
			// The userspace proxy cannot connect to its own overlay address.
			// A closed proxy must not invalidate a reachable local DSM service.
			s := &netbirdExecSession{proxyPort: 1}
			err := s.CheckHTTP(context.Background(), "127.0.0.1", port)
			if (err == nil) != valid || requests != 1 {
				t.Fatalf("local DSM evidence valid=%v: requests=%d error=%v", valid, requests, err)
			}
		})
	}
}

type localNetBirdSession struct {
	nbSession
	host string
	port int
}

func (s *localNetBirdSession) CheckHTTP(_ context.Context, host string, port int) error {
	s.host, s.port = host, port
	if host != "127.0.0.1" {
		return nbError("local_check_failed")
	}
	return nil
}

type localNetBirdDriver struct{ session *localNetBirdSession }

func (d localNetBirdDriver) Open(context.Context) (NetBirdSession, error) { return d.session, nil }

func TestNetBirdProbeChecksDetectedLocalServiceAndNeverClaimsExternalAccess(t *testing.T) {
	s := &localNetBirdSession{nbSession: nbSession{snapshot: NetBirdSnapshot{DaemonStatus: "Connected", ManagementConnected: true, SignalConnected: true, IP: "100.88.0.2/16"}}}
	p := NewNetBird(t.TempDir(), localNetBirdDriver{s})
	p.HTTPPort = func(context.Context) (int, error) { return 5432, nil }
	if _, err := p.Start(context.Background(), nbSettings()); err != nil {
		t.Fatal(err)
	}
	if external, err := p.Probe(context.Background()); err != nil || external || s.host != "127.0.0.1" || s.port != 5432 {
		t.Fatal("expected a local service check only", external, err, s.host, s.port)
	}
	s.snapshot.SignalConnected = false
	if _, err := p.Probe(context.Background()); err != connector.ErrNotConnected {
		t.Fatal("local service must not hide disconnected NetBird", err)
	}
}

func TestNetBirdHealthChecksLocalAPIAndWebEntryWithoutOverlayHairpin(t *testing.T) {
	for _, pageOK := range []bool{true, false} {
		t.Run(strconv.FormatBool(pageOK), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/webapi/query.cgi" {
					w.Write([]byte(`{"success":true,"data":{"SYNO.API.Auth":{"path":"entry.cgi","minVersion":1,"maxVersion":7}}}`))
					return
				}
				if pageOK {
					w.Write([]byte(`<html>DSM</html>`))
				} else {
					http.Redirect(w, r, "http://elsewhere.invalid/", http.StatusFound)
				}
			}))
			defer server.Close()
			commands := &nbCommands{commandFixture: commandFixture{status: []byte(`{"daemonStatus":"Connected","management":{"connected":true},"signal":{"connected":true},"netbirdIp":"100.88.0.2/16"}`)}}
			p := NewNetBird(t.TempDir(), nil)
			p.healthPort = server.Listener.Addr().(*net.TCPAddr).Port
			p.session = &netbirdExecSession{proxyPort: 1, commands: commands, daemon: &nbSafeProcess{}}
			if err := p.CheckHealth(context.Background()); (err == nil) != pageOK {
				t.Fatal("health must verify the host service and reject external redirects", err)
			}
		})
	}
}

type failingHealthFixture struct {
	safeFixture
	err error
}

func (p *failingHealthFixture) CheckHealth(context.Context) error { return p.err }

func TestUnverifiedHealthIsNotReportedAsFailedRecovery(t *testing.T) {
	p := &failingHealthFixture{safeFixture: safeFixture{fixtureRuntime: fixtureRuntime{id: "netbird", events: &[]string{}}}, err: nbError("local_check_failed")}
	m := NewMultiSelection(t.TempDir(), p)
	defer m.Stop()
	if _, err := m.Connect(context.Background(), connector.Request{Provider: "netbird", ConfirmExposure: true}); err != nil {
		t.Fatal(err)
	}
	child := m.children["netbird"]
	for i := 0; i < 3; i++ {
		child.healthTick(context.Background())
	}
	if got := child.Status().Apply; got == nil || got.Phase != "check-failed" || got.Code != "local_check_failed" {
		t.Fatal("no recovery was attempted", got)
	}
	p.err = nil
	child.healthTick(context.Background())
	if got := child.Status().Apply; got != nil {
		t.Fatal("recovered health retained a stale check failure", got)
	}
	// A real earlier recovery failure must retain its separate meaning.
	child.lastGood = selectionState{}
	child.applyStatus = &connector.ApplyStatus{Phase: "recovery-required", Code: "apply_failed"}
	p.err = nbError("local_check_failed")
	for i := 0; i < 3; i++ {
		child.healthTick(context.Background())
	}
	if got := child.Status().Apply; got == nil || got.Phase != "recovery-required" {
		t.Fatal("health check hid a real recovery failure", got)
	}
}
