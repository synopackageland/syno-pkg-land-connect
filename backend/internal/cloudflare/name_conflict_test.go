package cloudflare

import (
	"context"
	"dsmapp/internal/connector"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

const oldTunnel = "11111111-2222-4333-8444-555555555555"

func TestNameClassification(t *testing.T) {
	for _, tc := range []struct {
		name, typ, content, tunnelName string
		status                         int
		active, deleted                bool
		own, reason                    string
	}{
		{name: "own without tunnel read", typ: "CNAME", content: tunnelID + ".cfargotunnel.com", status: 401, own: tunnelID},
		{name: "inactive Connect", typ: "CNAME", content: oldTunnel + ".cfargotunnel.com", status: 200, tunnelName: "synopkgconnect-nas-example", reason: "leftover"},
		{name: "no connection permission", typ: "CNAME", content: oldTunnel + ".cfargotunnel.com", status: 200, tunnelName: "synopkgconnect-nas-example", reason: "other"},
		{name: "active Connect", typ: "CNAME", content: oldTunnel + ".cfargotunnel.com", status: 200, tunnelName: "synopkgconnect-nas-example", active: true, reason: "other_connect"},
		{name: "deleted", typ: "CNAME", content: oldTunnel + ".cfargotunnel.com", status: 200, deleted: true, reason: "leftover"},
		{name: "missing", typ: "CNAME", content: oldTunnel + ".cfargotunnel.com", status: 404, reason: "leftover"},
		{name: "no tunnel permission", typ: "CNAME", content: oldTunnel + ".cfargotunnel.com", status: 401, reason: "other"},
		{name: "A", typ: "A", content: "192.0.2.1", reason: "other"},
		{name: "foreign CNAME", typ: "CNAME", content: "else.example.com", reason: "other"},
		{name: "foreign tunnel", typ: "CNAME", content: oldTunnel + ".cfargotunnel.com", status: 200, tunnelName: "another-service", reason: "other"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/zones/" + testZone:
					fmt.Fprintf(w, `{"success":true,"result":{"id":%q,"name":"example.com","account":{"id":%q}}}`, testZone, testAccount)
				case "/zones/" + testZone + "/dns_records":
					if r.URL.Query().Get("name") != "nas.example.com" {
						fmt.Fprint(w, `{"success":true,"result":[]}`)
						return
					}
					json.NewEncoder(w).Encode(map[string]any{"success": true, "result": []any{map[string]any{"id": "record", "name": "nas.example.com", "type": tc.typ, "content": tc.content, "proxied": true}}})
				case "/accounts/" + testAccount + "/cfd_tunnel/" + oldTunnel:
					if tc.status != 200 {
						w.WriteHeader(tc.status)
						return
					}
					var deleted any
					if tc.deleted {
						deleted = "2026-01-01T00:00:00Z"
					}
					json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]any{"id": oldTunnel, "name": tc.tunnelName, "deleted_at": deleted}})
				case "/accounts/" + testAccount + "/cfd_tunnel/" + oldTunnel + "/connections":
					if tc.name == "no connection permission" {
						w.WriteHeader(401)
						return
					}
					connections := "[]"
					if tc.active {
						connections = `[{"id":"replica"}]`
					}
					fmt.Fprintf(w, `{"success":true,"result":%s}`, connections)
				default:
					t.Errorf("unexpected API request %s", r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer s.Close()
			claim, err := (API{Base: s.URL}).classifyName(context.Background(), signedIn(), "nas.example.com", tc.own)
			if err != nil || claim.reason != tc.reason {
				t.Fatalf("reason=%q err=%v", claim.reason, err)
			}
			if tc.reason != "" {
				conflict := (API{Base: s.URL}).conflict(context.Background(), signedIn(), claim)
				details := connector.Conflict(conflict)
				if connector.ErrorCode(conflict) != "cloudflare_name_taken" || details == nil || details.Reason != tc.reason || details.SuggestedName != "nas-2.example.com" {
					t.Fatalf("missing conflict recovery: %v %+v", conflict, details)
				}
				if !claim.owned && allowedClaim(claim, &connector.NameReuse{Name: claim.name, Record: claim.fingerprint()}) {
					t.Fatal("foreign record accepted explicit overwrite")
				}
			}
		})
	}
}
func TestTakeoverRechecksRecordAndDefersTunnelCleanup(t *testing.T) {
	for _, scenario := range []string{"reuse", "delete failure", "record race", "tunnel starts", "no consent"} {
		t.Run(scenario, func(t *testing.T) {
			deletes, routes, reads := 0, 0, 0
			record := dnsRecord{ID: "record", Name: "nas.example.com", Type: "CNAME", Content: oldTunnel + ".cfargotunnel.com", Proxied: true}
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/zones/"+testZone:
					fmt.Fprintf(w, `{"success":true,"result":{"id":%q,"name":"example.com","account":{"id":%q}}}`, testZone, testAccount)
				case r.URL.Path == "/zones/"+testZone+"/dns_records":
					reads++
					row := record
					if scenario == "record race" && reads > 1 {
						row.Content = "other.example.com"
					}
					json.NewEncoder(w).Encode(map[string]any{"success": true, "result": []dnsRecord{row}})
				case r.URL.Path == "/zones/"+testZone+"/dns_records/record":
					if r.Method != "DELETE" {
						t.Errorf("unexpected write %s", r.Method)
					}
					deletes++
					fmt.Fprint(w, `{"success":true,"result":{"id":"record"}}`)
				case r.URL.Path == "/accounts/"+testAccount+"/cfd_tunnel/"+oldTunnel+"/connections":
					if scenario == "tunnel starts" && reads > 1 {
						fmt.Fprint(w, `{"success":true,"result":[{"id":"replica"}]}`)
					} else {
						fmt.Fprint(w, `{"success":true,"result":[]}`)
					}
				default:
					if r.Method == "DELETE" {
						deletes++
						if scenario == "delete failure" {
							w.WriteHeader(403)
							return
						}
					}
					fmt.Fprintf(w, `{"success":true,"result":{"id":%q,"name":"synopkgconnect-nas-example"}}`, oldTunnel)
				}
			}))
			defer s.Close()
			a := API{Base: s.URL}
			claim, err := a.classifyName(context.Background(), signedIn(), record.Name, "")
			if err != nil {
				t.Fatal(err)
			}
			claim.owned = scenario != "no consent"
			reuse := &connector.NameReuse{Name: record.Name, Record: claim.fingerprint()}
			if scenario == "no consent" {
				reuse = nil
			}
			note, err := a.claimName(context.Background(), signedIn(), claim, tunnelID, reuse, func() error { routes++; return nil })
			success := scenario == "reuse" || scenario == "delete failure" || scenario == "tunnel starts"
			if success {
				if err != nil || routes != 1 || deletes != 1 {
					t.Fatalf("err=%v routes=%d deletes=%d", err, routes, deletes)
				}
				if note != "" {
					t.Fatalf("note %q", note)
				}
			} else if connector.ErrorCode(err) != "cloudflare_name_taken" || deletes != 0 || routes != 0 {
				t.Fatalf("unsafe reuse: %v deletes=%d routes=%d", err, deletes, routes)
			}
		})
	}
}

