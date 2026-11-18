//go:build connect_entry_probe

package apps

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// Optional loopback-only browser fixture. It never publishes a hostname or
// changes NAS/package state. The browser supplies DSM sign-in separately.
func TestEntryLoopbackBrowserFixture(t *testing.T) {
	hint := os.Getenv("CONNECT_ENTRY_PROBE_HINT")
	pkg := os.Getenv("CONNECT_ENTRY_PROBE_PACKAGE")
	port, err := strconv.Atoi(os.Getenv("CONNECT_ENTRY_PROBE_PORT"))
	if hint == "" || !packagePattern.MatchString(pkg) || err != nil {
		t.Skip("local browser fixture not configured")
	}
	v := NewVerifier(Options{})
	chain, err := v.Probe(context.Background(), port)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	info, gateway := filepath.Join(dir, "INFO"), filepath.Join(dir, "gateway.json")
	os.WriteFile(info, []byte("package="+pkg), 0644)
	raw, _ := json.Marshal(map[string]any{"package": pkg, "native_ingress": true, "ingress_path": "/synopkgland/" + pkg + "/", "listen": map[string]int{}})
	os.WriteFile(gateway, raw, 0644)
	b := PinBinding{Package: pkg, Target: TargetApp, Authority: "app.example.test", Port: port, EntryPath: "/synopkgland/" + pkg + "/", Mode: ModeS, SPKI: SPKI(chain[0])}
	p, err := NewPinProxy(PinConfig{Binding: b, Provider: "cloudflared", InfoPath: info, GatewayJSON: gateway, State: &memoryPinState{active: true}, Verifier: v})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	server := httptest.NewTLSServer(p)
	defer server.Close()
	raw, _ = json.Marshal(map[string]string{"url": server.URL})
	if err := os.WriteFile(hint, raw, 0600); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(15 * time.Minute)
	defer deadline.Stop()
	for {
		if _, err := os.Stat(hint + ".stop"); err == nil {
			return
		}
		select {
		case <-deadline.C:
			t.Fatal("browser fixture deadline")
		case <-time.After(250 * time.Millisecond):
		}
	}
}
