package provider

import (
	"bytes"
	"dsmapp/internal/connector"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

var nbComplete = regexp.MustCompile(`^https://login\.netbird\.io/activate\?user_code=([A-Z]{4}-[A-Z]{4})$`)
var nbCode = regexp.MustCompile(`^[A-Z]{4}-[A-Z]{4}$`)
var nbFallback = regexp.MustCompile(`^([^ ]+) and enter the code ([A-Z]{4}-[A-Z]{4}) to authenticate\.$`)
var nbHost = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*$`)

func nbError(code string) error { return connector.Coded(code, "NetBird: "+code) }
func nbClean(raw string) bool {
	if len(raw) == 0 || len(raw) > 8192 {
		return false
	}
	for _, c := range []byte(raw) {
		if c < 33 || c > 126 || c == '\\' {
			return false
		}
	}
	return true
}
func netbirdOrigin(raw string) bool {
	if !nbClean(raw) || strings.Contains(raw, "%") {
		return false
	}
	u, e := url.Parse(raw)
	if e != nil {
		return false
	}
	if u.Port() != "" {
		v, e := strconv.Atoi(u.Port())
		if e != nil || v < 1 || v > 65535 {
			return false
		}
	}
	return len(u.Hostname()) <= 253 && u.Scheme == "https" && nbHost.MatchString(u.Hostname()) && u.User == nil && u.Path == "" && u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && u.RawFragment == "" && !strings.Contains(raw, "#")
}
func netbirdLoginURL(raw, code string, origins []string) (string, string, error) {
	if !nbClean(raw) {
		return "", "", nbError("login_url_rejected")
	}
	u, e := url.Parse(raw)
	if e != nil || u.Scheme != "https" || u.User != nil || u.Fragment != "" || strings.Contains(raw, "#") || !nbHost.MatchString(u.Hostname()) {
		return "", "", nbError("login_url_rejected")
	}
	if match := nbComplete.FindStringSubmatch(raw); match != nil {
		return raw, match[1], nil
	}
	if raw == "https://login.netbird.io/activate" && nbCode.MatchString(code) {
		return raw, code, nil
	}
	if u.Hostname() == "login.netbird.io" || u.Hostname() == "api.netbird.io" || u.Hostname() == "app.netbird.io" || strings.Contains(u.Hostname(), "netbird.io") {
		return "", "", nbError("login_url_rejected")
	}
	// Reject malformed paths even for an unlisted IdP: they never become a trust prompt.
	if strings.Contains(raw, "%") || !strings.HasPrefix(u.Path, "/") || !netbirdOrigin(u.Scheme+"://"+u.Host) {
		return "", "", nbError("login_url_rejected")
	}
	for _, segment := range strings.Split(u.Path, "/") {
		if segment == "." || segment == ".." {
			return "", "", nbError("login_url_rejected")
		}
	}
	trusted := false
	for _, origin := range origins {
		if netbirdOrigin(origin) && netbirdCanonicalOrigin(u.Scheme+"://"+u.Host) == netbirdCanonicalOrigin(origin) {
			trusted = true
		}
	}
	if !trusted {
		// The caller may display this validated origin as text, never navigate to it.
		return netbirdCanonicalOrigin(u.Scheme + "://" + u.Host), "", nbError("login_origin_required")
	}
	return raw, code, nil
}

// This writer is shared by stdout and stderr, never logs output, and publishes
// only a complete newline-terminated challenge from the official CLI marker.
type netbirdLoginParser struct {
	mu          sync.Mutex
	buffer      bytes.Buffer
	count       int
	marker      bool
	url, code   string
	origin      string
	err         error
	origins     []string
	onChallenge func(string, string)
	onError     func()
	diagnostic  string
}

func newNetbirdLoginParser(origins []string) *netbirdLoginParser {
	return &netbirdLoginParser{origins: origins}
}
func (p *netbirdLoginParser) Write(b []byte) (int, error) {
	p.mu.Lock()
	defer func() {
		if p.err != nil && p.onError != nil {
			p.onError()
		}
		p.mu.Unlock()
	}()
	if p.err != nil {
		return 0, p.err
	}
	p.count += len(b)
	if p.count > 65536 {
		p.err = nbError("login_output_invalid")
		return 0, p.err
	}
	p.buffer.Write(b)
	for {
		raw := p.buffer.Bytes()
		i := bytes.IndexByte(raw, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimSpace(string(raw[:i]))
		lower := strings.ToLower(line)
		switch {
		case strings.Contains(lower, "unimplemented"), strings.Contains(lower, "sso is not supported"), strings.Contains(lower, "sso login is not supported"):
			p.diagnostic = "sso_not_supported"
		case strings.Contains(lower, "unavailable"), strings.Contains(lower, "deadlineexceeded"), strings.Contains(lower, "no such host"), strings.Contains(lower, "connection refused"), strings.Contains(lower, "certificate"), strings.Contains(lower, "i/o timeout"), strings.Contains(lower, "connection reset"), strings.Contains(lower, "failed to request access token"):
			p.diagnostic = "control_unreachable"
		case strings.Contains(lower, "permissiondenied"), strings.Contains(lower, "unauthenticated"):
			p.diagnostic = "login_denied"
		}
		p.buffer.Next(i + 1)
		if strings.Contains(line, "Use this URL to log in:") {
			if p.marker || p.url != "" || line != "Use this URL to log in:" {
				p.err = nbError("login_output_invalid")
				break
			}
			p.marker = true
			continue
		}
		if !p.marker || line == "" {
			continue
		}
		if p.url != "" {
			// The CLI's closing error may name the IdP URL; it is a diagnostic, never published.
			if strings.Contains(line, "https://") && !strings.HasPrefix(line, "Error:") {
				p.err = nbError("login_output_invalid")
				break
			}
			continue
		}
		value, code := line, ""
		if m := nbFallback.FindStringSubmatch(line); m != nil {
			value, code = m[1], m[2]
		}
		p.url, p.code, p.err = netbirdLoginURL(value, code, p.origins)
		if connector.ErrorCode(p.err) == "login_origin_required" {
			p.origin = p.url
			p.url = ""
			p.code = ""
		}
		if p.err != nil {
			break
		}
		if p.onChallenge != nil {
			p.onChallenge(p.url, p.code)
		}
	}
	return len(b), p.err
}
func (p *netbirdLoginParser) Challenge() (string, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.url, p.code, p.err
}

func netbirdLoginOrigins(management string, external []string) []string {
	origins := append([]string(nil), external...)
	if canonical, err := netbirdManagementURL(management); err == nil && netbirdOrigin(canonical) {
		origins = append(origins, canonical)
	}
	return origins
}

func netbirdCanonicalOrigin(raw string) string {
	if !netbirdOrigin(raw) {
		return ""
	}
	u, _ := url.Parse(raw)
	if u.Port() == "443" {
		return "https://" + u.Hostname()
	}
	return u.Scheme + "://" + u.Host
}
