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

func TestRetainedReceiptReclaimsAnUnmarkedPreviousInstallName(t *testing.T) {
	const old = "99999999-8888-4777-8666-555555555555"
	record := dnsRecord{ID: "retained-record", Name: "netbird.example.com", Type: "CNAME", Content: old + ".cfargotunnel.com", Proxied: true}
	deletes := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/zones/"+testZone:
			fmt.Fprintf(w, `{"success":true,"result":{"id":%q,"name":"example.com","account":{"id":%q}}}`, testZone, testAccount)
		case r.URL.Path == "/zones/"+testZone+"/dns_records":
			json.NewEncoder(w).Encode(map[string]any{"success": true, "result": []dnsRecord{record}})
		case r.Method == "DELETE":
			deletes++
			fmt.Fprint(w, `{"success":true,"result":{"id":"retained-record"}}`)
		case r.URL.Path == "/accounts/"+testAccount+"/cfd_tunnel/"+old+"/connections":
			fmt.Fprint(w, `{"success":true,"result":[]}`)
		default:
			fmt.Fprintf(w, `{"success":true,"result":{"id":%q,"name":"unmarked-example"}}`, old)
		}
	}))
	defer server.Close()
	tunnel := NewTunnel(Config{Dir: t.TempDir(), API: API{Base: server.URL}})
	c := signedIn()
	// Matching names, a Connect prefix and even an administrator's guessed
	// fingerprint are not ownership evidence.
	n, e := tunnel.classifyName(context.Background(), c, record.Name)
	if e != nil {
		t.Fatal(e)
	}
	_, e = tunnel.cfg.API.claimName(context.Background(), c, n, tunnelID, &connector.NameReuse{Name: record.Name, Record: n.fingerprint()}, func() error { return nil })
	if connector.ErrorCode(e) != "cloudflare_name_taken" || deletes != 0 {
		t.Fatal("unproven object was reclaimed", e, deletes)
	}
	unownedSameTunnel, e := tunnel.classifyName(context.Background(), c, record.Name, old)
	if e != nil || unownedSameTunnel.reason != "other" {
		t.Fatal("pointing at our tunnel is not creation evidence", unownedSameTunnel, e)
	}
	if e = tunnel.saveOwnership(ownership{Account: testAccount, Tunnels: map[string]bool{old: true}, DNS: map[string]dnsRecord{record.Name: record}}); e != nil {
		t.Fatal(e)
	}
	// A new process uses the same private retained receipt.
	tunnel = NewTunnel(Config{Dir: tunnel.cfg.Dir, API: API{Base: server.URL}})
	n, e = tunnel.classifyName(context.Background(), c, record.Name)
	if e != nil || !n.owned {
		t.Fatal(n, e)
	}
	_, e = tunnel.cfg.API.claimName(context.Background(), c, n, tunnelID, nil, func() error { return nil })
	if e != nil || deletes != 1 {
		t.Fatal("owned inactive record not recovered", e, deletes)
	}
	record.ID = "user-replacement"
	n, e = tunnel.classifyName(context.Background(), c, record.Name)
	if e != nil || !n.owned {
		t.Fatal("record pointing to our retained tunnel was not owned", n, e)
	}
}

func TestOwnershipSurvivesForgetAndSeparateAccountSelection(t *testing.T) {
	tunnel := NewTunnel(Config{Dir: t.TempDir()})
	first := ownership{Account: testAccount, Tunnels: map[string]bool{tunnelID: true}, DNS: map[string]dnsRecord{}}
	if e := tunnel.saveOwnership(first); e != nil {
		t.Fatal(e)
	}
	second := ownership{Account: "00000000000000000000000000000000", Tunnels: map[string]bool{}, DNS: map[string]dnsRecord{}}
	if e := tunnel.saveOwnership(second); e != nil {
		t.Fatal(e)
	}
	if e := Revoke(tunnel.cfg.Dir); e != nil {
		t.Fatal(e)
	}
	got, e := tunnel.ownership(testAccount)
	if e != nil || !got.Tunnels[tunnelID] {
		t.Fatal("forget removed ownership receipts", got, e)
	}
	other, e := tunnel.ownership(second.Account)
	if e != nil || other.Tunnels[tunnelID] {
		t.Fatal("ownership leaked across accounts", other, e)
	}
}

func TestRecreatedTunnelRefusesAForeignDNSReplacement(t *testing.T) {
	const old = "99999999-8888-4777-8666-555555555555"
	receipt := dnsRecord{ID: "original", Name: "netbird.example.com", Type: "CNAME", Content: old + ".cfargotunnel.com", Proxied: true}
	current := receipt
	current.ID = "user-replacement"
	current.Content = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee.cfargotunnel.com"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/zones/"+testZone {
			fmt.Fprintf(w, `{"success":true,"result":{"id":%q,"name":"example.com","account":{"id":%q}}}`, testZone, testAccount)
			return
		}
		rows := []dnsRecord{}
		if r.URL.Query().Get("name") == receipt.Name {
			rows = []dnsRecord{current}
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": rows})
	}))
	defer server.Close()
	tunnel := NewTunnel(Config{Dir: t.TempDir(), API: API{Base: server.URL}})
	if e := tunnel.saveOwnership(ownership{Account: testAccount, Tunnels: map[string]bool{old: true}, DNS: map[string]dnsRecord{receipt.Name: receipt}}); e != nil {
		t.Fatal(e)
	}
	_, e := tunnel.recoveryClaim(context.Background(), signedIn(), receipt.Name, old, tunnelID)
	if connector.ErrorCode(e) != "cloudflare_name_taken" {
		t.Fatal("replacement DNS inherited deletion authority", e)
	}
}
