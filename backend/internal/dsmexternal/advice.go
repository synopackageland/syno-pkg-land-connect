package dsmexternal

import (
	"net"
	"net/url"
	"regexp"
	"strings"

	"dsmapp/internal/connector"
)

// Suggestion is the stable public address DSM should advertise. Cloudflare
// serves it with a browser-trusted certificate on HTTPS port 443.
type Suggestion struct {
	Hostname  string `json:"hostname"`
	HTTPSPort int    `json:"httpsPort"`
	Source    string `json:"source"`
}

// Advice compares DSM's external access address with the stable address a
// running method provides. It never changes anything.
type Advice struct {
	// matches | differs | not-set | no-stable-address | unknown
	State string `json:"state"`
	// For no-stable-address: temporary-address (trycloudflare changes),
	// private-address (Tailscale/NetBird reach only your own devices), none.
	// For unknown: read-failed.
	Reason    string      `json:"reason,omitempty"`
	Current   *External   `json:"current"`
	Suggested *Suggestion `json:"suggested"`
}

func Advise(current *External, statuses []connector.Status) Advice {
	a := Advice{Current: current, Suggested: suggest(statuses)}
	switch {
	case a.Suggested == nil:
		a.State, a.Reason = "no-stable-address", "none"
		for _, s := range statuses {
			if s.State != "connected" {
				continue
			}
			if s.Provider == "cloudflare" {
				a.Reason = "temporary-address"
				break
			}
			if s.Provider == "tailscale" || s.Provider == "netbird" {
				a.Reason = "private-address"
			}
		}
	case current == nil:
		a.State, a.Reason = "unknown", "read-failed"
	case current.Hostname == "":
		a.State = "not-set"
	case SameHost(current.Hostname, a.Suggested.Hostname) && current.HTTPSPort != nil && *current.HTTPSPort == a.Suggested.HTTPSPort:
		a.State = "matches"
	default:
		a.State = "differs"
	}
	return a
}

// suggest returns the own domain or token address of a running Cloudflare
// tunnel. A temporary trycloudflare address changes on every start, and
// Tailscale and NetBird addresses are private, so neither is suggested.
func suggest(statuses []connector.Status) *Suggestion {
	for _, s := range statuses {
		if s.Provider != "cloudflare" || s.State != "connected" || (s.Mode != "domain" && s.Mode != "token") {
			continue
		}
		host := s.Hostname
		for _, e := range s.Endpoints {
			if host == "" && e.Public {
				if u, err := url.Parse(e.URL); err == nil && u.Scheme == "https" {
					host = u.Hostname()
				}
			}
		}
		host = strings.ToLower(strings.TrimSuffix(host, "."))
		if host == "" || strings.HasSuffix(host, ".trycloudflare.com") || !ValidHostname(host) {
			continue
		}
		return &Suggestion{Hostname: host, HTTPSPort: 443, Source: "cloudflare"}
	}
	return nil
}

// SameHost compares host names ignoring case and a trailing dot.
func SameHost(a, b string) bool {
	return strings.EqualFold(strings.TrimSuffix(a, "."), strings.TrimSuffix(b, "."))
}

var label = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

// ValidHostname accepts a DNS name or IP address as DSM's external address,
// or "" to clear it; never a URL, port, path or credentials.
func ValidHostname(h string) bool {
	if h == "" || net.ParseIP(h) != nil {
		return true
	}
	if len(h) > 253 {
		return false
	}
	for _, l := range strings.Split(h, ".") {
		if !label.MatchString(l) {
			return false
		}
	}
	return true
}
