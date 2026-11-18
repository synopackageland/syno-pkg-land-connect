package cloudflare

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dsmapp/internal/connector"
)

func apiServer(t *testing.T, handle func(w http.ResponseWriter, r *http.Request)) API {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret-api-token" {
			w.WriteHeader(401)
			return
		}
		handle(w, r)
	}))
	t.Cleanup(s.Close)
	return API{Base: s.URL}
}
func signedIn() cert {
	return cert{zoneID: testZone, accountID: testAccount, apiToken: "secret-api-token"}
}

func TestZoneNameReadsTheSignedInZoneOfTheSameAccount(t *testing.T) {
	api := apiServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/zones/"+testZone {
			t.Errorf("path %s", r.URL.Path)
		}
		w.Write([]byte(`{"success":true,"result":{"id":"` + testZone + `","name":"example.com","account":{"id":"` + testAccount + `"}}}`))
	})
	name, e := api.ZoneName(context.Background(), signedIn())
	if e != nil || name != "example.com" {
		t.Fatalf("zone = %q, %v", name, e)
	}
}

func TestZoneNameRefusesAnotherAccountOrABadName(t *testing.T) {
	for _, body := range []string{
		`{"success":true,"result":{"id":"` + testZone + `","name":"example.com","account":{"id":"00000000000000000000000000000000"}}}`,
		`{"success":true,"result":{"id":"` + testZone + `","name":"bad name","account":{"id":"` + testAccount + `"}}}`,
		`{"success":true,"result":{"id":"` + testZone + `","name":"localhost","account":{"id":"` + testAccount + `"}}}`,
		`{"success":false}`,
		`not json`,
	} {
		api := apiServer(t, func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(body)) })
		if name, e := api.ZoneName(context.Background(), signedIn()); e == nil {
			t.Errorf("accepted %q from %s", name, body)
		}
	}
}

func TestAPIReportsMissingPermissionWithoutTheReply(t *testing.T) {
	api := apiServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(403)
		w.Write([]byte(`{"success":false,"errors":[{"code":10000,"message":"secret detail"}]}`))
	})
	_, e := api.ZoneName(context.Background(), signedIn())
	if connector.ErrorCode(e) != "cloudflare_permission_denied" || strings.Contains(e.Error(), "secret detail") {
		t.Fatalf("error = %v", e)
	}
	_, e = api.NameInUse(context.Background(), signedIn(), "nas.example.com")
	if connector.ErrorCode(e) != "cloudflare_permission_denied" {
		t.Fatalf("error = %v", e)
	}
}

func TestNameInUseFindsAnyExistingRecord(t *testing.T) {
	for body, want := range map[string]bool{
		`{"success":true,"result":[]}`: false,
		`{"success":true,"result":[{"id":"r1","type":"A","name":"nas.example.com","content":"192.0.2.1"}]}`: true,
	} {
		api := apiServer(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/zones/"+testZone+"/dns_records" || r.URL.Query().Get("name") != "nas.example.com" {
				t.Errorf("request %s", r.URL)
			}
			w.Write([]byte(body))
		})
		used, e := api.NameInUse(context.Background(), signedIn(), "nas.example.com")
		if e != nil || used != want {
			t.Fatalf("in use = %v, %v; want %v", used, e, want)
		}
	}
	api := apiServer(t, func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{"success":false}`)) })
	if _, e := api.NameInUse(context.Background(), signedIn(), "nas.example.com"); e == nil || errors.Is(e, context.Canceled) {
		t.Fatalf("error = %v", e)
	}
}
