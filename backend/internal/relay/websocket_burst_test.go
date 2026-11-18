package relay

import (
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestWebSocketBurstBeforeUpgrade(t *testing.T) {
	const count = 128
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	c, _, _ := fixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/ready" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		close(entered)
		<-release
		local, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer local.Close()
		_ = local.SetReadDeadline(time.Now().Add(5 * time.Second))
		for i := range count {
			kind, payload, err := local.ReadMessage()
			if err != nil {
				t.Errorf("read burst message %d: %v", i, err)
				return
			}
			if err := local.WriteMessage(kind, payload); err != nil {
				return
			}
		}
	}))
	protocols := []string{}
	start(t, c, 1, openMessage{Method: "GET", Path: "/ws", Websocket: &protocols})
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("local upgrade did not start")
	}
	for i := range count {
		send(t, c, textFrame, 1, []byte(fmt.Sprintf("message-%03d", i)))
	}
	start(t, c, 2, openMessage{Method: "GET", Path: "/ready"})
	kind, id, _ := receive(t, c)
	if kind != headFrame || id != 2 {
		t.Fatalf("burst rejected before barrier: kind=%d stream=%d", kind, id)
	}
	unblock()
	kind, id, _ = receive(t, c)
	for id == 2 {
		kind, id, _ = receive(t, c)
	}
	if kind != headFrame || id != 1 {
		t.Fatalf("burst reset before upgrade: kind=%d stream=%d", kind, id)
	}
	for i := range count {
		kind, id, payload := receive(t, c)
		if kind != textFrame || id != 1 || string(payload) != fmt.Sprintf("message-%03d", i) {
			t.Fatalf("burst message %d: kind=%d stream=%d payload=%q", i, kind, id, payload)
		}
	}
}
