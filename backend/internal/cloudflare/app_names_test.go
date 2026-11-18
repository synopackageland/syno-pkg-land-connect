package cloudflare

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"dsmapp/internal/publicname"
)

func TestDefaultAppHostnameCollisionLadderAndOwnedDNS(t *testing.T) {
	f := newFixture(t)
	f.ownDomain()
	nas := publicname.NASName()
	occupied := []string{"jellyfin.example.com"}
	first := publicname.Candidate("jellyfin", nas, 1) + ".example.com"
	second := publicname.Candidate("jellyfin", nas, 2) + ".example.com"
	// Existing local route inventory plus retained tunnel credentials proves
	// legacy ownership; a matching CNAME target alone grants no authority.
	d, e := f.tunnel.savedDomain()
	if e != nil {
		t.Fatal(e)
	}
	d.Routed = []string{second}
	if e = f.tunnel.saveRecord(d); e != nil {
		t.Fatal(e)
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Error("name suggestion wrote cloud state")
		}
		name := r.URL.Query().Get("name")
		var rows []dnsRecord
		if name == first {
			rows = []dnsRecord{{ID: "foreign", Name: name, Type: "A", Content: "192.0.2.1"}}
		}
		if name == second {
			rows = []dnsRecord{{ID: "owned", Name: name, Type: "CNAME", Content: tunnelID + ".cfargotunnel.com", Proxied: true}}
		}
		if rows == nil {
			rows = []dnsRecord{}
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": rows})
	}))
	defer api.Close()
	f.tunnel.cfg.API = API{Base: api.URL}
	got, err := f.tunnel.DefaultAppHostname(context.Background(), "SynoPkgJellyfin", occupied)
	if err != nil || got != second {
		t.Fatalf("collision default %q %v", got, err)
	}
	got, err = f.tunnel.DefaultAppHostname(context.Background(), "SynoPkgJellyfin", append(occupied, second))
	if err != nil || got != publicname.Candidate("jellyfin", nas, 3)+".example.com" {
		t.Fatalf("local reservation %q %v", got, err)
	}
}

func TestDSMDefaultCollisionLadder(t *testing.T) {
	f := newFixture(t)
	f.ownDomain()
	base, _ := f.tunnel.defaultName()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		rows := []dnsRecord{}
		for _, attempt := range []int{0, 1, 2} {
			if name == publicname.Candidate(base, publicname.NASName(), attempt)+".example.com" {
				rows = append(rows, dnsRecord{ID: "foreign"})
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": rows})
	}))
	defer api.Close()
	f.tunnel.cfg.API = API{Base: api.URL}
	got, err := f.tunnel.defaultDomainName(context.Background(), signedIn(), "example.com", nil)
	if err != nil || got != publicname.Candidate(base, publicname.NASName(), 3)+".example.com" {
		t.Fatalf("DSM collision %q %v", got, err)
	}
}

func TestRemovedAppDNSCleanupRetriesAndChecksExactOwner(t *testing.T) {
	for _, foreign := range []bool{false, true} {
		t.Run(fmt.Sprint("foreign-", foreign), func(t *testing.T) {
			f := newFixture(t)
			f.ownDomain()
			d, err := f.tunnel.savedDomain()
			if err != nil {
				t.Fatal(err)
			}
			host := "jellyfin.example.com"
			d.Routed = append(d.Routed, d.Hostname, host)
			if err = f.tunnel.saveRecord(d); err != nil {
				t.Fatal(err)
			}
			exists, fail, deletes := true, true, 0
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "DELETE" {
					deletes++
					if foreign {
						t.Error("foreign record deleted")
					}
					if fail {
						w.WriteHeader(503)
						return
					}
					exists = false
				}
				rows := []dnsRecord{}
				if exists && strings.HasSuffix(r.URL.Path, "/dns_records") {
					content := tunnelID + ".cfargotunnel.com"
					if foreign {
						content = "different.cfargotunnel.com"
					}
					rows = append(rows, dnsRecord{ID: "owned", Name: host, Type: "CNAME", Content: content, Proxied: true})
				}
				json.NewEncoder(w).Encode(map[string]any{"success": true, "result": rows})
			}))
			defer api.Close()
			f.tunnel.cfg.API = API{Base: api.URL}
			if err = f.tunnel.DeleteAppRoute(context.Background(), host); err == nil {
				t.Fatal("cleanup failure hidden")
			}
			after, _ := f.tunnel.savedDomain()
			if !slices.Contains(after.Routed, host) {
				t.Fatal("failed cleanup lost retry inventory")
			}
			fail = false
			err = f.tunnel.DeleteAppRoute(context.Background(), host)
			if foreign {
				if err == nil || deletes != 0 {
					t.Fatal("foreign record ownership ignored")
				}
				return
			}
			if err != nil || exists || deletes != 2 {
				t.Fatal("retry failed", err, deletes)
			}
			if err = f.tunnel.DeleteAppRoute(context.Background(), host); err != nil || deletes != 2 {
				t.Fatal("deleted twice", err)
			}
			after, _ = f.tunnel.savedDomain()
			if slices.Contains(after.Routed, host) || !slices.Contains(after.Routed, d.Hostname) {
				t.Fatal("incorrect route inventory")
			}
			if err = f.tunnel.DeleteAppRoute(context.Background(), d.Hostname); err == nil {
				t.Fatal("DSM deletion allowed")
			}
		})
	}
}
