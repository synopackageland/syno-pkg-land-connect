//go:build linux

// Package ingress is the package-account control transport for restricted
// custom App grants. It exposes no administrator or provider operations.
package ingress

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"os/user"
	"strconv"
	"sync"
	"time"

	"dsmapp/internal/apps"
	"github.com/synopackageland/syno-pkg-land-sdk/ipc"
	"golang.org/x/sys/unix"
)

// Server delegates transport ownership and admission to the SDK.
type Server struct {
	peer   *ipc.PeerServer
	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
}

func Start(ctx context.Context, path string, service *apps.Service, changed func()) (*Server, error) {
	if service == nil {
		return nil, apps.ErrInvalid
	}
	s := &Server{}
	peer, err := ipc.StartPackagePeerUnix(ctx, path,
		func(_ context.Context, p ipc.PeerCredentials) bool { return p.UID != 0 },
		func(ctx context.Context, c net.Conn, p ipc.PeerCredentials) {
			defer c.Close()
			s.mu.Lock()
			if s.closed {
				s.mu.Unlock()
				return
			}
			s.wg.Add(1)
			s.mu.Unlock()
			defer s.wg.Done()
			handle(ctx, c, int(p.UID), service, changed)
		})
	if err != nil {
		return nil, err
	}
	s.peer = peer
	return s, nil
}

func (s *Server) Close() {
	// Preserve the worker's existing drain-before-teardown contract. The SDK
	// closes transport and cancels handler contexts before we wait for work.
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	_ = s.peer.Close()
	s.wg.Wait()
}

func peerUID(c *net.UnixConn) (int, error) {
	raw, err := c.SyscallConn()
	if err != nil {
		return 0, err
	}
	var cred *unix.Ucred
	var sockErr error
	err = raw.Control(func(fd uintptr) { cred, sockErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) })
	if err != nil || sockErr != nil || cred == nil || cred.Uid == 0 {
		return 0, apps.ErrInvalid
	}
	return int(cred.Uid), nil
}

func ResolveSubject(root, pkg string, uid int) (apps.IngressSubject, error) {
	return apps.VerifyIngressSubject(root, pkg, uid)
}

func handle(ctx context.Context, c net.Conn, uid int, service *apps.Service, changed func()) {
	_ = c.SetDeadline(time.Now().Add(10 * time.Second))
	var in Request
	raw, readErr := io.ReadAll(io.LimitReader(c, 4097))
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if readErr != nil || len(raw) > 4096 || decoder.Decode(&in) != nil {
		_ = json.NewEncoder(c).Encode(Response{Code: "bad_request"})
		return
	}
	// Exactly one JSON value followed by a write-half close is the frame.
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		_ = json.NewEncoder(c).Encode(Response{Code: "bad_request"})
		return
	}
	subject, err := ResolveSubject("/var/packages", in.Package, uid)
	if err != nil {
		_ = json.NewEncoder(c).Encode(Response{Code: "ingress_subject_refused"})
		return
	}
	if in.Method == "capabilities" && in.Request == nil && in.Ref == nil {
		_ = json.NewEncoder(c).Encode(Response{Version: 2, Capabilities: []string{"package-unix", "exact-path-set", "reference-by-key"}})
		return
	}
	if in.Method == "request" && (in.Request == nil || in.Ref != nil) || in.Method != "request" && (in.Ref == nil || in.Request != nil) {
		_ = json.NewEncoder(c).Encode(Response{Code: "bad_request"})
		return
	}
	var request apps.IngressRequest
	var ref apps.IngressRef
	if in.Request != nil {
		request = *in.Request
	}
	if in.Ref != nil {
		ref = *in.Ref
	}
	bounded, cancel := context.WithTimeout(ctx, 5*time.Second)
	out, err := service.IngressCall(bounded, subject, in.Method, request, ref)
	cancel()
	if changed != nil && in.Method != "status" {
		changed()
	}
	if err != nil {
		_ = json.NewEncoder(c).Encode(Response{Code: apps.ErrorCode(err)})
		return
	}
	_ = json.NewEncoder(c).Encode(Response{Result: &out})
}

// Call verifies Connect's nonroot UID before sending the grant request. The
// package account is the server authority; no token or DSM session is involved.
func Call(ctx context.Context, path string, in Request) (apps.IngressStatus, error) {
	account, err := user.Lookup("SynoPkgConnect")
	if err != nil {
		return apps.IngressStatus{}, apps.ErrUnavailable
	}
	expected, err := strconv.Atoi(account.Uid)
	if err != nil || expected <= 0 {
		return apps.IngressStatus{}, apps.ErrInvalid
	}
	c, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", path)
	if err != nil {
		return apps.IngressStatus{}, apps.ErrUnavailable
	}
	defer c.Close()
	u := c.(*net.UnixConn)
	uid, err := peerUID(u)
	if err != nil || uid != expected {
		return apps.IngressStatus{}, apps.ErrInvalid
	}
	deadline := time.Now().Add(10 * time.Second)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = c.SetDeadline(deadline)
	if err := json.NewEncoder(c).Encode(in); err != nil {
		return apps.IngressStatus{}, err
	}
	if err := u.CloseWrite(); err != nil {
		return apps.IngressStatus{}, err
	}
	var out Response
	if err := json.NewDecoder(io.LimitReader(c, 4097)).Decode(&out); err != nil {
		return apps.IngressStatus{}, apps.ErrUnavailable
	}
	if out.Code != "" {
		return apps.IngressStatus{}, errors.New(out.Code)
	}
	if out.Result == nil {
		return apps.IngressStatus{}, apps.ErrUnavailable
	}
	return *out.Result, nil
}
