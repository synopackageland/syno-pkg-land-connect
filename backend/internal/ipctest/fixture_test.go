package ipctest

import (
	"context"
	"os"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/ipc"
)

func TestFixtureUsesBoundSDKIdentityAndPrivateSocketMode(t *testing.T) {
	path := Path(t, t.TempDir())
	listener, err := Listen(t, path)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	conn, err := ipc.DialUnix(context.Background(), path)
	if err != nil {
		t.Fatalf("fixture must pass the production SDK checks: %v", err)
	}
	conn.Close()
	if err := os.Chmod(path, 0666); err != nil {
		t.Fatal(err)
	}
	if conn, err := ipc.DialUnix(context.Background(), path); err == nil {
		conn.Close()
		t.Fatal("SDK admitted a socket with broad permissions")
	}
}
