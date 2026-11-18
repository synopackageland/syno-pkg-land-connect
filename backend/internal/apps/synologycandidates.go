package apps

import (
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// PortalEntry is the read-only Login Portal list in the administrator's DSM
// session. It only supplies form suggestions, never permission or approval.
type PortalEntry struct {
	Icon       string `json:"icon,omitempty"`
	ID         string `json:"id"`
	Name       string `json:"display_name"`
	Alias      string `json:"alias"`
	HTTPPort   int    `json:"http_port"`
	HTTPSPort  int    `json:"https_port"`
	Additional struct {
		DefaultSetting PortalDefaults `json:"default_setting"`
	} `json:"additional"`
}
type PortalDefaults struct {
	Alias string `json:"alias"`
}

// WithSynologyPortals prefers saved alternate ports, then the saved alias or
// factory path on the detected DSM HTTPS origin. Factory ports are not active
// ports. No host from the browser is used; every target stays on loopback.
func WithSynologyPortals(base []Candidate, portals []PortalEntry) []Candidate {
	origin := ""
	for _, c := range base {
		if c.Source == "synology" && c.Name == "DSM" {
			origin = c.URL
			break
		}
	}
	native := []Candidate{}
	for _, p := range portals {
		if p.ID == "" || strings.TrimSpace(p.Name) == "" {
			continue
		}
		target := ""
		switch {
		case validPort(p.HTTPSPort):
			target = "https://127.0.0.1:" + strconv.Itoa(p.HTTPSPort)
		case validPort(p.HTTPPort):
			target = "http://127.0.0.1:" + strconv.Itoa(p.HTTPPort)
		case origin != "":
			alias := p.Alias
			if alias == "" {
				alias = p.Additional.DefaultSetting.Alias
			}
			// Login Portal aliases are a single path segment, never a URL or traversal.
			if alias == "" || strings.ContainsAny(alias, "/\\%?#") || alias == "." || alias == ".." {
				continue
			}
			target = origin + "/" + url.PathEscape(alias) + "/"
		}
		if target == "" {
			continue
		}
		icon := p.Icon
		if !portalIcon.MatchString(icon) || strings.Contains(icon, "..") || strings.Contains(icon, "//") {
			icon = ""
		}
		native = append(native, Candidate{Name: p.Name, Icon: icon, URL: target, Source: "synology"})
	}
	sort.SliceStable(native, func(i, j int) bool { return strings.ToLower(native[i].Name) < strings.ToLower(native[j].Name) })
	// Named portal entries beat generic port definitions; raw listeners stay last.
	all := []Candidate{}
	for _, c := range base {
		if c.Source == "synology" || c.Package != "" {
			all = append(all, c)
		}
	}
	all = append(all, native...)
	for _, c := range base {
		if c.Source != "synology" && c.Package == "" {
			all = append(all, c)
		}
	}
	return uniqueCandidates(all)
}

var portalIcon = regexp.MustCompile(`^/webman/[A-Za-z0-9_./-]+\.png$`)

func uniqueCandidates(list []Candidate) []Candidate {
	out := []Candidate{}
	seen := map[string]bool{}
	for _, c := range list {
		u, err := ParseTarget(c.URL)
		if err != nil {
			continue
		}
		c.URL = u.String()
		if seen[c.URL] {
			continue
		}
		seen[c.URL] = true
		out = append(out, c)
		if len(out) == 64 {
			break
		}
	}
	return out
}
