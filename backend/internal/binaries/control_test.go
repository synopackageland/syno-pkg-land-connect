package binaries

import (
	"context"
	"dsmapp/internal/ipctest"
	"errors"
	"net/http"
	"path/filepath"
	"testing"
)

func TestUnixControlSubmitsAndPolls(t *testing.T) {
	dir := t.TempDir()
	socket := ipctest.Path(t, filepath.Join(dir, "control.sock"))
	ln, e := ipctest.Listen(t, socket)
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	source := blockingSource{make(chan struct{}, 1), make(chan struct{})}
	svc := NewService(New(t.TempDir(), source), nil)
	defer svc.Close()
	server := &http.Server{Handler: svc}
	go server.Serve(ln)
	defer server.Close()
	client := Client{Socket: socket}
	status, e := client.Call(context.Background(), "update", "cloudflared")
	if e != nil || !status.Busy {
		t.Fatal(status, e)
	}
	close(source.release)
	waitService(t, svc, "cloudflared")
	status, e = client.Call(context.Background(), "status", "cloudflared")
	if e != nil || status.Current != "1.0.0" {
		t.Fatal(status, e)
	}
}
func TestUnixControlKeepsNoPreviousRefusal(t *testing.T) {
	dir := t.TempDir()
	socket := ipctest.Path(t, filepath.Join(dir, "control.sock"))
	ln, e := ipctest.Listen(t, socket)
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	svc := NewService(New(t.TempDir(), blockingSource{make(chan struct{}, 1), make(chan struct{})}), nil)
	defer svc.Close()
	server := &http.Server{Handler: svc}
	go server.Serve(ln)
	defer server.Close()
	if _, e = (Client{Socket: socket}).Call(context.Background(), "rollback", "tailscale"); !errors.Is(e, ErrNoPrevious) {
		t.Fatal(e)
	}
}
