package relay

import (
	"bytes"
	"context"
	"dsmapp/internal/ipctest"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

const testAuthority = "spl-app-abcd.example.workers.dev"

func fixture(t *testing.T, handler http.Handler) (*websocket.Conn, context.CancelFunc, <-chan error) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "relay-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := ipctest.Path(t, filepath.Join(dir, "pin.sock"))
	ln, err := ipctest.Listen(t, socket)
	if err != nil {
		t.Fatal(err)
	}
	local := &http.Server{Handler: handler}
	go local.Serve(ln)
	t.Cleanup(func() { local.Close() })
	accepted := make(chan *websocket.Conn, 1)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Hostc-Protocol") != "5" || r.Header.Get("Authorization") != "Bearer fixture" {
			t.Error("missing connector authentication")
		}
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		accepted <- c
	}))
	t.Cleanup(peer.Close)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Connect(ctx, Config{URL: "ws" + strings.TrimPrefix(peer.URL, "http"), Token: "fixture", Socket: socket, Authority: testAuthority})
	}()
	var c *websocket.Conn
	select {
	case c = <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("connect stalled")
	}
	t.Cleanup(func() { cancel(); c.Close() })
	return c, cancel, done
}
func send(t *testing.T, c *websocket.Conn, kind byte, id uint32, p []byte) {
	t.Helper()
	b := make([]byte, 5+len(p))
	b[0] = kind
	binary.BigEndian.PutUint32(b[1:5], id)
	copy(b[5:], p)
	if err := c.WriteMessage(websocket.BinaryMessage, b); err != nil {
		t.Fatal(err)
	}
}
func receive(t *testing.T, c *websocket.Conn) (byte, uint32, []byte) {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	kind, b, err := c.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if kind != websocket.BinaryMessage || len(b) < 5 {
		t.Fatalf("bad frame %v", b)
	}
	return b[0], binary.BigEndian.Uint32(b[1:5]), b[5:]
}
func start(t *testing.T, c *websocket.Conn, id uint32, op openMessage) {
	t.Helper()
	raw, _ := json.Marshal(op)
	send(t, c, openFrame, id, raw)
}
func TestHTTPFlowControlHostRangeAndIndependentStreams(t *testing.T) {
	body := bytes.Repeat([]byte("x"), window+chunk)
	c, cancel, done := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != testAuthority || r.Header.Get("X-Forwarded-Host") != "" || r.Header.Get("Forwarded") != "" {
			t.Error("untrusted Host metadata reached proxy")
		}
		w.Header().Add("Set-Cookie", "a=1")
		w.Header().Add("Set-Cookie", "b=2")
		if r.URL.Path == "/small" {
			w.Write([]byte("ok"))
			return
		}
		if r.URL.RawQuery != "q=1" {
			t.Error("query lost")
		}
		if r.Header.Get("Range") != "bytes=0-327679" {
			t.Error("range lost")
		}
		w.Header().Set("Content-Length", "327680")
		w.Header().Set("Content-Range", "bytes 0-327679/327680")
		w.WriteHeader(206)
		w.Write(body)
	}))
	start(t, c, 1, openMessage{Method: "GET", Path: "/large?q=1", Headers: [][2]string{{"Range", "bytes=0-327679"}, {"X-Forwarded-Host", "untrusted.example"}, {"Forwarded", "host=untrusted.example"}}})
	kind, id, p := receive(t, c)
	var head headMessage
	if kind != headFrame || id != 1 || json.Unmarshal(p, &head) != nil || head.Status != 206 {
		t.Fatal("missing HEAD")
	}
	h := http.Header{}
	for _, kv := range head.Headers {
		h.Add(kv[0], kv[1])
	}
	if h.Get("Content-Length") != "327680" || h.Get("Content-Range") == "" || len(h.Values("Set-Cookie")) != 2 {
		t.Fatal("lost response headers", h)
	}
	received := 0
	for received < window {
		k, i, p := receive(t, c)
		if k != dataFrame || i != 1 || len(p) > chunk {
			t.Fatal("bad flow")
		}
		received += len(p)
	}
	// The first stream has no remaining credit. Another stream must still progress.
	start(t, c, 2, openMessage{Method: "GET", Path: "/small"})
	for {
		k, i, _ := receive(t, c)
		if i != 2 {
			t.Fatal("sent without credit")
		}
		if k == endFrame {
			break
		}
	}
	var grant [4]byte
	binary.BigEndian.PutUint32(grant[:], 4*1024*1024)
	send(t, c, windowFrame, 1, grant[:])
	for {
		k, i, p := receive(t, c)
		if i != 1 {
			t.Fatal("wrong stream")
		}
		if k == endFrame {
			break
		}
		if k != dataFrame {
			t.Fatal("wrong body frame")
		}
		received += len(p)
	}
	if received != len(body) {
		t.Fatal(received)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("session cancellation stalled")
	}
}
func TestStreamingUploadAndReset(t *testing.T) {
	seen := make(chan int, 1)
	aborted := make(chan struct{})
	c, _, _ := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/cancel" {
			<-r.Context().Done()
			close(aborted)
			return
		}
		b, err := io.ReadAll(r.Body)
		if err != nil {
			return
		}
		seen <- len(b)
		w.Write([]byte("saved"))
	}))
	start(t, c, 1, openMessage{Method: "POST", Path: "/upload", Body: true})
	payload := bytes.Repeat([]byte("a"), chunk)
	for range 4 {
		send(t, c, dataFrame, 1, payload)
	}
	granted := 0
	for granted < chunk {
		k, id, p := receive(t, c)
		if k != windowFrame || id != 1 {
			t.Fatal("body was not consumed before END")
		}
		granted += int(binary.BigEndian.Uint32(p))
	}
	send(t, c, dataFrame, 1, payload)
	send(t, c, endFrame, 1, nil)
	select {
	case n := <-seen:
		if n != 5*chunk {
			t.Fatal(n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("upload stalled")
	}
	for {
		k, _, _ := receive(t, c)
		if k == endFrame {
			break
		}
	}
	start(t, c, 2, openMessage{Method: "GET", Path: "/cancel"})
	time.Sleep(20 * time.Millisecond)
	send(t, c, resetFrame, 2, []byte("visitor canceled"))
	select {
	case <-aborted:
	case <-time.After(time.Second):
		t.Fatal("RESET did not cancel local request")
	}
}
func TestWebSocketSubprotocolEchoAndRejection(t *testing.T) {
	c, _, _ := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/reject" {
			w.Header().Set("Set-Cookie", "denied=1")
			w.WriteHeader(403)
			return
		}
		if r.Host != testAuthority || r.Header.Get("Origin") != "https://"+testAuthority {
			t.Error("WS origin/host lost")
		}
		local, err := (&websocket.Upgrader{Subprotocols: []string{"echo"}, CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer local.Close()
		for {
			kind, p, err := local.ReadMessage()
			if err != nil {
				return
			}
			if local.WriteMessage(kind, p) != nil {
				return
			}
		}
	}))
	protocols := []string{"echo"}
	start(t, c, 1, openMessage{Method: "GET", Path: "/ws", Websocket: &protocols, Headers: [][2]string{{"Origin", "https://" + testAuthority}}})
	k, _, p := receive(t, c)
	var h headMessage
	json.Unmarshal(p, &h)
	if k != headFrame || h.Status != 101 || h.Protocol != "echo" {
		t.Fatal(string(p))
	}
	send(t, c, textFrame, 1, []byte("ping"))
	k, _, p = receive(t, c)
	if k != textFrame || string(p) != "ping" {
		t.Fatal("text lost")
	}
	send(t, c, dataFrame, 1, []byte{0, 1, 2})
	k, _, p = receive(t, c)
	if k != dataFrame || !bytes.Equal(p, []byte{0, 1, 2}) {
		t.Fatal("binary lost")
	}
	send(t, c, endFrame, 1, []byte{3, 232})
	k, _, _ = receive(t, c)
	if k != endFrame {
		t.Fatal("close lost")
	}
	start(t, c, 2, openMessage{Method: "GET", Path: "/reject", Websocket: &protocols})
	k, _, p = receive(t, c)
	json.Unmarshal(p, &h)
	if k != headFrame || h.Status != 403 {
		t.Fatal(string(p))
	}
}
func TestMalformedCreditTerminatesSession(t *testing.T) {
	c, _, done := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	start(t, c, 1, openMessage{Method: "GET", Path: "/"})
	send(t, c, windowFrame, 1, []byte{0, 0, 0, 0})
	select {
	case err := <-done:
		if err != errProtocol {
			t.Fatal(err)
		}
		_, _, readErr := c.ReadMessage()
		var closed *websocket.CloseError
		if !errors.As(readErr, &closed) || closed.Code != websocket.CloseProtocolError {
			t.Fatal("protocol close code lost")
		}
	case <-time.After(time.Second):
		t.Fatal("bad window accepted")
	}
}
func TestInvalidOpenPathResetsOnlyThatStream(t *testing.T) {
	c, _, done := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) }))
	start(t, c, 1, openMessage{Method: "GET", Path: "/%zz"})
	if k, id, _ := receive(t, c); k != resetFrame || id != 1 {
		t.Fatal("invalid path was not refused per stream")
	}
	start(t, c, 2, openMessage{Method: "GET", Path: "/small"})
	if k, id, _ := receive(t, c); k != headFrame || id != 2 {
		t.Fatal("session did not survive an invalid path")
	}
	select {
	case err := <-done:
		t.Fatal(err)
	default:
	}
}
func TestResetClosesStalledWebSocketUpgrade(t *testing.T) {
	started := make(chan struct{})
	closed := make(chan struct{})
	c, _, _ := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer close(closed)
		defer conn.Close()
		close(started)
		io.Copy(io.Discard, conn)
	}))
	protocols := []string{"echo"}
	start(t, c, 1, openMessage{Method: "GET", Path: "/stall", Websocket: &protocols})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("upgrade did not reach the local App")
	}
	send(t, c, resetFrame, 1, []byte("visitor canceled"))
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("RESET left the local upgrade handshake open")
	}
}

// Account credentials and a listening proxy are not evidence of a live relay.
func TestConnectionStatusFollowsWebSocketLifetime(t *testing.T) {
	accepted := make(chan *websocket.Conn, 1)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err == nil {
			accepted <- c
		}
	}))
	defer peer.Close()
	changes := make(chan bool, 4)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- Connect(ctx, Config{URL: "ws" + strings.TrimPrefix(peer.URL, "http"), Connected: func(v bool) { changes <- v }})
	}()
	c := <-accepted
	select {
	case v := <-changes:
		if !v {
			t.Fatal("not connected after handshake")
		}
	case <-time.After(time.Second):
		t.Fatal("missing connected status")
	}
	c.Close()
	select {
	case v := <-changes:
		if v {
			t.Fatal("stale connected status")
		}
	case <-time.After(time.Second):
		t.Fatal("missing disconnection")
	}
	<-done
}
