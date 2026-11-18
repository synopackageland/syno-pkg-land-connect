// Package cloudflare runs the official cloudflared for Connect: a temporary
// Quick Tunnel address, a tunnel on the user's own domain made after a browser
// sign-in, or a tunnel the user made and pasted as a token.
package cloudflare

import (
	"bytes"
	"encoding/json"
	"io"
	"net/url"
	"regexp"
	"strings"
)

var dnsLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// quickHost reports whether host is one label under trycloudflare.com.
func quickHost(host string) bool {
	label, ok := strings.CutSuffix(host, ".trycloudflare.com")
	return ok && dnsLabel.MatchString(label)
}

// quickHostnameFromOutput finds a Quick Tunnel address in one line of
// cloudflared output. Only a whole https URL with no user, port, path or
// query counts, so a longer or look-alike name never matches.
func quickHostnameFromOutput(line string) string {
	for _, field := range strings.FieldsFunc(line, func(r rune) bool { return r == ' ' || r == '|' || r == '\t' }) {
		u, e := url.Parse(field)
		if e != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
			continue
		}
		if quickHost(u.Host) {
			return u.Host
		}
	}
	return ""
}

// quickHostnameFromMetrics reads cloudflared's /quicktunnel reply,
// {"hostname":"…trycloudflare.com"}.
func quickHostnameFromMetrics(body []byte) string {
	d := json.NewDecoder(bytes.NewReader(body))
	d.DisallowUnknownFields()
	var reply struct {
		Hostname string `json:"hostname"`
	}
	var extra any
	if d.Decode(&reply) != nil || d.Decode(&extra) != io.EOF || !quickHost(reply.Hostname) {
		return ""
	}
	return reply.Hostname
}

// loginURLFromOutput finds the sign-in page `cloudflared tunnel login` prints.
// The page must be Cloudflare's own and must send the certificate back
// through Cloudflare's own store.
func loginURLFromOutput(line string) string {
	u, e := url.Parse(strings.TrimSpace(line))
	if e != nil || u.Scheme != "https" || u.Host != "dash.cloudflare.com" || u.User != nil || u.Path != "/argotunnel" {
		return ""
	}
	callback, e := url.Parse(u.Query().Get("callback"))
	if e != nil || callback.Scheme != "https" || callback.Host != "login.cloudflareaccess.org" || callback.User != nil {
		return ""
	}
	return u.String()
}
