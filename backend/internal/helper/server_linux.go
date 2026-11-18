package helper

import (
	"context"
	"encoding/json"
	"github.com/synopackageland/syno-pkg-land-sdk/ipc"
	"io"
	"net"
	"os/user"
	"path/filepath"
	"strconv"
	"time"
)

func serveLinux(c Client) error {
	r, err := readRecord(c.StateDir, 0)
	if err != nil || !r.Enabled || r.Inventory != c.Expected {
		return errTrust
	}
	// Only the supervisor writes process.json.
	// The package group can connect; ordinary users reach only the authenticated
	// CGI status route. SO_PEERCRED also checks the package's current numeric UID.
	u, err := user.Lookup(c.Expected.Package)
	if err != nil {
		return err
	}
	uid, err := strconv.ParseUint(u.Uid, 10, 32)
	if err != nil {
		return err
	}
	group, err := user.LookupGroup(c.Expected.Package)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handler := func(_ context.Context, conn net.Conn, _ ipc.PeerCredentials) {
		conn.SetDeadline(time.Now().Add(time.Second))
		var req Request
		d := json.NewDecoder(io.LimitReader(conn, 4097))
		d.DisallowUnknownFields()
		if d.Decode(&req) != nil {
			return
		}
		r, err := readRecord(c.StateDir, 0)
		disabled := err != nil || !r.Enabled || r.Inventory != c.Expected
		_ = json.NewEncoder(conn).Encode(Respond(c.Expected, disabled, req))
	}
	// The package and its root lifecycle controller have separate exact-UID
	// channels. Neither socket accepts privileged mutations.
	results := make(chan error, 2)
	go func() {
		results <- ipc.ServePeerUnix(ctx, filepath.Join(c.RunDir, "helper.sock"),
			ipc.PeerPolicy{UID: uint32(uid), GID: uint32(gid)}, handler)
	}()
	go func() {
		results <- ipc.ServePeerUnix(ctx, filepath.Join(c.RunDir, "helper-root.sock"),
			ipc.PeerPolicy{UID: 0, GID: 0}, handler)
	}()
	err = <-results
	cancel()
	<-results
	return err
}
