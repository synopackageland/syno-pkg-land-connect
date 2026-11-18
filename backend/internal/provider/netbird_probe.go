package provider

import (
	"context"
	"dsmapp/internal/connector"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"
)

// Probe checks the daemon identity and the fresh trusted DSM port on loopback.
// NetBird's userspace SOCKS cannot hairpin to its own overlay IP. A local
// service check never asserts that another peer can reach this NAS.
func (p *NetBird) Probe(ctx context.Context) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.status()
	if !s.NetworkReady {
		return false, connector.ErrNotConnected
	}
	if p.HTTPPort == nil {
		return false, nbError("local_check_failed")
	}
	port, e := p.HTTPPort(ctx)
	if e != nil {
		return false, nbError("local_check_failed")
	}
	session, ok := p.session.(interface {
		CheckHTTP(context.Context, string, int) error
	})
	if !ok {
		return false, nbError("local_check_failed")
	}
	if e = session.CheckHTTP(ctx, "127.0.0.1", port); e != nil {
		return false, e
	}
	if s.NetBirdFQDN != "" {
		if e = session.CheckHTTP(ctx, s.NetBirdFQDN, port); e == nil {
			p.verifiedFQDN = s.NetBirdFQDN
			p.verifiedIP = s.NetBirdIP
			p.verifiedPort = port
		}
	}
	return false, nil
}
func (s *netbirdExecSession) CheckHTTP(ctx context.Context, host string, port int) error {
	if (host != "127.0.0.1" && s.proxyPort <= 0) || port < 1 || port > 65535 {
		return nbError("proxy_bind_failed")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return netbirdSOCKS(ctx, s.proxyPort, host, port)
	}, DisableKeepAlives: true}
	if host == "127.0.0.1" {
		transport = netbirdLocalTransport(port)
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	// Query DSM's bounded API discovery, rather than treat any HTTP 200 as DSM.
	target := "http://" + net.JoinHostPort(host, strconv.Itoa(port)) + "/webapi/query.cgi?api=SYNO.API.Info&version=1&method=query&query=SYNO.API.Auth"
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if e != nil {
		return nbError("local_check_failed")
	}
	resp, e := client.Do(req)
	if e != nil {
		if connector.ErrorCode(e) != "" {
			return e
		}
		return nbError("local_check_failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nbError("local_check_failed")
	}
	raw, e := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if e != nil || len(raw) > 65536 {
		return nbError("local_check_failed")
	}
	var result struct {
		Success bool `json:"success"`
		Data    map[string]struct {
			Path string `json:"path"`
			Min  int    `json:"minVersion"`
			Max  int    `json:"maxVersion"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &result) != nil || !result.Success {
		return nbError("local_check_failed")
	}
	auth := result.Data["SYNO.API.Auth"]
	if !dsmAuthDescriptor(auth.Path, auth.Min, auth.Max) {
		return nbError("local_check_failed")
	}
	return nil
}

// Only a trusted detected DSM port enters this path, never a browser target.
// Do not use environment proxies or the overlay for a host service check.
func netbirdLocalTransport(port int) *http.Transport {
	return &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	}, DisableKeepAlives: true}
}

func netbirdSOCKS(ctx context.Context, proxyPort int, host string, port int) (net.Conn, error) {
	d := net.Dialer{Timeout: 5 * time.Second}
	c, e := d.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(proxyPort)))
	if e != nil {
		return nil, nbError("proxy_bind_failed")
	}
	fail := func(e error) (net.Conn, error) { c.Close(); return nil, e }
	deadline := time.Now().Add(10 * time.Second)
	if v, ok := ctx.Deadline(); ok && v.Before(deadline) {
		deadline = v
	}
	c.SetDeadline(deadline)
	if _, e = c.Write([]byte{5, 1, 0}); e != nil {
		return fail(e)
	}
	reply := make([]byte, 2)
	if _, e = io.ReadFull(c, reply); e != nil {
		return fail(e)
	}
	if reply[0] != 5 || reply[1] != 0 {
		return fail(nbError("local_check_failed"))
	}
	if len(host) > 253 {
		return fail(nbError("local_check_failed"))
	}
	request := append([]byte{5, 1, 0, 3, byte(len(host))}, []byte(host)...)
	request = binary.BigEndian.AppendUint16(request, uint16(port))
	if _, e = c.Write(request); e != nil {
		return fail(e)
	}
	header := make([]byte, 4)
	if _, e = io.ReadFull(c, header); e != nil {
		return fail(e)
	}
	if header[0] != 5 || header[1] != 0 || header[2] != 0 {
		return fail(nbError("local_check_failed"))
	}
	length := 0
	switch header[3] {
	case 1:
		length = 4
	case 4:
		length = 16
	case 3:
		one := []byte{0}
		if _, e = io.ReadFull(c, one); e != nil {
			return fail(e)
		}
		length = int(one[0])
	default:
		return fail(nbError("local_check_failed"))
	}
	if _, e = io.ReadFull(c, make([]byte, length+2)); e != nil {
		return fail(e)
	}
	return c, nil
}
