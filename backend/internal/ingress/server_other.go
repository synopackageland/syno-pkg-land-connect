//go:build !linux

package ingress

import (
	"context"
	"dsmapp/internal/apps"
)

type Server struct{}

func Start(context.Context, string, *apps.Service, func()) (*Server, error) {
	return nil, apps.ErrUnavailable
}
func (*Server) Close() {}
func Call(context.Context, string, Request) (apps.IngressStatus, error) {
	return apps.IngressStatus{}, apps.ErrUnavailable
}
func ResolveSubject(string, string, int) (apps.IngressSubject, error) {
	return apps.IngressSubject{}, apps.ErrUnavailable
}