func TestConflictSuggestsNextFreeNameAndDashboard(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/zones/"+testZone {
			fmt.Fprintf(w, `{"success":true,"result":{"id":%q,"name":"example.com","account":{"id":%q}}}`, testZone, testAccount)
			return
		}
		rows := []map[string]string{}
		if r.URL.Query().Get("name") == "nas-2.example.com" {
			rows = append(rows, map[string]string{"id": "taken"})
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": rows})
	}))
	defer s.Close()
	err := (API{Base: s.URL}).conflict(context.Background(), signedIn(), nameClaim{name: "nas.example.com", reason: "other"})
	d := connector.Conflict(err)
	if d == nil || d.SuggestedName != "nas-3.example.com" || d.DNSURL != "https://dash.cloudflare.com/"+testAccount+"/example.com/dns/records" {
		t.Fatalf("conflict: %v %+v", err, d)
	}
}

func TestRegisterSameSavedNameKeepsOwnTunnel(t *testing.T) {
	f := newFixture(t)
	f.ownDomain()
	d, err := f.tunnel.savedDomain()
	if err != nil {
		t.Fatal(err)
	}
	before := len(f.launcher.runs())
	f.inUse = true
	if err = f.tunnel.Start(context.Background(), Request{Mode: "domain", Subdomain: "nas"}); err != nil {
		t.Fatal(err)
	}
	got := f.waitState("connected")
	saved, err := f.tunnel.savedDomain()
	if err != nil || saved.TunnelID != d.TunnelID || len(f.launcher.runs()) != before {
		t.Fatalf("own record refused or tunnel recreated: %+v %v", got, err)
	}
}
