package provider

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

var dnsName = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)+$`)

var encodedControl = regexp.MustCompile(`(?i)%([01][0-9a-f]|7f)`)
var cloudLogin = regexp.MustCompile(`^https://login\.tailscale\.com/a/[A-Za-z0-9]{1,128}$`)

func safeLoginURL(raw string) string {
	if cloudLogin.MatchString(raw) {
		return raw
	}
	return ""
}

// Exact HTTPS origins are explicit administrator settings, never learned from AuthURL.
func exactOrigin(raw string) (string, error) {
	u, e := url.Parse(raw)
	if e != nil || len(raw) > 2048 || u.Scheme != "https" || u.User != nil || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || u.RawPath != "" || strings.HasSuffix(u.Hostname(), ".") {
		return "", ErrUnavailable
	}
	for _, c := range raw {
		if c < 33 || c > 126 || c == '\\' || c == '%' {
			return "", ErrUnavailable
		}
	}
	if !dnsName.MatchString(u.Hostname()) {
		return "", ErrUnavailable
	}
	if u.Port() != "" {
		port, e := strconv.Atoi(u.Port())
		if e != nil || port < 1 || port > 65535 {
			return "", ErrUnavailable
		}
	}
	return raw, nil
}
func trustedTailscaleURL(raw, server, origin string) string {
	if server == "" {
		return safeLoginURL(raw)
	}
	if origin == "" {
		origin = server
	}
	if _, e := exactOrigin(origin); e != nil {
		return ""
	}
	if len(raw) > 8192 || raw == "" {
		return ""
	}
	for _, c := range raw {
		if c < 33 || c > 126 || c == '\\' {
			return ""
		}
	}
	u, e := url.Parse(raw)
	if e != nil || u.User != nil || u.Fragment != "" || u.Scheme+"://"+u.Host != origin {
		return ""
	}
	if encodedControl.MatchString(raw) {
		return ""
	}
	lower := strings.ToLower(raw)
	for _, v := range []string{"%2f", "%5c", "%2e", "%00", "%0a", "%0d", "%09"} {
		if strings.Contains(lower, v) {
			return ""
		}
	}
	for _, part := range strings.Split(u.Path, "/") {
		if part == "." || part == ".." {
			return ""
		}
	}
	return raw
}
