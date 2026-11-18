package cloudflare

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const replacementID = "a0833b13-41ef-41df-bbfd-ca30c11e7012"

type recoveryCloud struct {
	dnsReads                                       map[string]int
	changeOnRead                                   int
	mu                                             sync.Mutex
	records                                        map[string]dnsRecord
	status, reads, creates, deletes, routeFailures int
	deletedAt                                      bool
}

func recoveryFixture(t *testing.T) (*fixture, *recoveryCloud) {
	t.Helper()
	f := newFixture(t)
	f.ownDomain()
	f.tunnel.Stop()
	c := &recoveryCloud{dnsReads: map[string]int{}, status: 404, records: map[string]dnsRecord{
		"nas.example.com": {ID: "dsm", Name: "nas.example.com", Type: "CNAME", Content: tunnelID + ".cfargotunnel.com", Proxied: true},
	}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		defer c.mu.Unlock()
		reply := func(v any) { json.NewEncoder(w).Encode(map[string]any{"success": true, "result": v}) }
		switch {
		case strings.HasSuffix(r.URL.Path, "/cfd_tunnel/"+tunnelID):
			c.reads++
			if c.status == -1 {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					conn.Close()
				}
				return
			}
			if c.status != 200 {
				w.WriteHeader(c.status)
				return
			}
			result := map[string]any{"id": tunnelID, "name": "synopkgconnect-nas-fixture"}
			if c.deletedAt {
				result["deleted_at"] = "2026-01-01T00:00:00Z"
			}
			reply(result)
		case strings.HasSuffix(r.URL.Path, "/cfd_tunnel/"+replacementID):
			reply(map[string]string{"id": replacementID, "name": "synopkgconnect-nas-new"})
		case strings.HasSuffix(r.URL.Path, "/dns_records"):
			name := r.URL.Query().Get("name")
			c.dnsReads[name]++
			if c.changeOnRead > 0 && c.dnsReads[name] == c.changeOnRead {
				c.records[name] = dnsRecord{ID: "racer", Name: name, Type: "A", Content: "192.0.2.1"}
			}
			rows := []dnsRecord{}
			if row, ok := c.records[r.URL.Query().Get("name")]; ok {
				rows = append(rows, row)
			}
			reply(rows)
		case r.Method == "PATCH" && strings.Contains(r.URL.Path, "/dns_records/"):
			var patch struct {
				Comment string `json:"comment"`
			}
			if json.NewDecoder(r.Body).Decode(&patch) != nil {
				w.WriteHeader(400)
				return
			}
			id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			for name, row := range c.records {
				if row.ID == id {
					row.Comment = patch.Comment
					c.records[name] = row
					reply(row)
					return
				}
			}
			w.WriteHeader(404)
		case r.Method == "DELETE" && strings.Contains(r.URL.Path, "/dns_records/"):
			id := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
			for name, row := range c.records {
				if row.ID == id {
					delete(c.records, name)
				}
			}
			c.deletes++
			reply(map[string]string{"id": id})
		case strings.Contains(r.URL.Path, "/cfd_tunnel/"):
			w.WriteHeader(404)
		case strings.HasSuffix(r.URL.Path, "/zones/"+testZone):
			reply(map[string]any{"id": testZone, "name": "example.com", "account": map[string]string{"id": testAccount}})
		default:
			t.Errorf("unexpected fake API request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	t.Cleanup(srv.Close)
	f.tunnel.cfg.API = API{Base: srv.URL}
	f.launcher.onStart = func(cmd Command, p *fakeProc) {
		if slices.Contains(cmd.Args, "run") && cmd.Args[len(cmd.Args)-1] == tunnelID {
			fmt.Fprintln(cmd.Output, `ERR Unauthorized: Tunnel not found`)
			p.exit()
		}
	}
	f.launcher.onRun = func(cmd Command) error {
		c.mu.Lock()
		defer c.mu.Unlock()
		switch cmd.Args[3] {
		case "create":
			c.creates++
			if !strings.HasPrefix(cmd.Args[len(cmd.Args)-1], "synopkgconnect-nas-") {
				t.Error("changed naming rules")
			}
			at := slices.Index(cmd.Args, "--credentials-file")
			return os.WriteFile(cmd.Args[at+1], credentials{account: testAccount, tunnelID: replacementID, secret: strings.Repeat("A", 43) + "="}.privateJSON(), 0600)
		case "route":
			if slices.Contains(cmd.Args, "--overwrite-dns") {
				t.Error("unsafe overwrite")
			}
			if c.routeFailures > 0 {
				c.routeFailures--
				return errors.New("temporary route failure")
			}
			name := cmd.Args[len(cmd.Args)-1]
			if _, exists := c.records[name]; exists {
				return errors.New("record already exists")
			}
			c.records[name] = dnsRecord{ID: "new-" + name, Name: name, Type: "CNAME", Content: cmd.Args[len(cmd.Args)-2] + ".cfargotunnel.com", Proxied: true}
		default:
			t.Errorf("unexpected fake command %s", cmd.Args[3])
		}
		return nil
	}
	return f, c
}

func waitRecreated(t *testing.T, f *fixture) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		d, err := f.tunnel.savedDomain()
		if err == nil && d.TunnelID == replacementID && f.tunnel.Status().State == "connected" {
			s := f.tunnel.Status()
			if s.Code != "" || s.RecoveryNotice != "cloudflare_tunnel_recreated" {
				t.Fatalf("recovery did not clear status: %+v", s)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("saved tunnel did not recover: %+v", f.tunnel.Status())
}

func TestDeletedSavedTunnelRecreatesOnSameNameWithoutLogin(t *testing.T) {
	for _, name := range []string{"", "nas"} {
		t.Run("reconnect-"+name, func(t *testing.T) {
			f, c := recoveryFixture(t)
			logins := f.logins()
			if err := f.tunnel.Domain(context.Background(), name); err != nil {
				t.Fatal(err)
			}
			waitRecreated(t, f)
			if f.logins() != logins {
				t.Fatal("valid saved authorization must not ask for login")
			}
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.creates != 1 || c.deletes != 1 || c.records["nas.example.com"].Content != replacementID+".cfargotunnel.com" {
				t.Fatalf("recovery mutations: %+v", c)
			}
			if !strings.Contains(f.config(), replacementID) {
				t.Fatal("ingress still uses deleted tunnel")
			}
		})
	}
}

func TestMissingTunnelWithForeignDNSNeverTakesOver(t *testing.T) {
	f, c := recoveryFixture(t)
	c.records["nas.example.com"] = dnsRecord{ID: "foreign", Name: "nas.example.com", Type: "CNAME", Content: "foreign.example.net", Proxied: true}
	if err := f.tunnel.Domain(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	s := f.waitState("failed")
	if s.Code != "cloudflare_name_taken" {
		t.Fatalf("wrong refusal: %+v", s)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.creates != 0 || c.deletes != 0 {
		t.Fatal("foreign record caused mutations")
	}
}

func TestUnprovenDeletionRetainsCredentialsAndRecoversAfterOutage(t *testing.T) {
	for _, status := range []int{-1, 200, 429, 500, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			f, c := recoveryFixture(t)
			c.status = status
			f.tunnel.cfg.RestartDelay = time.Millisecond
			before, _ := os.ReadFile(f.tunnel.domainFile("credentials.json"))
			if err := f.tunnel.Domain(context.Background(), ""); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(8 * time.Second)
			requiredReads := 3
			if status == 503 {
				requiredReads = 10
			}
			for {
				c.mu.Lock()
				reads := c.reads
				creates := c.creates
				deletes := c.deletes
				c.mu.Unlock()
				if creates != 0 || deletes != 0 {
					t.Fatal("unproven deletion mutated resources")
				}
				if reads >= requiredReads {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("transient API failure was not retried")
				}
				time.Sleep(5 * time.Millisecond)
			}
			after, _ := os.ReadFile(f.tunnel.domainFile("credentials.json"))
			if string(before) != string(after) || f.logins() != 1 {
				t.Fatal("outage discarded saved credentials or requested login")
			}
			if s := f.tunnel.Status(); s.State == "failed" {
				t.Fatalf("outage exhausted a failure budget: %+v", s)
			}
			c.mu.Lock()
			c.status = 404
			c.mu.Unlock()
			waitRecreated(t, f)
		})
	}
}

func TestDeletedAtAndMissingDNSRecreate(t *testing.T) {
	f, c := recoveryFixture(t)
	c.status = 200
	c.deletedAt = true
	delete(c.records, "nas.example.com")
	if err := f.tunnel.Domain(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	waitRecreated(t, f)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.creates != 1 || c.deletes != 0 {
		t.Fatal("wrong create/delete count")
	}
}

func TestRecreationResumesSameReplacementAfterRouteFailureAndRestart(t *testing.T) {
	f, c := recoveryFixture(t)
	c.routeFailures = 100
	if err := f.tunnel.Domain(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		c.mu.Lock()
		deleted := c.deletes
		c.mu.Unlock()
		if deleted > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never reached DNS repair")
		}
		time.Sleep(5 * time.Millisecond)
	}
	f.tunnel.Stop()
	private(t, f.tunnel.domainFile("recreate.json"), 0600)
	c.mu.Lock()
	c.routeFailures = 0
	c.mu.Unlock()
	f.tunnel = NewTunnel(f.tunnel.cfg)
	if err := f.tunnel.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitRecreated(t, f)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.creates != 1 {
		t.Fatal("restart created another replacement")
	}
	if _, err := os.Stat(f.tunnel.domainFile("recreate.json")); !os.IsNotExist(err) {
		t.Fatal("completed journal retained")
	}
}

func TestDeletedTunnelRequestsLoginOnlyWhenAuthorizationIsMissingOrRefused(t *testing.T) {
	for _, status := range []int{0, 401, 403} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			f, c := recoveryFixture(t)
			if status == 0 {
				os.Remove(f.tunnel.certFile())
			} else {
				c.status = status
			}
			oldStart := f.launcher.onStart
			f.launcher.onStart = func(cmd Command, p *fakeProc) {
				if hasArgs(cmd, "tunnel", "login") {
					fmt.Fprintln(cmd.Output, "https://dash.cloudflare.com/argotunnel?callback=https%3A%2F%2Flogin.cloudflareaccess.org%2Ffake")
					return
				}
				oldStart(cmd, p)
			}
			if err := f.tunnel.Domain(context.Background(), ""); err != nil {
				t.Fatal(err)
			}
			f.waitState("needs-login")
			c.mu.Lock()
			defer c.mu.Unlock()
			if c.creates != 0 || c.deletes != 0 {
				t.Fatal("missing/refused auth recreated without authorization")
			}
		})
	}
}

