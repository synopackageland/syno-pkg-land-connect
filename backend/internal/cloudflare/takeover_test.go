package cloudflare

import (
	"context"
	"dsmapp/internal/connector"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRemoteOwnershipRecoversWithoutLocalJournal(t *testing.T) {
	for _, scenario := range []string{"record marker", "tunnel marker", "active Connect", "foreign", "foreign record with unrelated marker", "unmarked missing tunnel", "marker mismatches target", "racing replacement"} {
		t.Run(scenario, func(t *testing.T) {
			record := dnsRecord{ID: "previous", Name: "photos.example.com", Type: "CNAME", Content: oldTunnel + ".cfargotunnel.com", Proxied: true}
			remoteName := "foreign-service"
			reads, deletes, routes := 0, 0, 0
			if scenario == "record marker" || scenario == "racing replacement" {
				record.Comment = dnsMarker(oldTunnel)
			}
			if scenario == "tunnel marker" || scenario == "active Connect" {
				remoteName = "synopkgconnect-photos-example"
			}
			if scenario == "foreign record with unrelated marker" {
				record.Comment = dnsMarker(oldTunnel)
				record.Content = "other.example.com"
			}
			if scenario == "marker mismatches target" {
				record.Comment = dnsMarker(tunnelID)
			}
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reply := func(v any) { json.NewEncoder(w).Encode(map[string]any{"success": true, "result": v}) }
				switch {
				case strings.HasSuffix(r.URL.Path, "/dns_records"):
					rows := []dnsRecord{}
					if r.URL.Query().Get("name") == record.Name {
						reads++
						row := record
						if scenario == "racing replacement" && reads > 1 {
							row.ID = "foreign-replacement"
							row.Comment = ""
						}
						rows = append(rows, row)
					}
					reply(rows)
				case r.Method == "DELETE":
					deletes++
					reply(map[string]string{"id": record.ID})
				case strings.HasSuffix(r.URL.Path, "/connections"):
					if scenario == "active Connect" {
						reply([]map[string]string{{"id": "active"}})
					} else {
						reply([]any{})
					}
				case strings.Contains(r.URL.Path, "/cfd_tunnel/"):
					if scenario == "record marker" || scenario == "unmarked missing tunnel" || scenario == "racing replacement" {
						w.WriteHeader(404)
					} else {
						reply(map[string]string{"id": oldTunnel, "name": remoteName})
					}
				default:
					reply(map[string]any{"id": testZone, "name": "example.com", "account": map[string]string{"id": testAccount}})
				}
			}))
			defer api.Close()
			tunnel := NewTunnel(Config{Dir: t.TempDir(), API: API{Base: api.URL}})
			observed, err := tunnel.classifyName(context.Background(), signedIn(), record.Name)
			if err != nil {
				t.Fatal(err)
			}
			_, err = tunnel.cfg.API.claimName(context.Background(), signedIn(), observed, tunnelID, nil, func() error { routes++; return nil })
			own := scenario == "record marker" || scenario == "tunnel marker" || scenario == "active Connect"
			if own {
				if err != nil || deletes != 1 || routes != 1 {
					t.Fatal("own record not taken over", err, deletes, routes)
				}
			} else if connector.ErrorCode(err) != "cloudflare_name_taken" || deletes != 0 || routes != 0 {
				t.Fatal("foreign or raced record mutated", err, deletes, routes)
			}
		})
	}
}

