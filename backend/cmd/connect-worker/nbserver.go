package main

import (
	"context"
	"errors"
	"time"

	"dsmapp/internal/binaries"
	"dsmapp/internal/connector"
)

var (
	errJoinPending = connector.Coded("netbird_server_join_pending", "this NAS is still joining the server; see the NetBird connection")
	serverPoll     = 2 * time.Second
)

type binaryJobs interface {
	Call(context.Context, string, string) (binaries.Status, error)
}
type binaryResolver interface {
	Version(context.Context, string) (string, error)
}

// installBinary downloads, verifies and selects product through the same
// binary job the Binaries page uses, unless a verified one is selected.
func installBinary(ctx context.Context, jobs binaryJobs, resolver binaryResolver, product string) error {
	if _, e := resolver.Version(ctx, product); e == nil {
		return nil
	}
	if _, e := jobs.Call(ctx, "update", product); e != nil && !errors.Is(e, binaries.ErrBusy) {
		return e
	}
	for {
		s, e := jobs.Call(ctx, "status", product)
		if e != nil {
			return e
		}
		if !s.Busy {
			if s.Error != "" {
				return errors.New(s.Error)
			}
			_, e = resolver.Version(ctx, product)
			return e
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(serverPoll):
		}
	}
}

// joinServer switches this NAS's NetBird to the server with a one-off key,
// replacing the running NetBird connection, and waits for it to connect.
func joinServer(ctx context.Context, c connector.Controller, status func(string) connector.Status, url, key string) error {
	if _, e := c.Connect(ctx, connector.Request{Provider: "netbird", ManagementURL: url, Token: key, ConfirmManagementPolicy: true, Replace: true}); e != nil {
		return e
	}
	wait, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	for {
		s := status("netbird")
		switch {
		case s.State == "connected" && s.ManagementURL == url:
			return nil
		case s.State == "failed":
			if s.Code != "" {
				return connector.Coded(s.Code, "NetBird did not join the server")
			}
			return errors.New("NetBird did not join the server")
		}
		select {
		case <-wait.Done():
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return errJoinPending
		case <-time.After(serverPoll):
		}
	}
}

type providerDisconnecter interface {
	DisconnectProvider(string) (connector.Status, error)
}

// leaveServer disconnects this NAS's NetBird when it uses the server being
// turned off; NetBird on another management server is left alone.
func leaveServer(c providerDisconnecter, status func(string) connector.Status, url string) error {
	if s := status("netbird"); s.State == "disconnected" || s.State == "" || s.ManagementURL != url {
		return nil
	}
	_, e := c.DisconnectProvider("netbird")
	return e
}
