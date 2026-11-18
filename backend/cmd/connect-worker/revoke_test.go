package main

import (
	"dsmapp/internal/control"
	"dsmapp/internal/ipctest"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestOfflineUninstallRevokesRetainedLegacySimulation(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "connection.json")
	raw := []byte(`{"schema":1,"provider":"fake","connection":{"connected":true,"shareUrl":"https://nas-demo.invalid/share/example"}}`)
	if err := os.WriteFile(state, raw, 0600); err != nil {
		t.Fatal(err)
	}
	args := os.Args
	os.Args = []string{"connectd", "revoke", t.TempDir(), dir}
	t.Cleanup(func() { os.Args = args })
	if err := run(); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Connection struct {
			Connected bool
			ShareURL  string `json:"shareUrl"`
		}
	}
	if err = json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got.Connection.Connected || got.Connection.ShareURL != "" {
		t.Fatalf("retained simulation still connected: %s", b)
	}
}

func TestOfflineUninstallLeavesTheServerAndFreeLinkOff(t *testing.T) {
	dir := t.TempDir()
	server := filepath.Join(dir, "connect", "netbird-server")
	links := filepath.Join(dir, "connect", "workers")
	for _, d := range []string{server, links} {
		if err := os.MkdirAll(d, 0700); err != nil {
			t.Fatal(err)
		}
	}
	files := map[string]string{
		filepath.Join(server, "state.json"):   `{"enabled":true,"hostname":"netbird.example.test","routed":true,"bridged":true}`,
		filepath.Join(server, "secrets.json"): `{"storeKey":"SYNTHETIC"}`,
		filepath.Join(links, "state.json"):    `{"schema":1,"token":"SYNTHETIC","account_id":"acct","subdomain":"sub","owner":"o","records":{}}`,
	}
	for path, raw := range files {
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
	}
	args := os.Args
	os.Args = []string{"connectd", "revoke", t.TempDir(), dir}
	t.Cleanup(func() { os.Args = args })
	if err := run(); err != nil {
		t.Fatal(err)
	}
	var st struct{ Enabled, Routed, Bridged bool }
	raw, _ := os.ReadFile(filepath.Join(server, "state.json"))
	if json.Unmarshal(raw, &st) != nil || st.Enabled || !st.Routed || !st.Bridged {
		t.Fatalf("server state %s", raw)
	}
	if _, err := os.Stat(filepath.Join(server, "secrets.json")); err != nil {
		t.Fatal("kept data removed", err)
	}
	var w struct {
		Token       string
		FreeLinkOff bool `json:"free_link_off"`
	}
	raw, _ = os.ReadFile(filepath.Join(links, "state.json"))
	if json.Unmarshal(raw, &w) != nil || !w.FreeLinkOff || w.Token == "" {
		t.Fatalf("workers state %s", raw)
	}
}

func TestOfflineUninstallRefusesStaleSocketBeforeLegacyMutation(t *testing.T) {
	ipctest.Require(t)
	t.Cleanup(func() { os.Remove(control.Socket) })
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "connect"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(control.Socket, []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(dir, "connection.json")
	raw := []byte(`{"schema":1,"provider":"fake","connection":{"connected":true,"shareUrl":"https://nas-demo.invalid/share/example"}}`)
	if err := os.WriteFile(state, raw, 0600); err != nil {
		t.Fatal(err)
	}
	args := os.Args
	os.Args = []string{"connectd", "revoke", t.TempDir(), dir}
	t.Cleanup(func() { os.Args = args })
	if err := run(); err == nil {
		t.Fatal("stale socket accepted")
	}
	got, err := os.ReadFile(state)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(raw) {
		t.Fatal("legacy state changed despite failed live cleanup")
	}
}

func TestOfflineUninstallDoesNotFollowLegacyStateSymlink(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "untouched.json")
	raw := []byte(`{"schema":1,"provider":"fake","connection":{"connected":true,"shareUrl":"https://nas-demo.invalid/share/example"}}`)
	if err := os.WriteFile(outside, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "connection.json")); err != nil {
		t.Fatal(err)
	}
	args := os.Args
	os.Args = []string{"connectd", "revoke", t.TempDir(), dir}
	t.Cleanup(func() { os.Args = args })
	if err := run(); err == nil {
		t.Fatal("symlink accepted")
	}
	got, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(raw) {
		t.Fatal("followed legacy symlink")
	}
}

