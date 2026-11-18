package apps

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
)

func TestPingCapabilitiesDistinguishUpdateFromWaiting(t *testing.T) {
	for _, test := range []struct {
		code       int
		body, want string
	}{
		{200, `{"package":"ExampleApp"}`, Ready},
		{200, `{"package":"OtherApp"}`, NeedsUpdate},
		{200, `{"package":"ExampleApp","extra":true}`, NeedsUpdate},
		{200, `{"package":"ExampleApp"} {}`, NeedsUpdate},
		{404, ``, NeedsUpdate}, {401, ``, NeedsUpdate}, {302, ``, NeedsUpdate},
		{502, ``, Waiting}, {503, ``, Waiting}, {504, ``, Waiting},
	} {
		t.Run(strconv.Itoa(test.code)+test.body, func(t *testing.T) {
			srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/_spl/ping" || r.Method != "GET" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
					t.Error("diagnostic sent application data")
				}
				w.Header().Set("Location", "https://example.com/")
				w.WriteHeader(test.code)
				_, _ = w.Write([]byte(test.body))
			}))
			defer srv.Close()
			u, _ := url.Parse(srv.URL)
			port, _ := strconv.Atoi(u.Port())
			if got := ProbePing(context.Background(), "ExampleApp", port); got != test.want {
				t.Fatalf("got %s want %s", got, test.want)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if got := ProbePing(ctx, "ExampleApp", 28443); got != Waiting {
		t.Fatal(got)
	}
}
