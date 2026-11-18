//go:build !linux

package helper

import "net"

func RootPeer(*net.UnixConn) error { return errTrust }
