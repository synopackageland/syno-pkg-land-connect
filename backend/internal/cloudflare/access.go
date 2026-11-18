package cloudflare

import (
	"dsmapp/internal/connector"
	"errors"
	"os"
)

// AccessInventory reads atomic private records without acquiring the tunnel's
// operation lock: callers also use it from the before-publication hook.
func (t *Tunnel) AccessInventory() (account string, hosts []string, unsupported bool, err error) {
	s := t.loadSettings()
	unsupported = s.Enabled && s.Mode != "domain"
	d, e := t.savedDomain()
	if e != nil {
		if _, stat := os.Lstat(t.domainFile("domain.json")); errors.Is(stat, os.ErrNotExist) {
			return "", nil, unsupported, nil
		}
		return "", nil, unsupported, e
	}
	raw, e := connector.ReadPrivate(t.domainFile("credentials.json"))
	if e != nil {
		return "", nil, unsupported, e
	}
	c, e := parseCredentials(raw)
	if e != nil {
		return "", nil, unsupported, e
	}
	account = c.account
	hosts = append(hosts, d.Hostname)
	for _, h := range d.Routed {
		hosts = append(hosts, h)
	}
	server := t.serverHostname()
	filtered := hosts[:0]
	for _, h := range hosts {
		if h != server {
			filtered = append(filtered, h)
		}
	}
	hosts = filtered
	return account, hosts, unsupported, nil
}

// AccessDSMHostname identifies the primary hostname independently of inventory order.
func (t *Tunnel) AccessDSMHostname() string {
	d, e := t.savedDomain()
	if e != nil {
		return ""
	}
	return d.Hostname
}
