// Package ipctest supports Linux-only transport fixtures. Shipped entries do
// not import it; every dial still uses the production SDK ownership checks.
package ipctest

import (
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"dsmapp/internal/localipc"
)

func Require(t testing.TB) {
	t.Helper()
	if runtime.GOOS != "linux" || os.Geteuid() == 0 {
		t.Skip("private IPC fixture requires a disposable Linux package-account container")
	}
	st, err := os.Stat(localipc.RunDir)
	if err != nil || !st.IsDir() {
		t.Fatal("prepare package var/run for the IPC fixture")
	}
}

func ID(t testing.TB) string {
	t.Helper()
	Require(t)
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("%x", b[:])
}

func Path(t testing.TB, identity string) string {
	t.Helper()
	Require(t)
	sum := sha256.Sum256([]byte(identity))
	return filepath.Join(localipc.RunDir, fmt.Sprintf("test-%x.sock", sum[:16]))
}

// Listen is for a fake upstream/control peer, not the production server under
// test. Its exact mode lets the production DialUnix authenticate the fixture.
func Listen(t testing.TB, path string) (net.Listener, error) {
	t.Helper()
	Require(t)
	l, err := net.Listen("unix", path)
	if err == nil {
		if err = os.Chmod(path, 0600); err != nil {
			l.Close()
			return nil, err
		}
	}
	return l, err
}
