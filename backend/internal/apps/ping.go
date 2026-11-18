package apps

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

const (
	Ready       = "ready"
	NeedsUpdate = "needs-update"
	Waiting     = "waiting"
)

// ProbePing is a capability diagnostic, never authority to forward user data.
// It sends only a fixed anonymous request to loopback and never follows a
// redirect. Approval and forwarding independently require the runtime's pin
// verifier and a ping on the very same verified TLS connection.
func ProbePing(ctx context.Context, pkg string, port int) string {
	return ProbeEntryPing(ctx, PackageEntry{Package: pkg, Port: port})
}

func ProbeEntryPing(ctx context.Context, e PackageEntry) string {
	pkg, port := e.Package, e.Port
	if !e.Valid() || !packagePattern.MatchString(pkg) || !validPort(port) {
		return NeedsUpdate
	}
	transport := &http.Transport{
		Proxy:               nil,
		DialContext:         (&net.Dialer{Timeout: 2 * time.Second}).DialContext,
		TLSHandshakeTimeout: 2 * time.Second,
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}, // Anonymous diagnostic only; not a pin or trust source.
		DisableKeepAlives:   true,
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://127.0.0.1:"+strconv.Itoa(port)+e.PingPath(), nil)
	if err != nil {
		return Waiting
	}
	res, err := client.Do(req)
	if err != nil {
		return Waiting
	}
	defer res.Body.Close()
	if res.StatusCode == 502 || res.StatusCode == 503 || res.StatusCode == 504 {
		return Waiting
	}
	if res.StatusCode != 200 {
		return NeedsUpdate
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, 257))
	if err != nil {
		return Waiting
	}
	if len(raw) > 256 {
		return NeedsUpdate
	}
	var data map[string]json.RawMessage
	if json.Unmarshal(raw, &data) != nil || len(data) != 1 {
		return NeedsUpdate
	}
	var got string
	if json.Unmarshal(data["package"], &got) != nil || got != pkg {
		return NeedsUpdate
	}
	return Ready
}