func TestCleanupRechecksTunnelMarkerAndActivityAfterSwitch(t *testing.T) {
	for _, scenario := range []string{"owned", "foreign", "active", "unreadable", "delete failure"} {
		t.Run(scenario, func(t *testing.T) {
			deletes := 0
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.Method == "DELETE":
					deletes++
					if scenario == "delete failure" {
						w.WriteHeader(503)
						return
					}
					fmt.Fprint(w, `{"success":true,"result":{}}`)
				case strings.HasSuffix(r.URL.Path, "/connections"):
					if scenario == "unreadable" {
						w.WriteHeader(403)
						return
					}
					rows := []any{}
					if scenario == "active" {
						rows = append(rows, map[string]string{"id": "live"})
					}
					json.NewEncoder(w).Encode(map[string]any{"success": true, "result": rows})
				default:
					name := "synopkgconnect-photos-example"
					if scenario == "foreign" {
						name = "foreign-service"
					}
					fmt.Fprintf(w, `{"success":true,"result":{"id":%q,"name":%q}}`, oldTunnel, name)
				}
			}))
			defer api.Close()
			tunnel := NewTunnel(Config{Dir: t.TempDir(), API: API{Base: api.URL}})
			if err := tunnel.retireTunnel(signedIn(), oldTunnel); err != nil {
				t.Fatal(err)
			}
			// Before replacement commits, cleanup cannot delete the current identity.
			tunnel.cleanupRetired(context.Background(), signedIn(), oldTunnel)
			if deletes != 0 {
				t.Fatal("current tunnel deleted")
			}
			// A new process resumes the durable retirement queue.
			tunnel = NewTunnel(tunnel.cfg)
			tunnel.cleanupRetired(context.Background(), signedIn(), tunnelID)
			want := 0
			if scenario == "owned" || scenario == "delete failure" {
				want = 1
			}
			if deletes != want {
				t.Fatal("unsafe cleanup count", deletes, want)
			}
			ledger, _ := tunnel.ownership(testAccount)
			if ledger.Retired[oldTunnel] != (scenario != "owned") {
				t.Fatal("cleanup queue lost retry")
			}
			if (tunnel.Status().CleanupNote != "") != (scenario != "owned") {
				t.Fatal("cleanup error invisible")
			}
		})
	}
}

func TestRouteMarksDNSAndKeepsExactReadbackReceipt(t *testing.T) {
	f, c := recoveryFixture(t)
	if err := f.tunnel.rememberTunnel(credentials{account: testAccount, tunnelID: replacementID, secret: strings.Repeat("A", 43) + "="}); err != nil {
		t.Fatal(err)
	}
	c.records["photos.example.com"] = dnsRecord{ID: "new", Name: "photos.example.com", Type: "CNAME", Content: replacementID + ".cfargotunnel.com", Proxied: true}
	if err := f.tunnel.route(context.Background(), "/opt/cloudflared", replacementID, "photos.example.com"); err != nil {
		t.Fatal(err)
	}
	c.mu.Lock()
	record := c.records["photos.example.com"]
	c.mu.Unlock()
	if record.Comment != dnsMarker(replacementID) {
		t.Fatal("remote DNS marker not saved", record.Comment)
	}
	ledger, err := f.tunnel.ownership(testAccount)
	if err != nil || ledger.DNS[record.Name] != record {
		t.Fatal("receipt does not match marked record", err)
	}
}

func TestReconnectSavedLiveTunnelRepairsOwnStaleDNSAndBlocksForeign(t *testing.T) {
	for _, owned := range []bool{true, false} {
		t.Run(fmt.Sprint("owned-", owned), func(t *testing.T) {
			f, c := recoveryFixture(t)
			c.status = 200
			f.launcher.onStart = nil
			row := dnsRecord{ID: "previous", Name: "nas.example.com", Type: "CNAME", Content: oldTunnel + ".cfargotunnel.com", Proxied: true}
			if owned {
				row.Comment = dnsMarker(oldTunnel)
			}
			c.records[row.Name] = row
			err := f.tunnel.Domain(context.Background(), "")
			if !owned {
				if connector.ErrorCode(err) != "cloudflare_name_taken" {
					t.Fatal("foreign record not blocked", err)
				}
				if c.deletes != 0 {
					t.Fatal("foreign record deleted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			f.waitState("connected")
			c.mu.Lock()
			got := c.records[row.Name]
			c.mu.Unlock()
			if got.Content != tunnelID+".cfargotunnel.com" || got.Comment != dnsMarker(tunnelID) {
				t.Fatal("saved identity did not repair DNS")
			}
			d, err := f.tunnel.savedDomain()
			if err != nil || d.TunnelID != tunnelID {
				t.Fatal("reconnect unnecessarily replaced tunnel", err)
			}
		})
	}
}

func TestDeletedSavedTunnelReclaimsAnotherMarkedConnectTarget(t *testing.T) {
	f, c := recoveryFixture(t)
	c.records["nas.example.com"] = dnsRecord{ID: "previous", Name: "nas.example.com", Type: "CNAME", Content: oldTunnel + ".cfargotunnel.com", Proxied: true, Comment: dnsMarker(oldTunnel)}
	if err := f.tunnel.Domain(context.Background(), ""); err != nil {
		t.Fatal(err)
	}
	waitRecreated(t, f)
	c.mu.Lock()
	row := c.records["nas.example.com"]
	c.mu.Unlock()
	if row.Content != replacementID+".cfargotunnel.com" || row.Comment != dnsMarker(replacementID) {
		t.Fatal("marked stale target not recovered")
	}
}
