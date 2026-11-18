package cloudflare

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The only in-place change left keeps the same ingress: it never starts a
// second connector or touches DNS, and the running one keeps serving.
func TestSameDomainUpdateKeepsTheRunningConnector(t *testing.T) {
	f := newFixture(t)
	f.ownDomain()
	old := f.serving()
	before, _ := os.ReadFile(f.tunnel.domainFile("domain.json"))
	f.tunnel.cfg.CheckPort = func(context.Context, int) error { return nil }
	prior := f.launcher.onRun
	f.launcher.onRun = func(c Command) error {
		if len(c.Args) == 1 && c.Args[0] == "--version" {
			return nil
		}
		return prior(c)
	}
	runs := len(f.launcher.runs())
	if e := f.tunnel.CheckUpdate(context.Background(), Request{Mode: "domain"}); e != nil {
		t.Fatal(e)
	}
	if e := f.tunnel.StageUpdate(context.Background(), Request{Mode: "domain"}); e != nil {
		t.Fatal(e)
	}
	if ready, e := f.tunnel.VerifyUpdate(context.Background()); !ready || e != nil {
		t.Fatal(ready, e)
	}
	if e := f.tunnel.CommitUpdate(context.Background()); e != nil {
		t.Fatal(e)
	}
	f.tunnel.FinishUpdate(true)
	after, _ := os.ReadFile(f.tunnel.domainFile("domain.json"))
	if !old.Alive() || string(before) != string(after) || len(f.launcher.runs()) != runs+1 || f.tunnel.safe != nil {
		t.Fatalf("update changed the connection: alive %v runs %d→%d", old.Alive(), runs, len(f.launcher.runs()))
	}
	if e := f.tunnel.CheckUpdate(context.Background(), Request{Mode: "domain", Subdomain: "other"}); e == nil {
		t.Fatal("a new name was staged in place")
	}
}

// An own domain saved by a version with TCP services starts cleanly: the
// services, their ingress and the DNS journal are dropped without asking.
func TestOldTCPServicesAreDroppedOnRestore(t *testing.T) {
	f := newFixture(t)
	f.ownDomain()
	f.tunnel.Stop()
	raw, err := os.ReadFile(f.tunnel.domainFile("domain.json"))
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if err = json.Unmarshal(raw, &record); err != nil {
		t.Fatal(err)
	}
	record["services"] = []map[string]any{{"port": 6690, "hostname": "nas-drive.example.com"}, {"port": 22, "hostname": "nas-ssh.example.com"}}
	record["routed"] = []string{"nas-drive.example.com", "nas-ssh.example.com"}
	raw, _ = json.Marshal(record)
	for path, body := range map[string]string{
		f.tunnel.domainFile("domain.json"):    string(raw),
		f.tunnel.domainFile("config.yml"):     "ingress:\n  - hostname: \"nas-drive.example.com\"\n    service: \"tcp://127.0.0.1:6690\"\n",
		f.tunnel.domainFile("apply-dns.json"): `{"tunnelId":"` + tunnelID + `","names":["nas-ssh.example.com"]}`,
	} {
		if err = os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	logins := f.logins()
	restarted := NewTunnel(f.tunnel.cfg)
	if err = restarted.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer restarted.Stop()
	f.tunnel = restarted
	s := f.waitState("connected")
	if s.Hostname != "nas.example.com" || strings.Contains(f.config(), "tcp://") || f.logins() != logins {
		t.Fatalf("restored %+v config %s", s, f.config())
	}
	if _, err = os.Stat(f.tunnel.domainFile("apply-dns.json")); !os.IsNotExist(err) {
		t.Fatal("the TCP DNS journal was kept", err)
	}
	if raw, _ = os.ReadFile(f.tunnel.domainFile("domain.json")); strings.Contains(string(raw), "services") {
		t.Fatalf("services kept: %s", raw)
	}
}
