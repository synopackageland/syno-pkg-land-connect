package cloudflare

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"dsmapp/internal/connector"
	"dsmapp/internal/publicnet"
)

var (
	errNotDSM           = connector.Coded("cloudflare_not_dsm", "the address answered, but not as DSM")
	errTunnelPending    = connector.Coded("cloudflare_tunnel_pending", "Cloudflare does not reach the tunnel at this address yet")
	errOriginDown       = connector.Coded("cloudflare_origin_unreachable", "the tunnel does not reach DSM")
	errDNSPending       = connector.Coded("cloudflare_dns_pending", "the address does not resolve yet")
	errTLS              = connector.Coded("cloudflare_tls_failed", "the address's HTTPS certificate is not valid")
	errAddressUnreached = connector.Coded("cloudflare_unreachable", "the address could not be reached")
)

// Probe opens the public address from the NAS. The request leaves through
// Cloudflare's edge and comes back down the tunnel, so a DSM answer shows the
// address works from outside. It proves only that some DSM answered, not
// which one, and not that sign-in or other apps work.
type Probe struct {
	Client *http.Client
	Now    func() time.Time
}

// dnsGraceWindow is the checker's settling window for a DNS-based way.
const dnsGraceWindow = connector.DNSSettle

// Check asks DSM's public, unauthenticated API list for SYNO.API.Auth. A new
// nonce and no-cache headers keep any cache from answering for DSM; redirects
// are refused and certificates are always verified.
func (p Probe) Check(ctx context.Context, address string) error {
	u, e := url.Parse(address)
	if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return errNotDSM
	}
	nonce := make([]byte, 16)
	rand.Read(nonce)
	u.Path = "/webapi/query.cgi"
	u.RawQuery = url.Values{"api": {"SYNO.API.Info"}, "version": {"1"}, "method": {"query"}, "query": {"SYNO.API.Auth"}, "nonce": {hex.EncodeToString(nonce)}}.Encode()
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if e != nil {
		return errNotDSM
	}
	req.Header.Set("Cache-Control", "no-cache, no-store")
	req.Header.Set("Pragma", "no-cache")
	transport := (publicnet.Dialer{}).Transport()
	defer transport.CloseIdleConnections()
	client := http.Client{Transport: transport}
	if p.Client != nil {
		client = *p.Client
	}
	client.Jar = nil
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, e := client.Do(req)
	if e != nil {
		return reachError(ctx, e, u.Hostname())
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == 530:
		return errTunnelPending
	case res.StatusCode == http.StatusBadGateway || res.StatusCode == http.StatusServiceUnavailable || res.StatusCode == http.StatusGatewayTimeout:
		return errOriginDown
	case res.StatusCode != http.StatusOK:
		return errNotDSM
	}
	var reply struct {
		Success bool `json:"success"`
		Data    map[string]struct {
			Path       string `json:"path"`
			MinVersion int    `json:"minVersion"`
			MaxVersion int    `json:"maxVersion"`
		} `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 65536)).Decode(&reply) != nil || !reply.Success {
		return errNotDSM
	}
	auth, ok := reply.Data["SYNO.API.Auth"]
	if !ok || auth.Path != "entry.cgi" || auth.MinVersion < 1 || auth.MaxVersion < auth.MinVersion || auth.MaxVersion > 100 {
		return errNotDSM
	}
	return nil
}

func reachError(ctx context.Context, e error, hostname string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var dns *net.DNSError
	var verify *tls.CertificateVerificationError
	var unknown x509.UnknownAuthorityError
	var host x509.HostnameError
	var invalid x509.CertificateInvalidError
	switch {
	case errors.As(e, &dns) && strings.EqualFold(strings.TrimSuffix(dns.Name, "."), hostname):
		return errDNSPending
	case errors.As(e, &verify), errors.As(e, &unknown), errors.As(e, &host), errors.As(e, &invalid):
		return errTLS
	}
	return errAddressUnreached
}

// Workers checks the Free link through public DSM's workers.dev address,
// given by url ("" while DSM is not public there). The request goes out
// through Cloudflare's edge and back down the relay to DSM.
func (p Probe) Workers(url func() string) connector.Probe {
	return func(ctx context.Context, _ string) (bool, error) {
		address := url()
		if address == "" {
			return false, connector.ErrNotConnected
		}
		if e := p.Check(ctx, address); e != nil {
			return false, e
		}
		return true, nil
	}
}

// For checks Cloudflare through its public address and leaves every other
// provider to next.
func (p Probe) For(status func(provider string) connector.Status, next connector.Probe) connector.Probe {
	return func(ctx context.Context, provider string) (bool, error) {
		if provider != "cloudflare" {
			return next(ctx, provider)
		}
		s := status(provider)
		if s.State != "connected" || len(s.Endpoints) == 0 {
			return false, connector.ErrNotConnected
		}
		if e := p.Check(ctx, s.Endpoints[0].URL); e != nil {
			now := time.Now()
			if p.Now != nil {
				now = p.Now()
			}
			age := now.Sub(s.AddressCreatedAt)
			if errors.Is(e, errDNSPending) && !s.AddressCreatedAt.IsZero() && age >= 0 && age < dnsGraceWindow {
				return false, connector.ErrCheckStarting
			}
			return false, e
		}
		return true, nil
	}
}
