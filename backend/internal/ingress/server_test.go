//go:build linux

package ingress

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"dsmapp/internal/apps"
)

type peerFixtureRuntime struct{}

func (peerFixtureRuntime) Start(context.Context, apps.State) error       { return nil }
func (peerFixtureRuntime) Validate(context.Context, apps.Target) error   { return nil }
func (peerFixtureRuntime) PrepareDNS(context.Context, apps.Target) error { return nil }
func (peerFixtureRuntime) Activate(context.Context, apps.Target) error   { return nil }
func (peerFixtureRuntime) Block(context.Context, apps.Target) error      { return nil }
func (peerFixtureRuntime) Remove(context.Context, apps.Target) error     { return nil }
func (peerFixtureRuntime) Prune(context.Context, apps.State) error       { return nil }
func (peerFixtureRuntime) RequestIngress(ctx context.Context, subject apps.IngressSubject, in apps.IngressRequest) (apps.IngressStatus, error) {
	c, err := apps.DialIngress(ctx, subject.UID, "127.0.0.1:"+strconv.Itoa(in.Port))
	if err != nil {
		return apps.IngressStatus{}, err
	}
	defer c.Close()
	return apps.IngressStatus{ID: subject.Package + ":" + strconv.Itoa(subject.UID), State: "pending", ListenerHealthy: true}, nil
}
func (peerFixtureRuntime) StatusIngress(context.Context, apps.IngressSubject, apps.IngressRef) (apps.IngressStatus, error) {
	return apps.IngressStatus{}, nil
}
func (peerFixtureRuntime) RevokeIngress(context.Context, apps.IngressSubject, apps.IngressRef) (apps.IngressStatus, error) {
	return apps.IngressStatus{}, nil
}

// The disposable container harness runs this test in separate numeric UIDs.
// Only transport ownership is faked here; the App suite tests real approval,
// forwarding, durable revocation, restore and provider integration separately.
func TestIngressPeerFixture(t *testing.T) {
	mode := os.Getenv("CONNECT_INGRESS_PEER_FIXTURE")
	if mode == "" {
		t.Skip("requires disposable multi-user container harness")
	}
	const path = "/fixture/public/control.sock"
	ctx := context.Background()
	if mode == "server" {
		service := &apps.Service{Lifecycle: apps.Lifecycle{Runtime: peerFixtureRuntime{}}}
		foreign, err := net.Listen("tcp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer foreign.Close()
		if err := os.WriteFile("/fixture/foreign-port", []byte(strconv.Itoa(foreign.Addr().(*net.TCPAddr).Port)), 0644); err != nil {
			t.Fatal(err)
		}
		s, err := Start(ctx, path, service, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if err := os.WriteFile("/fixture/ready", []byte("ready"), 0644); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			if _, err := os.Stat("/fixture/done"); err == nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatal("peer fixture did not finish")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(io.Discard, c) }()
		}
	}()
	in := Request{Method: "request", Package: "syngle", Request: &apps.IngressRequest{Key: "reverse", Port: listener.Addr().(*net.TCPAddr).Port, Path: "/reverse/ws", Hostname: "reverse.example.com"}}
	if mode == "consumer" {
		out, err := Call(ctx, path, in)
		if err != nil || out.ID != "syngle:1001" || !out.ListenerHealthy {
			t.Fatalf("authenticated package request: %+v %v", out, err)
		}
		rawPort, err := os.ReadFile("/fixture/foreign-port")
		if err != nil {
			t.Fatal(err)
		}
		foreignPort, err := strconv.Atoi(string(rawPort))
		if err != nil {
			t.Fatal(err)
		}
		in.Request.Port = foreignPort
		if _, err := Call(ctx, path, in); err == nil {
			t.Fatal("another package listener admitted")
		}
		// The same UID cannot impersonate another installed package.
		in.Package = "OtherApp"
		if _, err := Call(ctx, path, in); err == nil {
			t.Fatal("package impersonation admitted")
		}
		// Bodies cannot choose their own UID or smuggle another operation.
		for _, raw := range []string{`{"method":"status","package":"syngle","uid":1000,"ref":{"id":"x"}}`, `{"method":"status","package":"syngle","ref":{"id":"x"}} {}`, `{"method":"status","package":"syngle","ref":{"id":"x"}}` + strings.Repeat(" ", 5000)} {
			c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: path, Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			c.SetDeadline(time.Now().Add(time.Second))
			c.Write([]byte(raw))
			c.CloseWrite()
			var response Response
			err = json.NewDecoder(c).Decode(&response)
			c.Close()
			if err != nil || response.Code != "bad_request" {
				t.Fatal("invalid frame admitted", response, err)
			}
		}
		return
	}
	if _, err := Call(ctx, path, in); err == nil {
		t.Fatal("root or unrelated UID admitted")
	}
}

func TestIngressSubjectRefusals(t *testing.T) {
	for _, pkg := range []string{"../syngle", "SynoPkgConnect", "missing", "syngle/target", "custom.fake"} {
		if _, err := ResolveSubject(t.TempDir(), pkg, 1001); err == nil {
			t.Fatal("invalid subject admitted", pkg)
		}
	}
	if _, err := ResolveSubject(t.TempDir(), "syngle", 0); err == nil {
		t.Fatal("root subject admitted")
	}
}
