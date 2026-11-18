package apps

import (
	"context"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestPackageUnixIngressDefinitionAndExactPaths(t *testing.T) {
	g := IngressGrant{Package: "syngle", UID: 1001, Path: "/reverse", AdditionalPaths: []string{"/reverse/enroll"}, Hostname: "reverse.example.com", Socket: "/var/packages/syngle/var/ingress-reverse/http.sock"}
	c := CustomApp{Name: "Reverse", URL: "http://package.invalid", AppSignIn: true, Ingress: &g}
	if !c.valid() {
		t.Fatal("package Unix definition refused")
	}
	original := c.Digest()
	g.AdditionalPaths = nil
	if c.Digest() == original {
		t.Fatal("extra paths absent from approval digest")
	}
	g.AdditionalPaths = []string{"/reverse/enroll"}
	for _, path := range []string{"/reverse", "/reverse/enroll", "/reverse/other", "/webapi/entry.cgi", "/reverse/enroll?x", "/reverse/%65nroll"} {
		r := httptest.NewRequest("GET", path, nil)
		r.Header.Set("Upgrade", "websocket")
		r.Header.Set("Connection", "Upgrade")
		if got := g.Accepts(r); got != (path == "/reverse" || path == "/reverse/enroll") {
			t.Errorf("%s: %v", path, got)
		}
	}
	for _, path := range []string{"/var/packages/Other/var/ingress-reverse/http.sock", "/var/packages/syngle/var/run/worker.sock", "/tmp/http.sock", "/var/packages/syngle/var/ingress-../http.sock"} {
		g.Socket = path
		if c.valid() {
			t.Fatalf("unsafe socket: %s", path)
		}
	}
}

func TestDialPackageUnixAuthenticatesServerBeforeBytes(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("nonroot gate")
	}
	path := filepath.Join(t.TempDir(), "http.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, e := l.Accept()
		if e == nil {
			defer c.Close()
			b := make([]byte, 1)
			n, _ := c.Read(b)
			if n != 0 {
				t.Error("bytes sent to foreign UID")
			}
		}
	}()
	if c, e := DialIngressUnix(context.Background(), os.Geteuid()+1, path); e == nil {
		c.Close()
		t.Fatal("foreign server admitted")
	}
	l.Close()
	<-done
}
