package workers

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"dsmapp/internal/connector"
	"dsmapp/internal/relay"
	"dsmapp/internal/relayworker"
	"github.com/gorilla/websocket"
)

// This opt-in check uses only a fresh thread-prefixed Worker and always deletes
// it. Credentials enter through the environment, never argv, URLs or test logs.
func TestLiveCloudflareRelay(t *testing.T) {
	if os.Getenv("CONNECT_WORKERS_LIVE") != "1" {
		t.Skip("isolated Cloudflare test is opt-in")
	}
	name := os.Getenv("CONNECT_WORKERS_TEST_NAME")
	if !regexp.MustCompile(`^t[0-9]+-[a-z0-9-]{1,40}$`).MatchString(name) {
		t.Fatal("an isolated test Worker name is required")
	}
	token, account := os.Getenv("CLOUDFLARE_API_TOKEN"), os.Getenv("CLOUDFLARE_ACCOUNT_ID")
	if !validToken(token) || !accountPattern.MatchString(account) {
		t.Fatal("test credentials unavailable")
	}
	script := []byte(relayworker.Script())
	api := API{Script: script}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	sub, err := api.validate(ctx, token, account)
	if err != nil || sub == "" {
		t.Fatal("test account requires a working token and existing subdomain")
	}
	owner, _ := randomHex(16)
	secret, _ := randomHex(32)
	state := privateState{AccountID: account, Token: token, Owner: owner, Subdomain: sub}
	r := Record{Name: name, Secret: secret}
	owned, err := api.owned(ctx, state, r)
	if err != nil || owned {
		t.Fatal("test name already exists or ownership unavailable")
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if api.delete(cleanup, state, r) != nil {
			t.Error("isolated Worker cleanup failed")
		}
		var out any
		err := api.json(cleanup, state, "GET", "/workers/scripts/"+name+"/settings", nil, &out)
		if !missing(err) {
			t.Error("Worker deletion readback failed")
		}
	}()
	if err = api.deploy(ctx, state, r); err != nil {
		t.Fatalf("isolated Worker deployment failed (%s)", connector.ErrorCode(err))
	}
	authority := name + "." + sub + ".workers.dev"
	origin := "https://" + authority
	payload := bytes.Repeat([]byte("stream-range-"), 700000)
	directory, err := os.MkdirTemp("/tmp", "relay-live-")
	if err != nil {
		t.Fatal("local fixture unavailable")
	}
	defer os.RemoveAll(directory)
	socket := filepath.Join(directory, "pin.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal("local socket unavailable")
	}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != authority {
			w.WriteHeader(421)
			return
		}
		switch r.URL.Path {
		case "/ws":
			c, err := (&websocket.Upgrader{Subprotocols: []string{"echo"}, CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer c.Close()
			for {
				kind, p, err := c.ReadMessage()
				if err != nil {
					return
				}
				if c.WriteMessage(kind, p) != nil {
					return
				}
			}
		case "/blob":
			http.ServeContent(w, r, "blob", time.Time{}, bytes.NewReader(payload))
		case "/events":
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-transform")
			fmt.Fprint(w, "data: first\n\n")
			w.(http.Flusher).Flush()
			time.Sleep(100 * time.Millisecond)
			fmt.Fprint(w, "data: second\n\n")
		default:
			b, _ := io.ReadAll(r.Body)
			w.Header().Add("Set-Cookie", "a=1")
			w.Header().Add("Set-Cookie", "b=2")
			w.Write(b)
		}
	})}
	go server.Serve(ln)
	defer server.Close()
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte("connect:default"))
	bearer := base64.RawURLEncoding.EncodeToString(h.Sum(nil))
	relayCtx, stopRelay := context.WithCancel(ctx)
	relayDone := make(chan struct{})
	go func() {
		defer close(relayDone)
		relay.Run(relayCtx, relay.Config{URL: "wss://" + authority + "/_spl/relay/connect", Token: bearer, Socket: socket, Authority: authority})
	}()
	defer func() {
		stopRelay()
		select {
		case <-relayDone:
		case <-time.After(5 * time.Second):
			t.Error("relay shutdown stalled")
		}
	}()
	client := &http.Client{Timeout: 2 * time.Minute, Transport: &http.Transport{DisableCompression: true}}
	ready := false
	for attempt := 0; attempt < 60; attempt++ {
		req, _ := http.NewRequestWithContext(ctx, "POST", origin+"/echo?probe=1", strings.NewReader("ready"))
		res, err := client.Do(req)
		if err == nil {
			body, _ := io.ReadAll(res.Body)
			res.Body.Close()
			if res.StatusCode == 200 && string(body) == "ready" {
				if len(res.Header.Values("Set-Cookie")) != 2 {
					t.Fatal("repeated cookies lost")
				}
				ready = true
				break
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("relay readiness deadline")
		case <-time.After(time.Second):
		}
	}
	if !ready {
		t.Fatal("relay never became ready")
	}
	for _, bounds := range []string{"", "bytes=123-900122"} {
		req, _ := http.NewRequestWithContext(ctx, "GET", origin+"/blob", nil)
		req.Header.Set("Accept-Encoding", "identity")
		if bounds != "" {
			req.Header.Set("Range", bounds)
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal("download unavailable")
		}
		body, err := io.ReadAll(res.Body)
		res.Body.Close()
		expected := payload
		status := 200
		if bounds != "" {
			expected = payload[123:900123]
			status = 206
		}
		if err != nil || res.StatusCode != status || !bytes.Equal(body, expected) || res.ContentLength != int64(len(expected)) {
			t.Fatalf("download mismatch: status=%d bytes=%d expected=%d contentLength=%d uncompressed=%v readFailed=%v", res.StatusCode, len(body), len(expected), res.ContentLength, res.Uncompressed, err != nil)
		}
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", origin+"/events", nil)
	res, err := client.Do(req)
	if err != nil {
		t.Fatal("SSE unavailable")
	}
	body, err := io.ReadAll(res.Body)
	res.Body.Close()
	if err != nil || !bytes.Contains(body, []byte("data: second")) {
		t.Fatal("SSE truncated")
	}
	d := websocket.Dialer{Subprotocols: []string{"echo"}, HandshakeTimeout: time.Minute}
	u, _ := url.Parse(origin + "/ws")
	u.Scheme = "wss"
	ws, _, err := d.DialContext(ctx, u.String(), http.Header{"Origin": []string{origin}})
	if err != nil {
		t.Fatal("public WebSocket unavailable")
	}
	defer ws.Close()
	if ws.Subprotocol() != "echo" {
		t.Fatal("WS subprotocol lost")
	}
	for _, kind := range []int{websocket.TextMessage, websocket.BinaryMessage} {
		if ws.WriteMessage(kind, []byte("ping")) != nil {
			t.Fatal("WS send failed")
		}
		_ = ws.SetReadDeadline(time.Now().Add(time.Minute))
		got, body, err := ws.ReadMessage()
		if err != nil || got != kind || string(body) != "ping" {
			t.Fatal("WS echo failed")
		}
	}
	// Invalid auth must fail at the reserved connector endpoint.
	_, denied, err := d.DialContext(ctx, "wss://"+authority+"/_spl/relay/connect", http.Header{"Hostc-Protocol": []string{"5"}, "Authorization": []string{"Bearer invalid"}})
	if denied != nil && denied.Body != nil {
		denied.Body.Close()
	}
	if err == nil || denied == nil || denied.StatusCode != 401 {
		t.Fatal("invalid connector credential accepted")
	}
}
