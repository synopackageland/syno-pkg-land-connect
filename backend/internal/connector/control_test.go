package connector

import (
	"context"
	"dsmapp/internal/ipctest"
	"net/http"
	"path/filepath"
	"testing"
	"time"
)

func slowConnectServer(t *testing.T) string {
	t.Helper()
	socket := ipctest.Path(t, filepath.Join(t.TempDir(), "control.sock"))
	listener, e := ipctest.Listen(t, socket)
	if e != nil {
		t.Fatal(e)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(8200 * time.Millisecond):
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":{"provider":"tailscale","state":"needs-login"}}`))
	})}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })
	return socket
}

func TestConnectClientRetainsCallerCancellation(t *testing.T) {
	socket := slowConnectServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, e := (Client{Socket: socket}).Call(ctx, "connect", Request{Provider: "tailscale"}); e == nil {
		t.Fatal("cancellation ignored")
	}
	if time.Since(start) > time.Second {
		t.Fatal("cancellation not bounded")
	}
}
func TestDisconnectClientStaysWithinTheCGIDeadline(t *testing.T) {
	// connectd accepts a slow stop within Async.Accept, so a disconnect reply
	// that takes over 8 s means the daemon is stuck; with DSM authentication
	// on top, waiting longer would cross the 20 s CGI deadline.
	socket := slowConnectServer(t)
	start := time.Now()
	if _, e := (Client{Socket: socket}).Call(context.Background(), "disconnect", map[string]string{"provider": "tailscale"}); e == nil {
		t.Fatal("a stuck daemon must not hold the CGI")
	}
	if took := time.Since(start); took > 9*time.Second {
		t.Fatal("disconnect must give up like connect", took)
	}
}

func TestNameConflictDetailsSurviveControlClient(t *testing.T) {
	socket := ipctest.Path(t, filepath.Join(t.TempDir(), "conflict.sock"))
	listener, err := ipctest.Listen(t, socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"code":"cloudflare_name_taken","error":"name taken","conflict":{"reason":"leftover","suggestedName":"nas-2.example.com","conflictName":"nas.example.com","reuseRecord":"digest","dnsUrl":"https://dash.cloudflare.com/account/example.com/dns/records"}}`))
	})}
	go server.Serve(listener)
	defer server.Close()
	_, err = (Client{Socket: socket}).Call(context.Background(), "connect", Request{Provider: "cloudflare"})
	d := Conflict(err)
	if ErrorCode(err) != "cloudflare_name_taken" || d == nil || d.Reason != "leftover" || d.ReuseRecord != "digest" || d.SuggestedName != "nas-2.example.com" {
		t.Fatalf("details lost: %v %+v", err, d)
	}
}
