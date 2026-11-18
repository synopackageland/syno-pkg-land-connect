package control

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"dsmapp/internal/apps"
	"dsmapp/internal/binaries"
	"dsmapp/internal/connector"
	"dsmapp/internal/ipctest"
	"dsmapp/internal/nbserver"
)

type readApps struct{ apps.API }

func (readApps) List(context.Context) (apps.List, error) { return apps.List{}, nil }

type controller struct{ stopped atomic.Bool }

func (*controller) Status() connector.Status { return connector.Status{State: "disconnected"} }
func (*controller) Connect(context.Context, connector.Request) (connector.Status, error) {
	return connector.Status{}, connector.ErrConnectBusy
}
func (*controller) Disconnect() (connector.Status, error) {
	return connector.Status{State: "disconnected"}, nil
}
func (c *controller) Stop() error { c.stopped.Store(true); return nil }

func TestSDKControlGraphKeepsRestoreBarrierAndAllFourHandlers(t *testing.T) {
	ipctest.Require(t)
	binary := binaries.NewService(binaries.New(t.TempDir(), nil), nil)
	defer binary.Close()
	netbird := nbserver.New(nbserver.Deps{Dir: t.TempDir(), Zone: func() (string, error) {
		return "", errors.New("fixture not configured")
	}})
	defer netbird.Close()
	owner := &controller{}
	restored := make(chan struct{})
	var changed atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	server, err := Start(ctx, Config{Apps: readApps{}, Binaries: binary, NetBird: netbird,
		Controller: owner, Shutdown: func() {}, Restored: restored, AfterApps: func() { changed.Add(1) }})
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if _, err := (apps.Client{Socket: Socket}).List(ctx); err != nil {
		t.Fatal("App read blocked during restore", err)
	}
	if _, err := (binaries.Client{Socket: Socket}).Call(ctx, "status", "tailscale"); err != nil {
		t.Fatal("binary status", err)
	}
	if result, err := (nbserver.Client{Socket: Socket}).Call(ctx, "status", nbserver.Request{}); err != nil || result.Status.State != "off" {
		t.Fatal("NetBird status", result, err)
	}
	client := connector.Client{Socket: Socket}
	if _, err := client.Call(ctx, "ping", nil); err != nil {
		t.Fatal("connector ping", err)
	}
	short, stop := context.WithTimeout(ctx, 30*time.Millisecond)
	_, err = client.Call(short, "disconnect", nil)
	stop()
	if err == nil {
		t.Fatal("mutation passed restore barrier")
	}
	close(restored)
	if _, err := client.Call(ctx, "disconnect", nil); err != nil {
		t.Fatal("restored mutation refused", err)
	}
	if _, err := client.Call(ctx, "shutdown", nil); err != nil || !owner.stopped.Load() {
		t.Fatal("shutdown did not stop the controller", err)
	}
	cancel()
	if err := server.Wait(); err != context.Canceled {
		t.Fatal("server did not stop", err)
	}
	if _, err := client.Call(context.Background(), "ping", nil); err == nil {
		t.Fatal("control socket survived cancellation")
	}
}
