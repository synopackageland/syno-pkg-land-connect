package apps

import (
	"net/url"
	"path"
	"strconv"
	"strings"
)

// PackageEntry is a resolution of the installed package's declaration, never a
// browser supplied DSM proxy target. Package is stable across upgrades.
type PackageEntry struct {
	Package string `json:"package"`
	Port    int    `json:"port"`
	Path    string `json:"path,omitempty"`
}

func validIngressPath(pkg, p string) bool {
	if p == "/synopkgland/"+pkg+"/" {
		return true
	}
	if strings.Contains(p, "//") || len(p) > 255 || !strings.HasPrefix(p, "/") || !strings.HasSuffix(p, "/") || p == "/" {
		return false
	}
	parts := strings.Split(strings.Trim(p, "/"), "/")
	reserved := map[string]bool{"dsfile": true, "indexdb": true, "mobile": true, "oauth": true, "scripts": true, "sharing": true, "ssdp": true, "synosdsjslib": true, "synohdpack": true, "webapi": true, "webman": true, "calendar": true, "chatplus": true, "contacts": true, "drive": true, "file": true, "photo": true, "sso": true, "synologymeet": true, "synopkgland": true}
	if reserved[parts[0]] {
		return false
	}
	for _, s := range parts {
		if len(s) == 0 || len(s) > 64 || s[0] == '-' || s[len(s)-1] == '-' {
			return false
		}
		for _, c := range s {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

func (e PackageEntry) Valid() bool {
	return packagePattern.MatchString(e.Package) && validPort(e.Port) && (e.Path == "" || validIngressPath(e.Package, e.Path))
}
func (e PackageEntry) PingPath() string {
	if e.Path != "" {
		return e.Path + "_spl/ping"
	}
	return "/_spl/ping"
}
func (e PackageEntry) String() string {
	u := url.URL{Scheme: "https", Host: "127.0.0.1:" + strconv.Itoa(e.Port), Path: e.Path}
	return u.String()
}

// AllowsPath exposes the selected package and its two fixed sign-in handlers.
// Login assets/APIs have a separate method/query constrained policy.
func (e PackageEntry) AllowsPath(p string) bool {
	if e.Path == "" {
		return true
	}
	if strings.ContainsAny(p, "%\\\x00") || path.Clean(p) != strings.TrimSuffix(p, "/") {
		return false
	}
	if p == strings.TrimSuffix(e.Path, "/") || strings.HasPrefix(p, e.Path) {
		return true
	}
	base := "/webman/3rdparty/" + e.Package + "/sso/"
	return p == base+"authorize.cgi" || p == base+"open.cgi"
}

func EntryPath(e *PackageEntry) string {
	if e == nil {
		return ""
	}
	return e.Path
}

type EntryActivity struct {
	Package  string       `json:"package"`
	Scope    string       `json:"scope"`
	Before   PackageEntry `json:"before"`
	After    PackageEntry `json:"after"`
	Revision uint64       `json:"revision"`
}

type EntryChange struct {
	BeforeProtection *Protection  `json:"beforeProtection,omitempty"`
	AfterProtection  *Protection  `json:"afterProtection,omitempty"`
	Before           PackageEntry `json:"before"`
	After            PackageEntry `json:"after"`
	Reason           string       `json:"reason"`
}

type Protection struct {
	Entry               string   `json:"entry,omitempty"`
	Access              string   `json:"access,omitempty"`
	Mode                string   `json:"mode"`
	UpstreamAccess      string   `json:"upstreamAccess,omitempty"`
	Passthrough         []string `json:"passthrough"`
	OptionalPassthrough []string `json:"optionalPassthrough,omitempty"`
}
