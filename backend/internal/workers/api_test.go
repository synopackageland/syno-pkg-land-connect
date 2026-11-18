package workers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dsmapp/internal/apps"
	"dsmapp/internal/connector"
)

func TestVerificationRefusalsAreSafeAndNeverSaveCredentials(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		for _, tc := range []struct {
			status int
			code   string
		}{
			{400, "workers_token_invalid"}, {401, "workers_token_invalid"},
			{403, "workers_permission_denied"}, {429, "workers_rate_limited"}, {500, "workers_api_refused"},
		} {
			t.Run(fmt.Sprintf("explicit=%v/%d", explicit, tc.status), func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					expectedPath := "/accounts"
					if explicit {
						expectedPath = "/user/tokens/verify"
					}
					if r.URL.Path != expectedPath || r.Method != "GET" {
						t.Errorf("unexpected verification request: %s", r.URL.Path)
					}
					w.WriteHeader(tc.status)
					fmt.Fprintf(w, `{"success":false,"errors":[{"message":%q}]}`, fixtureToken)
				}))
				defer srv.Close()
				m := &Manager{Store: Store{Dir: filepath.Join(t.TempDir(), "workers")}, API: API{Base: srv.URL, HTTP: srv.Client()}}
				req := apps.WorkersConnectRequest{Token: fixtureToken}
				if explicit {
					req.AccountID = fixtureAccount
				}
				_, err := m.Connect(context.Background(), req)
				if connector.ErrorCode(err) != tc.code {
					t.Fatalf("code = %s, want %s", connector.ErrorCode(err), tc.code)
				}
				if strings.Contains(err.Error(), fixtureToken) {
					t.Fatal("error leaked credential")
				}
				if _, err := os.Stat(filepath.Join(m.Store.Dir, "state.json")); !os.IsNotExist(err) {
					t.Fatal("refused credential persisted")
				}
			})
		}
	}
}

func TestDeploymentBadRequestIsNotInvalidToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"success":false}`)
	}))
	defer srv.Close()
	a := API{Base: srv.URL, HTTP: srv.Client()}
	err := sanitize(a.call(context.Background(), fixtureToken, "PUT", "/accounts/"+fixtureAccount+"/workers/scripts/example", "", nil, nil))
	if connector.ErrorCode(err) != "workers_api_refused" {
		t.Fatalf("deployment code = %s", connector.ErrorCode(err))
	}
}

func TestExplicitAccountBypassesAmbiguousDiscovery(t *testing.T) {
	for _, accounts := range []string{`[]`, `[{"id":"0123456789abcdef0123456789abcdef"},{"id":"abcdef0123456789abcdef0123456789"}]`} {
		t.Run(accounts, func(t *testing.T) {
			listed := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				var result string
				switch r.URL.Path {
				case "/accounts":
					listed++
					result = accounts
				case "/user/tokens/verify":
					result = `{"status":"active"}`
				case "/accounts/" + fixtureAccount + "/workers/subdomain":
					result = `{"subdomain":"existing"}`
				default:
					t.Errorf("unexpected API request: %s", r.URL.Path)
					w.WriteHeader(403)
					return
				}
				fmt.Fprintf(w, `{"success":true,"result":%s}`, result)
			}))
			defer srv.Close()
			m := &Manager{Store: Store{Dir: filepath.Join(t.TempDir(), "workers")}, API: API{Base: srv.URL, HTTP: srv.Client()}}
			_, err := m.Connect(context.Background(), apps.WorkersConnectRequest{Token: fixtureToken})
			if connector.ErrorCode(err) != "workers_accounts_required" {
				t.Fatalf("ambiguous account code = %s", connector.ErrorCode(err))
			}
			status, err := m.Connect(context.Background(), apps.WorkersConnectRequest{Token: fixtureToken, AccountID: fixtureAccount})
			if err != nil || !status.Connected || listed != 1 {
				t.Fatalf("explicit account failed: connected=%v listed=%d error=%v", status.Connected, listed, err)
			}
		})
	}
}
