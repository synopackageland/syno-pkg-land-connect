package apps

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/netip"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A custom App is one an administrator added by URL: any web service on the
// NAS or the LAN that was not built with the SDK. Its ID never names a DSM
// package; discovery skips package directories that start with the prefix.
const (
	CustomPrefix = "custom."
	ModeCustom   = Mode("C") // no pin: the target is the configured origin itself
	MaxCustom    = 64
)

var (
	customIDPattern = regexp.MustCompile(`^custom\.[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$`)
	hostPattern     = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)*$`)
	nonLabelRune    = regexp.MustCompile(`[^a-z0-9]+`)
	metadataIPv6    = netip.MustParseAddr("fd00:ec2::254")
)

// CustomApp is the administrator's definition. URL is a normalized origin with an optional start path.
// The whole origin is forwarded; the path selects its initial page. AppSignIn turns Connect's DSM sign-in
// off; it is only saved with an explicit risk confirmation.
type CustomApp struct {
	Name        string        `json:"name"`
	URL         string        `json:"url"`
	AppSignIn   bool          `json:"app_sign_in,omitempty"`
	InsecureTLS bool          `json:"insecure_tls,omitempty"`
	Ingress     *IngressGrant `json:"ingress,omitempty"`
	// Published is the last public route Connect created for this App. Turning
	// public access off keeps the route by design, so deletion must not depend
	// on a currently active scope to find and remove it later.
	Published *CustomPublication `json:"published,omitempty"`
}

// CustomPublication is cleanup evidence, not an active approval. It retains
// only values Connect wrote after a successful public activation.
type CustomPublication struct {
	Provider  string `json:"provider,omitempty"`
	Authority string `json:"authority"`
	Socket    string `json:"socket"`
}

// RememberCustomPublication migrates and refreshes the cleanup receipt from a
// durable public scope before any path withdraws that scope. Older state files
// have no Published field, so the first close after upgrade must backfill it.
func (s *State) RememberCustomPublication(id string, scope *Scope) {
	def, ok := s.Custom[id]
	if !ok || scope == nil {
		return
	}
	def.Published = &CustomPublication{Provider: scope.Provider, Authority: scope.Authority, Socket: scope.Socket}
	s.Custom[id] = def
}

// CustomView is what the page shows of a custom App.
type CustomView struct {
	Ingress     *IngressGrant `json:"ingress,omitempty"`
	URL         string        `json:"url"`
	AppSignIn   bool          `json:"appSignIn"`
	InsecureTLS bool          `json:"insecureTls"`
	Reachable   *bool         `json:"reachable,omitempty"`
}

func IsCustom(pkg string) bool { return strings.HasPrefix(pkg, CustomPrefix) }

var (
	errCustomURL     = customCode("custom_url_invalid", "Enter a URL such as http://127.0.0.1:8080")
	errCustomScheme  = customCode("custom_url_scheme", "Use an http:// or https:// URL")
	errCustomPath    = customCode("custom_url_path", "Enter a canonical App path without a query or fragment")
	errCustomRefused = customCode("custom_target_refused", "Syno Connect Center does not forward to this address")
	errCustomName    = customCode("custom_name_invalid", "Enter a name of 1 to 64 characters")
	errCustomRisk    = customCode("custom_risk_unconfirmed", "Confirm that the App handles sign-in itself")
	errCustomLimit   = customCode("custom_limit", "Remove a custom App before adding another")
	errCustomMissing = customCode("custom_not_found", "This custom App was removed")
	errCustomPrivate = customCode("custom_public_only", "Custom Apps open with Free link or your own domain")
)

// ParseTarget accepts an http(s) origin with an optional canonical start path and
// returns it normalized. IP literals in refused ranges are rejected here;
// names are checked again on every dial, after resolution.
func ParseTarget(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 512 || strings.ContainsFunc(raw, unicode.IsControl) || strings.ContainsAny(raw, " \\") {
		return nil, errCustomURL
	}
	u, err := url.Parse(raw)
	if err != nil || u.Opaque != "" {
		return nil, errCustomURL
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return nil, errCustomScheme
	}
	if u.User != nil || u.Host == "" {
		return nil, errCustomURL
	}
	if !validTargetPath(u) || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.Contains(raw, "#") {
		return nil, errCustomPath
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if strings.HasSuffix(u.Host, ":") {
		return nil, errCustomURL
	}
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[scheme]
	}
	n, err := strconv.Atoi(port)
	if err != nil || !validPort(n) || strconv.Itoa(n) != port {
		return nil, errCustomURL
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if refusedIP(ip) {
			return nil, errCustomRefused
		}
		host = ip.Unmap().String()
	} else if len(host) > 253 || !hostPattern.MatchString(host) {
		return nil, errCustomURL
	}
	out := &url.URL{Scheme: scheme, Host: net.JoinHostPort(host, port)}
	if u.Path != "/" {
		out.Path = u.Path
	}
	if port == map[string]string{"http": "80", "https": "443"}[scheme] {
		out.Host = host
		if strings.Contains(host, ":") {
			out.Host = "[" + host + "]"
		}
	}
	return out, nil
}

// A start path is navigation within the approved origin, not path confinement.
// Keep canonical plain paths so URL normalization cannot change the selection.
func validTargetPath(u *url.URL) bool {
	p := u.Path
	return p == "" || p == "/" || u.RawPath == "" && strings.HasPrefix(p, "/") && !strings.Contains(p, "//") && !strings.ContainsAny(p, "\\%") && !strings.ContainsFunc(p, unicode.IsControl) && path.Clean(p) == strings.TrimSuffix(p, "/")
}

// refusedIP: link-local (cloud metadata lives there), unspecified, multicast
// and broadcast never become targets. Loopback and private LAN are allowed.
func refusedIP(ip netip.Addr) bool {
	ip = ip.Unmap()
	return !ip.IsValid() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() || ip == netip.AddrFrom4([4]byte{255, 255, 255, 255}) || ip == metadataIPv6
}

func (c CustomApp) target() (*url.URL, error) { return ParseTarget(c.URL) }

// Port is the target's TCP port, the binding's port field.
func (c CustomApp) Port() int {
	u, err := c.target()
	if err != nil {
		return 0
	}
	port, _ := strconv.Atoi(u.Port())
	if port == 0 {
		port = map[string]int{"http": 80, "https": 443}[u.Scheme]
	}
	return port
}

// Digest binds an approval to the forwarding settings. The name is display
// only and changes nothing about what is reachable.
func (c CustomApp) Digest() string {
	raw, _ := json.Marshal(struct {
		URL         string
		AppSignIn   bool
		InsecureTLS bool
		Ingress     *IngressGrant `json:",omitempty"`
	}{c.URL, c.AppSignIn, c.InsecureTLS, c.Ingress})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func validCustomName(name string) bool {
	n := utf8.RuneCountInString(name)
	return utf8.ValidString(name) && n >= 1 && n <= 64 && strings.TrimSpace(name) == name && !strings.ContainsFunc(name, unicode.IsControl)
}

func (c CustomApp) valid() bool {
	u, err := c.target()
	publicationValid := c.Published == nil || ValidProvider(c.Published.Provider) &&
		socketPattern.MatchString(c.Published.Socket) && validAuthority(c.Published.Authority, false)
	return err == nil && u.String() == c.URL && validCustomName(c.Name) && (!c.InsecureTLS || u.Scheme == "https") &&
		(c.Ingress == nil || c.Ingress.valid(c)) && publicationValid
}
