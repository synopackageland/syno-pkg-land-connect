package apps

import (
	"bufio"
	"context"
	"fmt"
	"golang.org/x/sys/unix"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// CheckIngressListener requires a literal IPv4 loopback bind by the subject.
// Wildcard binds and ambiguous same-port listeners fail closed.
func CheckIngressListener(uid, port int) error {
	if uid <= 0 || port < 1024 || port > 65535 {
		return ErrInvalid
	}
	found := false
	for _, l := range ListeningTCP() {
		if l.Port != port {
			continue
		}
		if l.UID != uid || l.Addr.String() != "127.0.0.1" {
			return ErrInvalid
		}
		found = true
	}
	if !found {
		return ErrNotReady
	}
	return nil
}

// DialIngress checks the accepted server socket's UID using the reverse TCP
// tuple before sending any HTTP bytes. A listener swap between validation and
// connect therefore cannot redirect a request to another package or DSM.
func DialIngress(ctx context.Context, uid int, address string) (net.Conn, error) {
	host, text, err := net.SplitHostPort(address)
	port, parseErr := strconv.Atoi(text)
	if err != nil || parseErr != nil || host != "127.0.0.1" {
		return nil, ErrInvalid
	}
	if err = CheckIngressListener(uid, port); err != nil {
		return nil, err
	}
	c, err := (&net.Dialer{Timeout: 3 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, "tcp4", address)
	if err != nil {
		return nil, err
	}
	local := c.LocalAddr().(*net.TCPAddr).AddrPort()
	remote := c.RemoteAddr().(*net.TCPAddr).AddrPort()
	// The kernel may expose an unaccepted server socket with inode zero.
	// Wait briefly for accept, without writing a byte or weakening the UID test.
	for attempt := 0; attempt < 20; attempt++ {
		if establishedOwner(uid, remote, local) {
			return c, nil
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			c.Close()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	c.Close()
	return nil, ErrNotReady
}

func establishedOwner(uid int, server, client netip.AddrPort) bool {
	f, err := os.Open("/proc/net/tcp")
	if err != nil {
		return false
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		fields := strings.Fields(s.Text())
		if len(fields) < 10 || fields[3] != "01" {
			continue
		}
		if fields[1] == procTuple(server) && fields[2] == procTuple(client) {
			return fields[7] == strconv.Itoa(uid) && fields[9] != "0"
		}
	}
	return false
}

func procTuple(a netip.AddrPort) string {
	b := a.Addr().Unmap().As4()
	return fmt.Sprintf("%02X%02X%02X%02X:%04X", b[3], b[2], b[1], b[0], a.Port())
}

// DialIngressUnix authenticates the accepted server with kernel credentials.
// Filesystem access and a retained socket path never establish package identity.
func DialIngressUnix(ctx context.Context, uid int, path string) (net.Conn, error) {
	if uid <= 0 {
		return nil, ErrInvalid
	}
	fd, err := unix.Open(filepath.Dir(path), unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	var dir, st unix.Stat_t
	if unix.Fstat(fd, &dir) != nil || dir.Uid != uint32(uid) || dir.Mode&07777 != 0711 {
		return nil, ErrInvalid
	}
	if unix.Fstatat(fd, filepath.Base(path), &st, unix.AT_SYMLINK_NOFOLLOW) != nil || st.Uid != uint32(uid) || st.Mode&07777 != 0666 || st.Mode&unix.S_IFMT != unix.S_IFSOCK {
		return nil, ErrInvalid
	}
	c, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "unix", fmt.Sprintf("/proc/self/fd/%d/%s", fd, filepath.Base(path)))
	if err != nil {
		return nil, err
	}
	raw, err := c.(*net.UnixConn).SyscallConn()
	var cred *unix.Ucred
	var sockErr error
	if err == nil {
		err = raw.Control(func(fd uintptr) { cred, sockErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED) })
	}
	if err != nil || sockErr != nil || cred == nil || cred.Uid != uint32(uid) || cred.Uid == 0 {
		c.Close()
		return nil, ErrInvalid
	}
	return c, nil
}
