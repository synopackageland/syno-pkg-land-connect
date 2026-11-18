package helper

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fixture(t *testing.T) Client {
	t.Helper()
	i := Inventory{Package: "Fixture", Version: "1.0.0-0001", Protocol: 1, PayloadDigest: strings.Repeat("a", 64), RegistryDigest: strings.Repeat("b", 64)}
	run, err := os.MkdirTemp("/tmp", "m1a-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(run) })
	return Client{Expected: i, StateDir: t.TempDir(), RunDir: run, Owner: uint32(os.Getuid()), Peer: func(*net.UnixConn) error { return nil }, Dial: func(ctx context.Context, path string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", path)
	}}
}

func record(t *testing.T, c Client, r Record) {
	t.Helper()
	b, _ := json.Marshal(r)
	if err := os.WriteFile(filepath.Join(c.StateDir, "approval.json"), b, 0644); err != nil {
		t.Fatal(err)
	}
}

func live(t *testing.T, c Client, i Inventory, reply func(Request) Response) {
	t.Helper()
	l, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(c.RunDir, "helper.sock"), Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			conn, err := l.AcceptUnix()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				var req Request
				if json.NewDecoder(conn).Decode(&req) != nil {
					return
				}
				r := Respond(i, false, req)
				if reply != nil {
					r = reply(req)
				}
				json.NewEncoder(conn).Encode(r)
			}()
		}
	}()
}

func TestSixStatesAndPriority(t *testing.T) {
	c := fixture(t)
	check := func(want string) {
		t.Helper()
		s := c.Status(context.Background())
		if s.State != want {
			t.Fatalf("want %s, got %+v", want, s)
		}
	}
	c.Expected = Inventory{}
	check("not-enabled")
	c.Expected = fixture(t).Expected
	check("needs-approval")
	record(t, c, Record{Inventory: c.Expected, Enabled: true})
	check("stopped")
	live(t, c, c.Expected, nil)
	check("ready")
	old := c.Expected
	old.Protocol++
	record(t, c, Record{Inventory: old, Enabled: true})
	check("incompatible")
	record(t, c, Record{Inventory: old, Enabled: false})
	check("not-enabled")
}

func TestLiveProbeCannotBeReplacedByApprovalOrHistoricalOutput(t *testing.T) {
	c := fixture(t)
	record(t, c, Record{Inventory: c.Expected, Enabled: true})
	if err := os.WriteFile(filepath.Join(c.RunDir, "helper.sock"), []byte("ready"), 0644); err != nil {
		t.Fatal(err)
	}
	if got := c.Status(context.Background()).State; got != "incompatible" {
		t.Fatal(got)
	}
}

func TestHandshakeFailuresAreRedacted(t *testing.T) {
	for _, kind := range []string{"nonce", "protocol", "payload", "worker", "version", "peer"} {
		t.Run(kind, func(t *testing.T) {
			c := fixture(t)
			record(t, c, Record{Inventory: c.Expected, Enabled: true})
			live(t, c, c.Expected, func(req Request) Response {
				r := Respond(c.Expected, false, req)
				switch kind {
				case "nonce":
					r.Nonce = "replay"
				case "protocol":
					r.Inventory.Protocol++
				case "payload":
					r.Inventory.PayloadDigest = "foreign"
				case "worker":
					r.Inventory.RegistryDigest = "foreign"
				case "version":
					r.Inventory.Version = "old"
				}
				return r
			})
			if kind == "peer" {
				c.Peer = func(*net.UnixConn) error { return os.ErrPermission }
			}
			s := c.Status(context.Background())
			if s.State != "incompatible" {
				t.Fatalf("%+v", s)
			}
			b, _ := json.Marshal(s)
			if strings.Contains(string(b), c.StateDir) || strings.Contains(string(b), "foreign") {
				t.Fatal(string(b))
			}
		})
	}
	c := fixture(t)
	record(t, c, Record{Inventory: c.Expected, Enabled: true})
	live(t, c, c.Expected, func(req Request) Response { return Response{Inventory: c.Expected, Nonce: req.Nonce, Error: "failed"} })
	if c.Status(context.Background()).State != "failed" {
		t.Fatal("health failure must be failed")
	}
}

func TestUntrustedPersistentStateFailsClosed(t *testing.T) {
	for _, kind := range []string{"writable", "symlink", "malformed"} {
		t.Run(kind, func(t *testing.T) {
			c := fixture(t)
			record(t, c, Record{Inventory: c.Expected, Enabled: true})
			p := filepath.Join(c.StateDir, "approval.json")
			switch kind {
			case "writable":
				os.Chmod(p, 0666)
			case "symlink":
				os.Rename(p, p+".real")
				os.Symlink(p+".real", p)
			case "malformed":
				os.WriteFile(p, []byte(`{"enabled":false}`), 0644)
			}
			if c.Status(context.Background()).State != "incompatible" {
				t.Fatal("untrusted state accepted")
			}
		})
	}
}

func TestOldHelperRejectsNewInventoryAndAllUnimplementedWork(t *testing.T) {
	i := fixture(t).Expected
	for _, field := range []string{"protocol", "payload", "workers", "version", "package"} {
		new := i
		switch field {
		case "protocol":
			new.Protocol++
		case "payload":
			new.PayloadDigest = strings.Repeat("c", 64)
		case "workers":
			new.RegistryDigest = strings.Repeat("c", 64)
		case "version":
			new.Version = "2.0.0-0001"
		case "package":
			new.Package = "Other"
		}
		r := Respond(i, false, Request{Inventory: new, Op: "work", Nonce: "test"})
		if r.Error != "incompatible" || r.OK {
			t.Fatalf("%s: %+v", field, r)
		}
	}
	if r := Respond(i, true, Request{Inventory: i, Op: "health"}); r.OK || r.Error != "disabled" {
		t.Fatal(r)
	}
	if r := Respond(i, false, Request{Inventory: i, Op: "work"}); r.OK || r.Error != "unsupported" {
		t.Fatal(r)
	}
}

func TestMissingEnableFieldIsNotARevocationRecord(t *testing.T) {
	c := fixture(t)
	b, _ := json.Marshal(map[string]any{"inventory": c.Expected})
	os.WriteFile(filepath.Join(c.StateDir, "approval.json"), b, 0644)
	if c.Status(context.Background()).State != "incompatible" {
		t.Fatal("incomplete approval treated as trusted disable")
	}
}

func TestProcessRecordIsNotReadableByOrdinaryOSUsers(t *testing.T) {
	c, _ := controlFixture(t)
	s, err := os.Stat(filepath.Join(c.RunDir, "process.json"))
	if err != nil || s.Mode().Perm() != 0600 {
		t.Fatalf("process record permissions: %v %v", s, err)
	}
}