type recoveryTargets struct{ routes []AppRoute }

func (a recoveryTargets) ActivePublic(context.Context) ([]AppRoute, error) {
	return slices.Clone(a.routes), nil
}
func (a recoveryTargets) AppExists(_ context.Context, pkg string) bool {
	for _, route := range a.routes {
		if route.Package == pkg {
			return true
		}
	}
	return false
}
func (recoveryTargets) ActivatePublic(context.Context, []AppRoute) error      { return nil }
func (recoveryTargets) PausePublic(context.Context, []AppRoute, string) error { return nil }

func TestRecreationRestoresApprovedAppAndServerIngressOnly(t *testing.T) {
	f, c := recoveryFixture(t)
	appHost, serverHost, withdrawn := "one.example.com", "netbird.example.com", "old-drive.example.com"
	d, err := f.tunnel.savedDomain()
	if err != nil {
		t.Fatal(err)
	}
	d.Routed = []string{appHost, serverHost, withdrawn}
	if err = f.tunnel.saveRecord(d); err != nil {
		t.Fatal(err)
	}
	f.tunnel.cfg.Apps = recoveryTargets{routes: []AppRoute{appTarget("Example", appHost, "one")}}
	f.tunnel.cfg.NetBirdServer = func() (ServerOrigin, bool) {
		return ServerOrigin{Hostname: serverHost, Port: 8443, CAFile: "/private/server.pem"}, true
	}
	for _, name := range d.Routed {
		c.records[name] = dnsRecord{ID: name, Name: name, Type: "CNAME", Content: tunnelID + ".cfargotunnel.com", Proxied: true}
	}
	if err = f.tunnel.Domain(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	waitRecreated(t, f)
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, name := range []string{d.Hostname, appHost, serverHost} {
		if c.records[name].Content != replacementID+".cfargotunnel.com" {
			t.Fatalf("name not repaired: %s", name)
		}
	}
	if c.records[withdrawn].Content != tunnelID+".cfargotunnel.com" {
		t.Fatal("historical route was republished")
	}
	config := f.config()
	if !strings.Contains(config, appHost) || !strings.Contains(config, serverHost) || !strings.Contains(config, "http2Origin: true") || strings.Contains(config, withdrawn) || strings.Contains(config, "noTLSVerify") {
		t.Fatal("approved ingress was not restored")
	}
}

func TestRecreationDNSRaceRefusesChangedRecord(t *testing.T) {
	f, c := recoveryFixture(t)
	c.changeOnRead = 3 // preflight, observation, then immediate re-read before delete
	if err := f.tunnel.Domain(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	s := f.waitState("failed")
	if s.Code != "cloudflare_name_taken" {
		t.Fatalf("wrong refusal %+v", s)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.deletes != 0 || c.records["nas.example.com"].ID != "racer" {
		t.Fatal("competing record was changed")
	}
}

func TestStoppingRecoveryWhileAnotherOperationHoldsLockDoesNotDeadlock(t *testing.T) {
	f, _ := recoveryFixture(t)
	f.tunnel.ops.Lock()
	defer f.tunnel.ops.Unlock()
	if err := f.tunnel.Domain(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { f.tunnel.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop deadlocked on recovery operations lock")
	}
}

func TestRunningTunnelDeletionAutomaticallyRecovers(t *testing.T) {
	f, _ := recoveryFixture(t)
	f.launcher.onStart = nil
	if err := f.tunnel.Domain(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	f.waitState("connected")
	commands := f.launcher.starts()
	fmt.Fprintln(commands[len(commands)-1].Output, "ERR Unauthorized: Tunnel not found")
	waitRecreated(t, f)
	if f.logins() != 1 {
		t.Fatal("live recovery requested unnecessary login")
	}
}

func TestMissingAuthorizationContinuesRecreationAfterLogin(t *testing.T) {
	f, _ := recoveryFixture(t)
	os.Remove(f.tunnel.certFile())
	oldStart := f.launcher.onStart
	f.launcher.onStart = func(cmd Command, p *fakeProc) {
		if hasArgs(cmd, "tunnel", "login") {
			if err := os.WriteFile(f.tunnel.certFile(), goodCert(), 0600); err != nil {
				t.Error(err)
			}
			p.exit()
			return
		}
		oldStart(cmd, p)
	}
	if err := f.tunnel.Domain(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	waitRecreated(t, f)
	if f.logins() != 2 {
		t.Fatal("missing authorization did not sign in exactly once")
	}
}
