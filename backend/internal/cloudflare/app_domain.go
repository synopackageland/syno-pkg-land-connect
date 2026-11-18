package cloudflare

import "strings"

type AppDomain struct {
	Hostname string
	Zone     string
	Enabled  bool
}

// AppDomain is local configuration, not proof of retained cloud authorization.
// It is safe from AppTargets callbacks while the tunnel's operations lock is held.
func (t *Tunnel) AppDomain() (AppDomain, error) {
	d, err := t.savedDomain()
	if err != nil {
		return AppDomain{}, err
	}
	_, zone, ok := strings.Cut(d.Hostname, ".")
	if !ok || zone == "" {
		return AppDomain{}, errZone
	}
	return AppDomain{Hostname: d.Hostname, Zone: zone, Enabled: t.loadSettings().Enabled}, nil
}
