package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestP5ProbeVerifiesDSMAndNeverFollowsRedirects(t *testing.T) {
	for _, tc := range []struct {
		body string
		code int
		pass bool
	}{{`{"data":{"SYNO.API.Auth":{"minVersion":1,"maxVersion":7,"path":"entry.cgi"}},"success":true}`, 200, true}, {`{"data":{"SYNO.API.Auth":{"minVersion":1,"maxVersion":7,"path":"auth.cgi"}},"success":true}`, 200, true}, {`{"success":true}`, 200, false}, {`welcome`, 200, false}, {strings.Repeat("x", 65537), 200, false}, {``, 302, false}} {
		t.Run(tc.body[:min(12, len(tc.body))], func(t *testing.T) {
			requests := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Path != "/webapi/query.cgi" || r.URL.Query().Get("query") != "SYNO.API.Auth" || r.URL.Query().Get("_connect_probe") == "" || !strings.Contains(r.Header.Get("Cache-Control"), "no-store") {
					t.Error("probe lost its identity or cache fence")
				}
				w.Header().Set("Location", "http://untrusted.invalid/")
				w.WriteHeader(tc.code)
				io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			e := probeTailscaleDSM(context.Background(), srv.URL)
			if (e == nil) != tc.pass {
				t.Fatal(e)
			}
			if requests != 1 {
				t.Fatal("redirect followed", requests)
			}
		})
	}
}
