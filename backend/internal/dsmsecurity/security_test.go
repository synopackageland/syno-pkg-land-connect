package dsmsecurity

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func serve(t *testing.T, replies map[string]string) *httptest.Server {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if r.Method != http.MethodGet || q.Get("method") != "get" || q.Get("version") != "1" {
			t.Errorf("not a read: %s %v", r.Method, q)
		}
		if r.Header.Get("Cookie") != "id=fixture" || r.Header.Get("X-SYNO-TOKEN") != "fixture-token" {
			t.Errorf("missing session: %v", r.Header)
		}
		reply, ok := replies[q.Get("api")]
		if !ok {
			t.Errorf("unexpected API %s", q.Get("api"))
			reply = `{"success":false,"error":{"code":102}}`
		}
		w.Write([]byte(reply))
	}))
	t.Cleanup(server.Close)
	return server
}

func TestReadsAutoBlockAndEnforcedTwoFactor(t *testing.T) {
	cases := []struct {
		autoBlock, otp string
		want           Status
	}{
		{`{"success":true,"data":{"enable":true,"attempts":10}}`, `{"success":true,"data":{"otp_enforce_option":"admin"}}`, Status{AutoBlock: On, TwoFactor: On}},
		{`{"success":true,"data":{"enable":false}}`, `{"success":true,"data":{"otp_enforce_option":"none"}}`, Status{AutoBlock: Off, TwoFactor: Off}},
		// "user" is everyone; "custom" is chosen users and groups: enforced either way.
		{`{"success":true,"data":{"enable":true}}`, `{"success":true,"data":{"otp_enforce_option":"user"}}`, Status{AutoBlock: On, TwoFactor: On}},
		{`{"success":true,"data":{"enable":true}}`, `{"success":true,"data":{"otp_enforce_option":"custom"}}`, Status{AutoBlock: On, TwoFactor: On}},
	}
	for _, c := range cases {
		server := serve(t, map[string]string{"SYNO.Core.Security.AutoBlock": c.autoBlock, "SYNO.Core.OTP.EnforcePolicy": c.otp})
		if got := Read(context.Background(), server.URL, "id=fixture", "fixture-token"); got != c.want {
			t.Fatalf("%s %s: got %+v, want %+v", c.autoBlock, c.otp, got, c.want)
		}
	}
}

func TestNoPermissionOrAnUnknownAnswerIsUnknownNotAnError(t *testing.T) {
	server := serve(t, map[string]string{
		"SYNO.Core.Security.AutoBlock": `{"success":false,"error":{"code":105}}`,
		"SYNO.Core.OTP.EnforcePolicy":  `{"success":true,"data":{"otp_enforce_option":"later"}}`,
	})
	if got := Read(context.Background(), server.URL, "id=fixture", "fixture-token"); got != (Status{AutoBlock: Unknown, TwoFactor: Unknown}) {
		t.Fatalf("got %+v", got)
	}
	if got := Read(context.Background(), "http://127.0.0.1:1", "id=fixture", "fixture-token"); got != (Status{AutoBlock: Unknown, TwoFactor: Unknown}) {
		t.Fatalf("unreachable DSM: %+v", got)
	}
}
