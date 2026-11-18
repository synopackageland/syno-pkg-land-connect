package helper

import (
	"net"
	"syscall"
)

func peerCredentials(conn *net.UnixConn) (*syscall.Ucred, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return nil, err
	}
	var cred *syscall.Ucred
	var sockErr error
	err = raw.Control(func(fd uintptr) {
		cred, sockErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	})
	if err != nil {
		return nil, err
	}
	if sockErr != nil {
		return nil, sockErr
	}
	return cred, nil
}

func peerUID(conn *net.UnixConn) (uint32, error) {
	cred, err := peerCredentials(conn)
	if err != nil {
		return 0, err
	}
	return cred.Uid, nil
}

func RootPeer(conn *net.UnixConn) error {
	uid, err := peerUID(conn)
	if err != nil {
		return err
	}
	if uid != 0 {
		return errTrust
	}
	return nil
}
