package apps

import (
	"context"
	"net"
	"os"
	"strconv"
	"testing"
)

func TestIngressDialVerifiesAcceptedSocketOwner(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires package-account container")
	}
	l, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		c, err := l.Accept()
		if err == nil {
			defer c.Close()
			var b [1]byte
			c.Read(b[:])
		}
	}()
	port := l.Addr().(*net.TCPAddr).Port
	if err := CheckIngressListener(os.Geteuid(), port); err != nil {
		t.Fatal(err)
	}
	if err := CheckIngressListener(os.Geteuid()+1, port); err == nil {
		t.Fatal("foreign owner accepted")
	}
	c, err := DialIngress(context.Background(), os.Geteuid(), "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatal(err)
	}
	if !establishedOwner(os.Geteuid(), c.RemoteAddr().(*net.TCPAddr).AddrPort(), c.LocalAddr().(*net.TCPAddr).AddrPort()) || establishedOwner(os.Geteuid()+1, c.RemoteAddr().(*net.TCPAddr).AddrPort(), c.LocalAddr().(*net.TCPAddr).AddrPort()) {
		t.Fatal("accepted server socket UID mismatch")
	}
	c.Close()
	if _, err := DialIngress(context.Background(), os.Geteuid()+1, l.Addr().String()); err == nil {
		t.Fatal("foreign socket dialed")
	}
	if _, err := DialIngress(context.Background(), os.Geteuid(), "localhost:"+strconv.Itoa(port)); err == nil {
		t.Fatal("DNS target admitted")
	}
	if err := CheckIngressListener(0, port); err == nil {
		t.Fatal("root listener admitted")
	}
	wildcard, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	defer wildcard.Close()
	if err := CheckIngressListener(os.Geteuid(), wildcard.Addr().(*net.TCPAddr).Port); err == nil {
		t.Fatal("wildcard listener admitted")
	}
}
