package provider

import "context"

// AppNetwork reads fresh status without configuring Serve or changing approvals.
// The App-page owner uses DNSName and CertDomains for its private availability
// and to bind an explicit approval to the daemon's canonical authority.
func (p *Tailscale) AppNetwork(ctx context.Context) (TailscaleSnapshot, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.session == nil {
		return TailscaleSnapshot{}, ErrUnavailable
	}
	observed, e := p.session.Inspect(ctx)
	observed.IPs = append([]string(nil), observed.IPs...)
	observed.CertDomains = append([]string(nil), observed.CertDomains...)
	return observed, e
}
