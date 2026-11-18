package appaccess

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"dsmapp/internal/apps"
	"dsmapp/internal/cfaccess"
	"dsmapp/internal/connector"
	"dsmapp/internal/ipctest"
	"github.com/gorilla/websocket"
	"github.com/synopackageland/syno-pkg-land-sdk/ipc"
)

func TestIngressGrantApprovalRevocationAndConnectorRecovery(t *testing.T) {
	ipctest.Require(t)
	f := newFixture(t, false)
	f.owner.cfg.IngressIdentity = func(string, int) error { return nil }
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/reverse/ws" {
			t.Error("unexpected upstream path", r.URL.Path)
		}
		c, err := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			kind, data, err := c.ReadMessage()
			if err != nil {
				return
			}
			if c.WriteMessage(kind, data) != nil {
				return
			}
		}
	}))
	t.Cleanup(upstream.Close)
	_, portText, _ := net.SplitHostPort(strings.TrimPrefix(upstream.URL, "http://"))
	port, _ := strconv.Atoi(portText)
	subject := apps.IngressSubject{Package: "syngle", UID: os.Geteuid()}
	in := apps.IngressRequest{Key: "reverse", Port: port, Path: "/reverse/ws", Hostname: "reverse.example.com"}
	ctx := context.Background()
	var connectorState atomic.Value
	connectorState.Store("connected")
	f.owner.cfg.IngressConnector = func() connector.Status { return connector.Status{State: connectorState.Load().(string)} }
	out, err := f.owner.RequestIngress(ctx, subject, in)
	if err != nil || out.State != "pending" || out.URL != "" || !out.ListenerHealthy {
		t.Fatalf("pending: %+v %v", out, err)
	}
	ref := apps.IngressRef{ID: out.ID}
	if f.owner.InUse() {
		t.Fatal("pending grant enabled safety polling")
	}
	if repeat, err := f.owner.RequestIngress(ctx, subject, in); err != nil || repeat.ID != out.ID {
		t.Fatal("request not idempotent", repeat, err)
	}
	changed := in
	changed.Port++
	if _, err := f.owner.RequestIngress(ctx, subject, changed); err == nil {
		t.Fatal("same key changed target")
	}
	foreign := subject
	foreign.UID++
	if _, err := f.owner.StatusIngress(ctx, foreign, ref); err == nil {
		t.Fatal("another UID read grant")
	}
	foreign = subject
	foreign.Package = "OtherApp"
	if _, err := f.owner.RevokeIngress(ctx, foreign, ref); err == nil {
		t.Fatal("another package revoked grant")
	}
	if _, err := f.api.Inspect(ctx, apps.InspectRequest{Package: out.ID, Scope: apps.Public, Provider: apps.Workers}); err == nil {
		t.Fatal("free link machine ingress admitted")
	}
	if _, err := f.api.Inspect(ctx, apps.InspectRequest{Package: out.ID, Scope: apps.Public, Hostname: "other.example.com"}); err == nil {
		t.Fatal("hostname changed during approval")
	}
	inspection, err := f.api.Inspect(ctx, apps.InspectRequest{Package: out.ID, Scope: apps.Public})
	if err != nil || inspection.DSM != nil || inspection.Hostname != in.Hostname {
		t.Fatalf("implicit DSM publication: %+v %v", inspection, err)
	}
	approveCustom(t, f, out.ID, "")
	state, _ := f.store.Load()
	scope := state.Apps[out.ID].Public
	active, err := f.owner.StatusIngress(ctx, subject, ref)
	if err != nil || !active.Ready {
		t.Fatal("active grant not locally ready", active, err)
	}
	if state.DSM != nil || !f.owner.IngressOnly() {
		t.Fatal("grant published DSM or failed machine-only mode")
	}
	routes, _ := f.owner.ActivePublic(ctx)
	if len(routes) != 1 || routes[0].Path != in.Path {
		t.Fatal("missing edge path", routes)
	}
	if _, err := f.api.(apps.CustomAPI).CustomEdit(ctx, apps.CustomRequest{ID: out.ID, Name: "changed", URL: upstream.URL, AppSignIn: true, AcceptRisk: true}); err == nil {
		t.Fatal("ordinary custom edit widened grant")
	}
	// An email gate cannot be enabled around a noninteractive machine grant.
	inventory, _ := f.owner.AccessInventory()
	if !inventory.Unsupported {
		t.Fatal("Access gate accepted machine grant")
	}
	access := cfaccess.New(cfaccess.Config{Path: f.store.Dir + "/access.json", Endpoints: f.owner.AccessInventory})
	defer access.Close()
	if _, err := access.Apply(ctx, cfaccess.Request{Emails: []string{"admin@example.com"}}); err == nil {
		t.Fatal("email gate enabled")
	}

	dial := func() *websocket.Conn {
		t.Helper()
		d := websocket.Dialer{HandshakeTimeout: time.Second, NetDialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return ipc.DialUnix(ctx, apps.PinSocket(scope.Socket))
		}}
		c, _, err := d.Dial("ws://"+in.Hostname+in.Path, nil)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { c.Close() })
		return c
	}
	c := dial()
	if err := c.WriteMessage(websocket.BinaryMessage, []byte("ssh payload")); err != nil {
		t.Fatal(err)
	}
	_, body, err := c.ReadMessage()
	if err != nil || string(body) != "ssh payload" {
		t.Fatal(string(body), err)
	}
	// Refused paths and hosts never reach the listener, even with WS headers.
	client := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return ipc.DialUnix(ctx, apps.PinSocket(scope.Socket))
	}}}
	defer client.CloseIdleConnections()
	for _, path := range []string{"/", "/webapi/entry.cgi", "/reverse/ws?port=22", "/reverse/ws/", "/reverse/%77s"} {
		req, _ := http.NewRequest("GET", "http://"+in.Hostname+path, nil)
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Connection", "Upgrade")
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 404 {
			t.Fatal(path, res.StatusCode)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("refused request reached listener", calls.Load())
	}
	// The connector's existing supervisor retains the grant; no second grant
	// or DNS name is created as transport availability changes.
	connectorState.Store("reconnecting")
	out, _ = f.owner.StatusIngress(ctx, subject, ref)
	if out.Ready || out.ConnectorState != "reconnecting" || out.State != apps.Active || out.ExternalVerified {
		t.Fatal(out)
	}
	c.Close()
	connectorState.Store("connected")
	c = dial()
	if _, err = f.owner.RevokeIngress(ctx, subject, ref); err != nil {
		t.Fatal(err)
	}
	c.SetReadDeadline(time.Now().Add(time.Second))
	if _, _, err = c.ReadMessage(); err == nil {
		t.Fatal("revoked stream survived")
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("revoke did not close stream")
	}
	if out, err = f.owner.RequestIngress(ctx, subject, in); err != nil || out.State != "revoked" || out.URL != "" {
		t.Fatal("request resurrected grant", out, err)
	}
	if _, err = f.owner.RevokeIngress(ctx, subject, ref); err != nil {
		t.Fatal("repeat revoke", err)
	}
	if f.owner.InUse() {
		t.Fatal("revoked grant retained polling demand")
	}
	if _, err = f.api.Inspect(ctx, apps.InspectRequest{Package: out.ID, Scope: apps.Public}); err == nil {
		t.Fatal("revoked grant offered approval")
	}
	// A fresh owner restores the tombstone and refuses any old binding.
	restored := New(ctx, Config{Store: f.store})
	defer restored.Close()
	if out, err = restored.StatusIngress(ctx, subject, ref); err != nil || out.State != "revoked" {
		t.Fatal(out, err)
	}
}

func TestIngressCancelBeforeAdmissionKeepsTombstone(t *testing.T) {
	f := newFixture(t, false)
	s := apps.IngressSubject{Package: "syngle", UID: 1001}
	ref := apps.IngressRef{Key: "reverse-delayed"}
	out, err := f.owner.RevokeIngress(context.Background(), s, ref)
	if err != nil || out.State != "revoked" {
		t.Fatalf("%+v %v", out, err)
	}
	in := apps.IngressRequest{Key: ref.Key, Socket: "/var/packages/syngle/var/ingress-reverse/http.sock", Path: "/reverse", Hostname: "reverse.example.com", AdditionalPaths: []string{"/reverse/enroll"}}
	out, err = f.owner.RequestIngress(context.Background(), s, in)
	if err != nil || out.State != "revoked" || out.Ready {
		t.Fatalf("late admission revived: %+v %v", out, err)
	}
	restored := New(context.Background(), f.owner.cfg)
	defer restored.Close()
	out, err = restored.StatusIngress(context.Background(), s, ref)
	if err != nil || out.State != "revoked" {
		t.Fatalf("lost tombstone: %+v %v", out, err)
	}
}
