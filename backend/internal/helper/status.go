// Package helper implements the opt-in per-package lifecycle contract. It has
// no file/grant/job API: all unimplemented operations fail closed.
package helper

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/synopackageland/syno-pkg-land-sdk/ipc"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
	"time"
)

type Inventory struct {
	Package        string `json:"package"`
	Version        string `json:"version"`
	Protocol       int    `json:"protocol"`
	PayloadDigest  string `json:"payloadDigest"`
	RegistryDigest string `json:"registryDigest"`
}

var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var packagePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{1,63}$`)
var versionPattern = regexp.MustCompile(`^\d+(\.\d+){0,3}-\d{4}$`)

func (i Inventory) Valid() bool {
	return packagePattern.MatchString(i.Package) && versionPattern.MatchString(i.Version) && i.Protocol > 0 && digestPattern.MatchString(i.PayloadDigest) && digestPattern.MatchString(i.RegistryDigest)
}

type Record struct {
	Inventory Inventory `json:"inventory"`
	Enabled   bool      `json:"enabled"`
	Residual  bool      `json:"residual,omitempty"`
}
type Request struct {
	Inventory Inventory `json:"inventory"`
	Op        string    `json:"op"`
	Nonce     string    `json:"nonce"`
}
type Response struct {
	Inventory Inventory `json:"inventory"`
	Nonce     string    `json:"nonce"`
	OK        bool      `json:"ok"`
	Error     string    `json:"error,omitempty"`
}

func Respond(i Inventory, disabled bool, req Request) Response {
	r := Response{Inventory: i, Nonce: req.Nonce}
	switch {
	case disabled:
		r.Error = "disabled"
	case !i.Valid() || req.Inventory != i:
		r.Error = "incompatible"
	case req.Op != "health":
		r.Error = "unsupported"
	default:
		r.OK = true
	}
	return r
}

// State is safe to expose to any authenticated DSM user. Paths, PIDs, worker
// internals, scheduler definitions and raw errors never enter this envelope.
type State struct {
	State          string `json:"state"`
	Reason         string `json:"reason"`
	PackageRunning string `json:"packageRunning"`
	Scheduler      string `json:"scheduler"`
	Cleanup        string `json:"cleanup,omitempty"`
}

type Client struct {
	Expected         Inventory
	StateDir, RunDir string
	// Production uses UID 0 and RootPeer. Tests inject only local fixture identity.
	Owner uint32
	Peer  func(*net.UnixConn) error
	Dial  func(context.Context, string) (net.Conn, error)
}

func NewClient(i Inventory) Client {
	return Client{Expected: i, StateDir: "/var/lib/synopkgland-" + i.Package, RunDir: "/var/run/synopkgland/" + i.Package, Peer: RootPeer}
}

func (c Client) legacyRuntime() bool {
	if c.RunDir != "/var/run/synopkgland/"+c.Expected.Package {
		return false
	}
	_, err := os.Lstat("/run/synopkgland-helper-" + c.Expected.Package)
	return !errors.Is(err, fs.ErrNotExist)
}

// LocalClient is a nonprivileged diagnostic entry point for package hooks.
// Hashing the local executable establishes only the expected build inventory;
// readiness still requires the protected record and root socket handshake.
func LocalClient(i Inventory) (Client, error) {
	exe, err := os.Executable()
	if err != nil {
		return Client{}, err
	}
	f, err := os.Open(exe)
	if err != nil {
		return Client{}, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, (64<<20)+1)); err != nil {
		return Client{}, err
	}
	i.PayloadDigest = hex.EncodeToString(h.Sum(nil))
	if !i.Valid() {
		return Client{}, errTrust
	}
	return NewClient(i), nil
}

func (c Client) CheckStopped(ctx context.Context) error {
	s := c.Status(ctx)
	if s.Cleanup != "" || (s.State != "not-enabled" && s.State != "stopped" && s.State != "needs-approval") {
		return errors.New("approved helper teardown required")
	}
	for _, file := range []string{"process.json", "helper.sock", "helper-root.sock", "supervisor.sock"} {
		if _, err := os.Lstat(filepath.Join(c.RunDir, file)); !errors.Is(err, fs.ErrNotExist) {
			return errors.New("helper residue; approved teardown required")
		}
	}
	return nil
}

var errTrust = errors.New("untrusted helper state")

func protected(path string, owner uint32, directory bool) error {
	st, err := os.Lstat(path)
	if err != nil {
		return err
	}
	s, ok := st.Sys().(*syscall.Stat_t)
	if !ok || s.Uid != owner || st.Mode().Perm()&0022 != 0 || (directory && !st.IsDir()) || (!directory && !st.Mode().IsRegular()) {
		return errTrust
	}
	return nil
}

func readRecord(dir string, owner uint32) (Record, error) {
	var r Record
	if err := protected(dir, owner, true); err != nil {
		return r, err
	}
	f, err := os.OpenFile(filepath.Join(dir, "approval.json"), os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return r, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return r, err
	}
	s, ok := st.Sys().(*syscall.Stat_t)
	if !ok || s.Uid != owner || st.Mode().Perm()&0022 != 0 || !st.Mode().IsRegular() || st.Size() > 4096 {
		return r, errTrust
	}
	d := json.NewDecoder(io.LimitReader(f, 4097))
	d.DisallowUnknownFields()
	var decoded struct {
		Inventory Inventory `json:"inventory"`
		Enabled   *bool     `json:"enabled"`
		Residual  bool      `json:"residual,omitempty"`
	}
	if d.Decode(&decoded) != nil || d.Decode(new(any)) != io.EOF || !decoded.Inventory.Valid() || decoded.Enabled == nil {
		return r, errTrust
	}
	r = Record{Inventory: decoded.Inventory, Enabled: *decoded.Enabled, Residual: decoded.Residual}
	return r, nil
}

func (c Client) Status(ctx context.Context) State {
	result := func(state, reason string) State {
		return State{State: state, Reason: reason, PackageRunning: "unknown", Scheduler: "not-probed"}
	}
	if c.Expected.Package == "" {
		return result("not-enabled", "not-requested")
	}
	if !c.Expected.Valid() {
		return result("incompatible", "invalid-inventory")
	}
	if c.legacyRuntime() {
		s := result("failed", "legacy-runtime")
		s.Cleanup = "required"
		return s
	}
	r, err := readRecord(c.StateDir, c.Owner)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return result("incompatible", "untrusted-approval")
	}
	if err == nil && !r.Enabled {
		s := result("not-enabled", "disabled")
		if r.Residual {
			s.Cleanup = "required"
		}
		return s
	}
	if err == nil && r.Inventory != c.Expected {
		return result("incompatible", "inventory-mismatch")
	}
	if err == nil && r.Residual {
		s := result("failed", "incomplete-transition")
		s.Cleanup = "required"
		return s
	}
	// Conflict has precedence over missing approval. Even an unapproved leftover
	// socket is probed before claiming that authorization alone can fix it.
	if e := protected(c.RunDir, c.Owner, true); e != nil && !errors.Is(e, fs.ErrNotExist) {
		return result("incompatible", "untrusted-runtime")
	}
	socket := filepath.Join(c.RunDir, "helper.sock")
	st, e := os.Lstat(socket)
	if e == nil {
		s, ok := st.Sys().(*syscall.Stat_t)
		if !ok || s.Uid != c.Owner || st.Mode()&os.ModeSocket == 0 || st.Mode().Perm()&0002 != 0 {
			return result("incompatible", "untrusted-socket")
		}
		state, reason := c.probe(ctx, socket)
		if state != "ready" {
			return result(state, reason)
		}
	} else if !errors.Is(e, fs.ErrNotExist) {
		return result("failed", "runtime-unreadable")
	}
	if err != nil {
		return result("needs-approval", "approval-missing")
	}
	if e != nil {
		return result("stopped", "runtime-missing")
	}
	return result("ready", "live-handshake")
}

func (c Client) probe(ctx context.Context, socket string) (string, string) {
	deadline := time.Now().Add(time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	dialCtx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	var conn net.Conn
	var err error
	if c.Dial != nil {
		conn, err = c.Dial(dialCtx, socket)
	} else {
		if os.Geteuid() == 0 {
			socket = filepath.Join(c.RunDir, "helper-root.sock")
		}
		conn, err = ipc.DialPeerUnix(dialCtx, socket)
	}
	if err != nil {
		if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, fs.ErrNotExist) {
			return "stopped", "runtime-not-listening"
		}
		return "failed", "probe-failed"
	}
	defer conn.Close()
	if c.Peer == nil || c.Peer(conn.(*net.UnixConn)) != nil {
		return "incompatible", "untrusted-peer"
	}
	conn.SetDeadline(deadline)
	var nonce [32]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return "failed", "probe-failed"
	}
	req := Request{c.Expected, "health", hex.EncodeToString(nonce[:])}
	if json.NewEncoder(conn).Encode(req) != nil {
		return "failed", "probe-failed"
	}
	var res Response
	d := json.NewDecoder(io.LimitReader(conn, 4097))
	d.DisallowUnknownFields()
	if d.Decode(&res) != nil {
		return "failed", "invalid-handshake"
	}
	if res.Nonce != req.Nonce || res.Inventory != c.Expected || res.Error == "incompatible" {
		return "incompatible", "handshake-mismatch"
	}
	if !res.OK || res.Error != "" {
		return "failed", "health-refused"
	}
	return "ready", "live-handshake"
}
