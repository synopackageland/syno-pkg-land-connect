package cloudflare

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"dsmapp/internal/connector"
)

// ownDomain sets up nas.example.com and leaves it connected.
func (f *fixture) ownDomain() {
	f.t.Helper()
	close(f.signIn(goodCert()))
	f.cloudflaredRuns("")
	if e := f.tunnel.Domain(context.Background(), "nas"); e != nil {
		f.t.Fatal(e)
	}
	f.waitState("connected")
}

func TestUseAnotherNameReplacesSavedHostnameAndIngress(t *testing.T) {
	f := newFixture(t)
	f.ownDomain()
	if err := f.tunnel.Domain(context.Background(), "testnas"); err != nil {
		t.Fatal(err)
	}
	s := f.waitState("connected")
	saved, err := f.tunnel.savedDomain()
	if err != nil || saved.Hostname != "testnas.example.com" || s.Hostname != saved.Hostname {
		t.Fatalf("name was not changed: status %+v, saved %+v, error %v", s, saved, err)
	}
	if !strings.Contains(f.config(), `hostname: "testnas.example.com"`) || strings.Contains(f.config(), `hostname: "nas.example.com"`) {
		t.Fatalf("ingress was not replaced: %s", f.config())
	}
	if got := f.routed(); !slices.Equal(got, []string{"nas.example.com", "testnas.example.com"}) {
		t.Fatalf("wrong DNS route: %v", got)
	}
}

func (f *fixture) logins() int {
	n := 0
	for _, c := range f.launcher.starts() {
		if hasArgs(c, "tunnel", "login") {
			n++
		}
	}
	return n
}

func (f *fixture) routed() []string {
	out := []string{}
	for _, c := range f.launcher.runs() {
		if len(c.Args) > 5 && c.Args[3] == "route" {
			out = append(out, c.Args[len(c.Args)-1])
		}
	}
	return out
}

func (f *fixture) config() string {
	raw, _ := os.ReadFile(filepath.Join(f.dir, "domain", "config.yml"))
	return string(raw)
}

// waitCode waits for the job to report why it did not finish.
func (f *fixture) waitCode() connector.Status {
	f.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if s := f.tunnel.Status(); s.Code != "" || time.Now().After(deadline) {
			return s
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// serving is the cloudflared that runs the own domain, the last one started.
func (f *fixture) serving() *fakeProc {
	f.launcher.mu.Lock()
	defer f.launcher.mu.Unlock()
	for i := len(f.launcher.started) - 1; i >= 0; i-- {
		if slices.Contains(f.launcher.started[i].Args, "run") {
			return f.launcher.procs[i]
		}
	}
	return nil
}

func TestRecoveredDomainKeepsItsNameAndFailedSignInNamesTheSavedDomain(t *testing.T) {
	f := newFixture(t)
	f.ownDomain()
	before := len(f.launcher.starts())
	f.serving().exit()
	deadline := time.Now().Add(time.Second)
	for len(f.launcher.starts()) == before && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(f.launcher.starts()) != before+1 {
		t.Fatal("saved domain did not recover")
	}
	if s := f.waitState("connected"); s.Hostname != "nas.example.com" || s.Mode != "domain" {
		t.Fatalf("status %+v", s)
	}
	// A new name that fails names the domain still saved.
	f.tunnel.DisconnectAuthorization()
	f.signIn(goodCert())
	f.tunnel.cfg.LoginTimeout = 30 * time.Millisecond
	f.tunnel.Start(context.Background(), Request{Mode: "domain", Subdomain: "home"})
	if s := f.waitState("failed"); s.Hostname != "nas.example.com" {
		t.Fatalf("status %+v", s)
	}
	if e := f.tunnel.Start(context.Background(), Request{Mode: "domain"}); e != nil {
		t.Fatal(e)
	}
	if s := f.waitState("connected"); s.Hostname != "nas.example.com" {
		t.Fatalf("status %+v", s)
	}
}
