package cfaccess

import (
	"encoding/json"
	"net/mail"
	"sort"
	"strconv"
	"strings"
	"time"
)

func fmtInt(n int) string { return strconv.Itoa(n) }
func Emails(values []string) ([]string, error) {
	if len(values) > 100 {
		return nil, failure("access_invalid_email")
	}
	seen := map[string]bool{}
	out := []string{}
	for _, v := range values {
		if strings.ContainsAny(v, "\r\n\x00*") {
			return nil, failure("access_invalid_email")
		}
		v = strings.TrimSpace(v)
		parsed, e := mail.ParseAddress(v)
		if e != nil || parsed.Address != v || parsed.Name != "" {
			return nil, failure("access_invalid_email")
		}
		local, domain, ok := strings.Cut(v, "@")
		if !ok || local == "" || !validHost(strings.ToLower(domain)) {
			return nil, failure("access_invalid_email")
		}
		v = local + "@" + strings.ToLower(domain)
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out, nil
}
func validHost(host string) bool {
	if host != strings.ToLower(host) || len(host) > 253 || !strings.Contains(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
func desiredApp(name, host, otp string, emails []string, policyID string) Application {
	rules := []map[string]any{}
	for _, v := range emails {
		rules = append(rules, map[string]any{"email": map[string]any{"email": v}})
	}
	return Application{Name: name, Type: "self_hosted", Domain: host, AllowedIDPs: []string{otp}, AutoRedirect: true, Launcher: false, Session: DefaultSessionDuration, Policies: []Policy{{ID: policyID, Name: name + " email allow list", Decision: "allow", Precedence: 1, Include: rules, Exclude: []map[string]any{}, Require: []map[string]any{}}}}
}
func canonical(v Application) string {
	v.AllowedIDPs = append([]string(nil), v.AllowedIDPs...)
	v.Policies = append([]Policy(nil), v.Policies...)
	// Cloudflare derives this exact destination from domain on readback.
	if len(v.Destinations) == 1 && len(v.Destinations[0]) == 2 && v.Destinations[0]["type"] == "public" && v.Destinations[0]["uri"] == v.Domain {
		v.Destinations = nil
	}
	// Session length belongs to the user, not policy drift repair.
	v.Session = ""
	v.ID = ""
	v.AUD = ""
	sort.Strings(v.AllowedIDPs)
	for i := range v.Policies {
		v.Policies[i].ID = ""
		p := &v.Policies[i]
		for _, r := range []*[]map[string]any{&p.Include, &p.Exclude, &p.Require} {
			*r = append([]map[string]any{}, (*r)...)
			sort.Slice(*r, func(i, j int) bool {
				a, _ := json.Marshal((*r)[i])
				b, _ := json.Marshal((*r)[j])
				return string(a) < string(b)
			})
		}
	}
	raw, _ := json.Marshal(v)
	return string(raw)
}
func overlaps(domain, host string) bool {
	base, _, _ := strings.Cut(strings.ToLower(domain), "/")
	if base == host {
		return true
	}
	if base == "*" {
		return true
	}
	if strings.HasPrefix(base, "*.") {
		root := strings.TrimPrefix(base, "*.")
		return host == root || strings.HasSuffix(host, "."+root)
	}
	return false
}

func ownedDestination(a Application, host string) bool {
	return len(a.Destinations) == 0 || len(a.Destinations) == 1 && len(a.Destinations[0]) == 2 && a.Destinations[0]["type"] == "public" && a.Destinations[0]["uri"] == host
}

// Cloudflare accepts Go duration strings; application sessions range up to one
// month (730h). Keep dashboard values outside our small UI preset list intact.
const DefaultSessionDuration = "720h"

func validSessionDuration(value string) bool {
	d, e := time.ParseDuration(value)
	return e == nil && d >= 0 && d <= 730*time.Hour && len(value) <= 64
}
func sessionDuration(value string) string {
	if validSessionDuration(value) {
		return value
	}
	return DefaultSessionDuration
}
func sessionChoice(value string) bool {
	switch value {
	case "15m", "30m", "1h", "6h", "12h", "24h", "168h", "720h", "730h":
		return true
	}
	return false
}
