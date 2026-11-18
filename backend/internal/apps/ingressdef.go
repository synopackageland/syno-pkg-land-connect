package apps

import (
	"net/http"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// IngressSubject is supplied by the Unix peer verifier, not JSON.
type IngressSubject struct {
	Package string
	UID     int
}

// IngressGrant restricts a custom App to one package-owned HTTP/WS listener.
// UID comes from the authenticated local peer, never from its request body.
type IngressGrant struct {
	Socket          string   `json:"socket,omitempty"`
	AdditionalPaths []string `json:"additionalPaths,omitempty"`
	Package         string   `json:"package"`
	UID             int      `json:"uid"`
	Path            string   `json:"path"`
	Hostname        string   `json:"hostname"`
	Revoked         bool     `json:"revoked,omitempty"`
}

var ingressPackageName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]{0,63}$`)
var ingressLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,38}[a-z0-9])?$`)
var ingressPath = regexp.MustCompile(`^/[A-Za-z0-9_-]+(?:/[A-Za-z0-9_-]+)*$`)

func ValidIngressPath(path string) bool { return len(path) <= 256 && ingressPath.MatchString(path) }

func (g IngressGrant) valid(c CustomApp) bool {
	u, err := c.target()
	return err == nil && packagePattern.MatchString(g.Package) && !IsCustom(g.Package) && g.Package != "SynoPkgConnect" &&
		g.UID > 0 && ValidIngressPath(g.Path) && validAuthority(g.Hostname, false) && strings.Contains(g.Hostname, ".") &&
		c.AppSignIn && !c.InsecureTLS && g.validPaths() && ((g.Socket == "" && u.Scheme == "http" && u.Hostname() == "127.0.0.1" && c.Port() >= 1024) || (g.Socket != "" && g.ValidSocket() && c.URL == "http://package.invalid"))
}

// Accepts rejects alternate encodings, queries and absolute-form requests as
// well as ordinary HTTP. The SSH protocol inside WS authenticates machines.
func (g IngressGrant) Accepts(r *http.Request) bool {
	return !g.Revoked && r.Method == http.MethodGet && !r.URL.IsAbs() && r.URL.Host == "" &&
		(r.URL.Path == g.Path || slices.Contains(g.AdditionalPaths, r.URL.Path)) && r.URL.RawPath == "" && r.URL.RawQuery == "" && !r.URL.ForceQuery &&
		r.RequestURI == r.URL.Path && strings.EqualFold(r.Header.Get("Upgrade"), "websocket") &&
		headerToken(r.Header.Get("Connection"), "upgrade")
}

func headerToken(raw, want string) bool {
	for _, value := range strings.Split(raw, ",") {
		if strings.EqualFold(strings.TrimSpace(value), want) {
			return true
		}
	}
	return false
}

// ValidSocket confines a grant to its owner's dedicated SDK package-peer boundary.
// It never grants access to a private run socket or takes a path from HTTP.
func (g IngressGrant) ValidSocket() bool {
	root := "/var/packages/" + g.Package + "/var"
	dir := filepath.Dir(g.Socket)
	label := strings.TrimPrefix(filepath.Base(dir), "ingress-")
	return filepath.Clean(g.Socket) == g.Socket && filepath.Dir(dir) == root && filepath.Base(g.Socket) == "http.sock" && strings.HasPrefix(filepath.Base(dir), "ingress-") && label != "" && len(label) <= 40 && ingressLabel.MatchString(label)
}
func (g IngressGrant) validPaths() bool {
	if len(g.AdditionalPaths) > 3 {
		return false
	}
	seen := map[string]bool{g.Path: true}
	for _, path := range g.AdditionalPaths {
		if !ValidIngressPath(path) || seen[path] {
			return false
		}
		seen[path] = true
	}
	return true
}
func (g IngressGrant) Paths() []string { return append([]string{g.Path}, g.AdditionalPaths...) }

func (g IngressGrant) ValidDefinition(c CustomApp) bool { return g.valid(c) }
