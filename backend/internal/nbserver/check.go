package nbserver

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"time"

	"dsmapp/internal/connector"
	"dsmapp/internal/publicnet"
)

var (
	errGRPCBlocked = connector.Coded("netbird_server_grpc_blocked", "the public address does not carry NetBird's gRPC")
	errUnreachable = connector.Coded("netbird_server_unreachable", "the public address could not be reached yet")
	errSignal      = connector.Coded("netbird_server_signal_blocked", "the public address does not carry NetBird's Signal stream")
)

// GRPCCheck asks the public address for the server's key, the first call
// every NetBird client makes. It passes only when the answer is gRPC over
// HTTP/2 with an OK status, so an HTTPS page alone never passes. It then
// opens the Signal stream the way a client does, keeping its request open,
// and needs the registration header back while it is open: a path that
// carries single calls but holds a two-way stream's answer (a Cloudflare
// Tunnel public hostname does) would pass the first check yet never let a
// device connect.
type GRPCCheck struct {
	// Transport defaults to public DNS and public addresses only.
	Transport http.RoundTripper
	// SignalWait bounds the wait for the registration header; 15 s when zero.
	SignalWait time.Duration
}

// signalRegistered is the header NetBird's Signal sends once a peer's
// stream is registered; clients give up without it.
const signalRegistered = "X-Wiretrustee-Peer-Registered"

func (c GRPCCheck) Check(ctx context.Context, managementURL string) error {
	transport := c.Transport
	if transport == nil {
		t := (publicnet.Dialer{}).Transport()
		t.ForceAttemptHTTP2 = true
		defer t.CloseIdleConnections()
		transport = t
	}
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if e := serverKey(ctx, client, managementURL); e != nil {
		return e
	}
	wait := c.SignalWait
	if wait <= 0 {
		wait = 15 * time.Second
	}
	return signalStream(ctx, transport, managementURL, wait)
}

func serverKey(ctx context.Context, client *http.Client, managementURL string) error {
	// One empty message: no compression flag and a zero length.
	req, e := http.NewRequestWithContext(ctx, "POST", strings.TrimSuffix(managementURL, "/")+"/management.ManagementService/GetServerKey", bytes.NewReader(make([]byte, 5)))
	if e != nil {
		return errUnreachable
	}
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("TE", "trailers")
	res, e := client.Do(req)
	if e != nil {
		return errUnreachable
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 64<<10))
	if res.StatusCode >= 520 && res.StatusCode <= 530 {
		// Cloudflare reached no tunnel or no server yet.
		return errUnreachable
	}
	status := res.Trailer.Get("Grpc-Status")
	if status == "" {
		status = res.Header.Get("Grpc-Status")
	}
	if res.ProtoMajor != 2 || res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "application/grpc") || status != "0" {
		return errGRPCBlocked
	}
	return nil
}

// signalStream opens Signal's two-way stream with a throwaway peer id and
// keeps the request open, as a client does, until the registration header
// arrives or the wait ends.
func signalStream(ctx context.Context, transport http.RoundTripper, managementURL string, wait time.Duration) error {
	id := make([]byte, 32)
	if _, e := rand.Read(id); e != nil {
		return errUnreachable
	}
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	body, open := io.Pipe()
	defer open.Close()
	req, e := http.NewRequestWithContext(ctx, "POST", strings.TrimSuffix(managementURL, "/")+"/signalexchange.SignalExchange/ConnectStream", body)
	if e != nil {
		return errUnreachable
	}
	req.Header.Set("Content-Type", "application/grpc")
	req.Header.Set("TE", "trailers")
	req.Header.Set("X-Wiretrustee-Peer-Id", base64.StdEncoding.EncodeToString(id))
	res, e := transport.RoundTrip(req)
	if e != nil {
		if ctx.Err() != nil {
			// The request went out and no answer came while it stayed open.
			return errSignal
		}
		return errUnreachable
	}
	defer res.Body.Close()
	if res.StatusCode >= 520 && res.StatusCode <= 530 {
		return errUnreachable
	}
	if res.ProtoMajor != 2 || res.StatusCode != 200 || res.Header.Get(signalRegistered) != "1" {
		return errSignal
	}
	return nil
}
