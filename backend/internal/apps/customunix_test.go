package apps

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"dsmapp/internal/ipctest"
	"github.com/gorilla/websocket"
	"github.com/synopackageland/syno-pkg-land-sdk/ipc"
)

func unixClient(socket string) *http.Client {
	return &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return ipc.DialUnix(ctx, socket)
		},
	}}
}

func TestCustomUnixStopClosesStreamsAndWebSocketsIndependently(t *testing.T) {
	for _, reason := range []string{"stop", "cancel", "revoke"} {
		for _, kind := range []string{"stream", "websocket"} {
			t.Run(reason+"/"+kind, func(t *testing.T) {
				ipctest.Require(t)
				upstreamDone := make(chan struct{})
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					defer close(upstreamDone)
					if kind == "stream" {
						w.Header().Set("Content-Type", "text/event-stream")
						io.WriteString(w, "data: ready\n\n")
						w.(http.Flusher).Flush()
						<-r.Context().Done()
						return
					}
					c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
					if err != nil {
						return
					}
					defer c.Close()
					for {
						if _, _, err := c.ReadMessage(); err != nil {
							return
						}
					}
				}))
				defer upstream.Close()
				state := &customActive{}
				state.on.Store(true)
				app := CustomApp{Name: "Media", URL: upstream.URL, AppSignIn: true}
				cfg := CustomConfig{App: app, State: state, Binding: PinBinding{
					Provider: Workers, Package: "custom.media", Target: TargetApp, Scope: Public,
					Socket: ipctest.ID(t), Port: app.Port(), Summary: app.Digest(),
					Authority: customAuthority, Mode: ModeCustom,
				}}
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				p, err := StartCustomProxy(ctx, cfg)
				if err != nil {
					t.Fatal(err)
				}
				defer p.Stop()
				socket := PinSocket(cfg.Binding.Socket)
				companion := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					io.WriteString(w, "still active")
				}))
				defer companion.Close()
				otherCfg := cfg
				otherState := &customActive{}
				otherState.on.Store(true)
				otherCfg.State = otherState
				otherCfg.Binding.Socket = ipctest.ID(t)
				otherCfg.App.URL = companion.URL
				otherCfg.Binding.Port = otherCfg.App.Port()
				otherCfg.Binding.Summary = otherCfg.App.Digest()
				other, err := StartCustomProxy(context.Background(), otherCfg)
				if err != nil {
					t.Fatal(err)
				}
				defer other.Stop()
				c, err := ipc.DialUnix(context.Background(), socket)
				if err != nil {
					t.Fatal(err)
				}
				defer c.Close()
				c.SetDeadline(time.Now().Add(3 * time.Second))
				upgrade := ""
				if kind == "websocket" {
					upgrade = "Upgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n"
				}
				io.WriteString(c, "GET /long HTTP/1.1\r\nHost: "+customAuthority+"\r\n"+upgrade+"\r\n")
				reader := bufio.NewReader(c)
				res, err := http.ReadResponse(reader, nil)
				if err != nil {
					t.Fatal(err)
				}
				if kind == "stream" {
					line, err := bufio.NewReader(res.Body).ReadString('\n')
					if err != nil || line != "data: ready\n" {
						t.Fatal("stream did not flush", line, err)
					}
				} else if res.StatusCode != http.StatusSwitchingProtocols {
					t.Fatal(res.StatusCode)
				}
				switch reason {
				case "stop":
					p.Stop()
				case "cancel":
					cancel()
				case "revoke":
					state.on.Store(false)
					p.Stop()
				}
				_, err = io.Copy(io.Discard, reader)
				var timeout net.Error
				if errors.As(err, &timeout) && timeout.Timeout() {
					t.Fatal("live connection survived withdrawal")
				}
				select {
				case <-upstreamDone:
				case <-time.After(3 * time.Second):
					t.Fatal("upstream survived withdrawal")
				}
				client := unixClient(PinSocket(otherCfg.Binding.Socket))
				defer client.CloseIdleConnections()
				req, _ := http.NewRequest("GET", "http://connect/", nil)
				req.Host = customAuthority
				// The other scope has an independent approval.
				reply, err := client.Do(req)
				if err != nil {
					t.Fatal("stopping one scope stopped another", err)
				}
				body, _ := io.ReadAll(reply.Body)
				reply.Body.Close()
				if strings.TrimSpace(string(body)) != "still active" {
					t.Fatal(string(body))
				}
			})
		}
	}
}
