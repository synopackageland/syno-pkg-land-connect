package cfaccess

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPreflightDoesNotConfuseForbiddenWithMissingOrganization(t *testing.T) {
	for _, tc := range []struct {
		name    string
		details bool
		org     int
		want    string
	}{
		{"unknown permissions", false, 403, "access_permission_denied"},
		{"verified permissions", true, 403, "access_needs_zero_trust"},
		{"available", true, 200, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer private-fixture" {
					t.Error("missing credential")
				}
				switch r.URL.Path {
				case "/user/tokens/verify":
					fmt.Fprint(w, `{"success":true,"result":{"id":"token-id","status":"active"}}`)
				case "/user/tokens/token-id":
					if !tc.details {
						w.WriteHeader(403)
						fmt.Fprint(w, `{"success":false}`)
						return
					}
					fmt.Fprint(w, `{"success":true,"result":{"policies":[{"effect":"allow","resources":{"com.cloudflare.api.account.account-id":"*"},"permission_groups":[{"name":"Access: Apps and Policies Write"},{"name":"Access: Organizations, Identity Providers, and Groups Write"}]}]}}`)
				case "/accounts/account-id/access/organizations":
					if tc.org != 200 {
						w.WriteHeader(tc.org)
						fmt.Fprint(w, `{"success":false,"errors":[{"code":10000,"message":"private-fixture"}]}`)
						return
					}
					fmt.Fprint(w, `{"success":true,"result":{"auth_domain":"fixture.cloudflareaccess.com"}}`)
				case "/accounts/account-id/access/apps", "/accounts/account-id/access/identity_providers":
					fmt.Fprint(w, `{"success":true,"result":[],"result_info":{"total_pages":1}}`)
				default:
					t.Errorf("unexpected %s", r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer s.Close()
			_, err := (API{Base: s.URL}).Preflight(context.Background(), Credential{Account: "account-id", Token: "private-fixture"})
			if Code(err) != tc.want {
				t.Fatalf("code %s want %s", Code(err), tc.want)
			}
			if err != nil && strings.Contains(err.Error(), "private-fixture") {
				t.Fatal("secret in error")
			}
		})
	}
}

func TestPaginationAndRefusedMutation(t *testing.T) {
	pages := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" {
			w.WriteHeader(403)
			fmt.Fprint(w, `{"success":false,"errors":[{"message":"secret"}]}`)
			return
		}
		pages++
		fmt.Fprintf(w, `{"success":true,"result":[{"id":%q,"name":"foreign","type":"self_hosted","domain":"web.example.test"}],"result_info":{"page":%d,"total_pages":2}}`, fmt.Sprint(pages), pages)
	}))
	defer s.Close()
	a := API{Base: s.URL}
	c := Credential{Account: "account-id", Token: "secret"}
	apps, err := a.Apps(context.Background(), c)
	if err != nil || len(apps) != 2 || pages != 2 {
		t.Fatal(len(apps), pages, err)
	}
	if err = a.DeleteApp(context.Background(), c, "id"); Code(err) != "access_permission_denied" {
		t.Fatal(err)
	}
}

func TestAPINeverFollowsRedirectOrAcceptsTruncatedResponse(t *testing.T) {
	hit := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hit = true }))
	defer target.Close()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 302) }))
	defer s.Close()
	_, err := (API{Base: s.URL, HTTP: target.Client()}).Apps(context.Background(), Credential{Account: "account-id", Token: "secret"})
	if err == nil || hit {
		t.Fatal("redirect followed")
	}
}

func TestRecordedApplicationShape(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var app Application
		if json.NewDecoder(r.Body).Decode(&app) != nil {
			t.Fatal("body")
		}
		if app.Type != "self_hosted" || len(app.Policies) != 1 || app.Policies[0].Decision != "allow" || len(app.AllowedIDPs) != 1 || app.Session != "720h" {
			t.Error("unexpected policy")
		}
		app.Destinations = []map[string]any{{"type": "public", "uri": app.Domain}}
		app.ID = "app-id"
		app.AUD = "audience"
		app.Policies[0].ID = "policy-id"
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": app})
	}))
	defer s.Close()
	a := desiredApp("Connect owner nonce", "web.example.test", "otp-id", []string{"reader@example.test"}, "")
	got, err := (API{Base: s.URL}).SaveApp(context.Background(), Credential{Account: "account-id", Token: "secret"}, a)
	if err != nil || got.ID != "app-id" || got.AUD != "audience" {
		t.Fatal(got.ID, err)
	}
}
