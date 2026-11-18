//go:build !linux

package apps

import (
	"context"
	"net"
)

func CheckIngressListener(int, int) error                        { return ErrUnavailable }
func DialIngress(context.Context, int, string) (net.Conn, error) { return nil, ErrUnavailable }

func DialIngressUnix(context.Context, int, string) (net.Conn, error) { return nil, ErrUnavailable }
