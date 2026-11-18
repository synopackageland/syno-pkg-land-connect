package provider

import (
	"context"
	"crypto/rand"
	"dsmapp/internal/connector"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Probe checks the fresh trusted DSM port on loopback, which is where
// userspace tailscaled forwards a peer's connection to the NAS's own address.
// Its SOCKS proxy cannot reach that address itself. A local service check
// never asserts that another peer can reach this NAS.
func (p *Tailscale) Probe(ctx context.Context) (bool, error) {
	p.mu.Lock()
	state := p.status()
	p.mu.Unlock()
	if state.State != "connected" {
		return false, connector.ErrNotConnected
	}
	if p.HTTPPort == nil {
		return false, connector.Coded("local_check_failed", "DSM port unknown")
	}
	port, e := p.HTTPPort(ctx)
	if e != nil || port < 1 || port > 65535 {
		return false, connector.Coded("local_check_failed", "DSM port unknown")
	}
	return false, probeTailscaleDSM(ctx, "http://"+net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
}

// probeTailscaleDSM asks DSM's bounded API discovery at a loopback base URL.
func probeTailscaleDSM(ctx context.Context, base string) error {
	target, e := url.Parse(base)
	if e != nil {
		return connector.ErrNotConnected
	}
	nonce := make([]byte, 16)
	if _, e = rand.Read(nonce); e != nil {
		return e
	}
	target.Path = "/webapi/query.cgi"
	target.RawQuery = url.Values{"api": {"SYNO.API.Info"}, "version": {"1"}, "method": {"query"}, "query": {"SYNO.API.Auth"}, "_connect_probe": {hex.EncodeToString(nonce)}}.Encode()
	if target.Hostname() != "127.0.0.1" {
		return connector.ErrNotConnected
	}
	transport := &http.Transport{Proxy: nil, TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if e != nil {
		return e
	}
	req.Header.Set("Cache-Control", "no-cache, no-store")
	response, e := client.Do(req)
	if e != nil {
		return connector.Coded("local_check_failed", "NAS-side service check failed")
	}
	defer response.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(response.Body, 65537))
	if e != nil || len(raw) > 65536 || response.StatusCode != http.StatusOK {
		return connector.Coded("local_check_failed", "NAS-side service check failed")
	}
	var result struct {
		Success bool
		Data    map[string]struct {
			Path                   string
			MinVersion, MaxVersion int
		}
	}
	if json.Unmarshal(raw, &result) != nil || !result.Success {
		return connector.Coded("local_check_failed", "DSM response not verified")
	}
	if auth := result.Data["SYNO.API.Auth"]; !dsmAuthDescriptor(auth.Path, auth.MinVersion, auth.MaxVersion) {
		return connector.Coded("local_check_failed", "DSM response not verified")
	}
	return nil
}