// A serve killed before cleanup (start timeout, SIGKILL) leaves its socket.
// Without the daemon lock holder nothing can answer it: revoke runs offline.
func TestOfflineUninstallRevokesBehindAbandonedSocket(t *testing.T) {
	ipctest.Require(t)
	t.Cleanup(func() { os.Remove(control.Socket) })
	dir := t.TempDir()
	socket := control.Socket
	if err := os.MkdirAll(filepath.Join(dir, "connect"), 0700); err != nil {
		t.Fatal(err)
	}
	abandonSocket(t, socket)
	state := filepath.Join(dir, "connection.json")
	raw := []byte(`{"schema":1,"provider":"fake","connection":{"connected":true,"shareUrl":"https://nas-demo.invalid/share/example"}}`)
	if err := os.WriteFile(state, raw, 0600); err != nil {
		t.Fatal(err)
	}
	args := os.Args
	os.Args = []string{"connectd", "revoke", t.TempDir(), dir}
	t.Cleanup(func() { os.Args = args })
	if err := run(); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(state); string(got) == string(raw) {
		t.Fatal("legacy state not revoked behind abandoned socket")
	}
	if _, err := os.Lstat(socket); !os.IsNotExist(err) {
		t.Fatal("abandoned socket kept")
	}
}

// A live daemon holds the lock. Its unanswered socket must still refuse.
func TestOfflineUninstallRefusesUnansweredSocketWhileDaemonLocked(t *testing.T) {
	ipctest.Require(t)
	t.Cleanup(func() { os.Remove(control.Socket) })
	dir := t.TempDir()
	socket := control.Socket
	if err := os.MkdirAll(filepath.Join(dir, "connect"), 0700); err != nil {
		t.Fatal(err)
	}
	abandonSocket(t, socket)
	lock, err := os.OpenFile(filepath.Join(dir, "connect", "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	args := os.Args
	os.Args = []string{"connectd", "revoke", t.TempDir(), dir}
	t.Cleanup(func() { os.Args = args })
	if err := run(); err == nil {
		t.Fatal("unanswered socket accepted while daemon holds its lock")
	}
	if _, err := os.Lstat(socket); err != nil {
		t.Fatal("live daemon socket removed")
	}
}

func abandonSocket(t *testing.T, path string) {
	t.Helper()
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	os.Chmod(path, 0600)
	ln.SetUnlinkOnClose(false)
	ln.Close()
}

func TestOfflineUninstallKeepsDisconnectedLegacySimulation(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "connection.json")
	if err := os.WriteFile(state, []byte(`{"schema":1,"provider":"fake","connection":{"connected":false,"shareUrl":""}}`), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Lstat(state)
	if err != nil {
		t.Fatal(err)
	}
	args := os.Args
	os.Args = []string{"connectd", "revoke", t.TempDir(), dir}
	t.Cleanup(func() { os.Args = args })
	if err = run(); err != nil {
		t.Fatal(err)
	}
	after, err := os.Lstat(state)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("disconnected legacy simulation rewritten")
	}
}

// connectd accepts a slow stop as disconnecting; uninstall must not remove
// credentials offline until the live daemon has actually stopped.
func TestUninstallWaitsForAnAcceptedDisconnectToFinish(t *testing.T) {
	ipctest.Require(t)
	t.Cleanup(func() { os.Remove(control.Socket) })
	dir, err := os.MkdirTemp("/tmp", "revoke-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := control.Socket
	if err = os.MkdirAll(filepath.Join(dir, "connect"), 0700); err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(filepath.Join(dir, "connect", "daemon.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	ln, err := ipctest.Listen(t, socket)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		state := "disconnected"
		if calls.Add(1) < 3 {
			state = "disconnecting"
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"status":{"provider":"multiple","state":"` + state + `","connections":[{"provider":"tailscale","state":"` + state + `"}]}}`))
	})}
	go server.Serve(ln)
	defer server.Close()
	revokePoll = 10 * time.Millisecond
	t.Cleanup(func() { revokePoll = time.Second })
	args := os.Args
	os.Args = []string{"connectd", "revoke", t.TempDir(), dir}
	t.Cleanup(func() { os.Args = args })
	if err = run(); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 3 {
		t.Fatal("uninstall must repeat the disconnect until it has stopped", calls.Load())
	}
}
