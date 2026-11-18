// Package uninstallreminder projects only resource names into a read-only DSM wizard.
// It never constructs providers, contacts a server or changes private state.
package uninstallreminder

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
)

//go:embed messages.json
var messages []byte

type copyText struct {
	Title, Intro, Workers, DNS, Tunnels, Devices, Peers                     string
	Kept, AccountToken, TailscaleKey, NetBirdKey, Server, GitHubToken, IPv6 string
}
type item struct {
	Desc string `json:"desc"`
}
type page struct {
	Title string `json:"step_title"`
	Items []item `json:"items"`
}
type inventory struct {
	workers, dns, tunnels []string
	tailscale, netbird    bool
	management            string
	cloudflare, ipv6      bool
	// kept names the credentials and data that stay on the NAS; only their
	// presence is checked, never their content.
	accountToken, tailscaleKey, netbirdKey, server, githubToken bool
}

var label = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,252}$`)
var uuid = regexp.MustCompile(`^[a-fA-F0-9]{8}(?:-[a-fA-F0-9]{4}){3}-[a-fA-F0-9]{12}$`)

// Render preserves the SDK's original pages and replaces only our final reminder.
// An empty dir requests the generic page used in the static fallback.
func Render(base []byte, dir, locale string) ([]byte, error) {
	var catalog map[string]copyText
	if err := json.Unmarshal(messages, &catalog); err != nil {
		return nil, err
	}
	text, ok := catalog[locale]
	if !ok {
		text = catalog["en"]
	}
	var pages []json.RawMessage
	if err := json.Unmarshal(base, &pages); err != nil || len(pages) == 0 {
		return nil, errors.New("wizard unavailable")
	}
	var last page
	if json.Unmarshal(pages[len(pages)-1], &last) == nil && last.Title == text.Title {
		pages = pages[:len(pages)-1]
	}
	v, err := scan(dir)
	if dir == "" || err != nil {
		v = inventory{cloudflare: true, tailscale: true, netbird: true}
	}
	p := page{Title: text.Title, Items: []item{{text.Intro}}}
	link := func(name, href string) string {
		return `<a href="` + html.EscapeString(href) + `" target="_blank" rel="noopener noreferrer">` + html.EscapeString(name) + `</a>`
	}
	names := func(values []string) string {
		sort.Strings(values)
		values = compact(values)
		if len(values) == 0 {
			return ""
		}
		return ": " + html.EscapeString(strings.Join(values, ", "))
	}
	if v.cloudflare {
		p.Items = append(p.Items, item{"Cloudflare — " + link(text.Workers, "https://dash.cloudflare.com/?to=/:account/workers-and-pages") + names(v.workers) + "<br>" + link(text.DNS, "https://dash.cloudflare.com/?to=/:account/:zone/dns/records") + names(v.dns) + "<br>" + link(text.Tunnels, "https://dash.cloudflare.com/?to=/:account/access/tunnels") + names(v.tunnels)})
	}
	if v.tailscale {
		p.Items = append(p.Items, item{"Tailscale — " + link(text.Devices, "https://login.tailscale.com/admin/machines") + ": synopkgconnect"})
	}
	if v.netbird {
		href := "https://app.netbird.io/peers"
		suffix := ""
		if u, err := url.Parse(v.management); err == nil && u.Scheme == "https" && u.User == nil && label.MatchString(u.Hostname()) && u.RawQuery == "" && u.Fragment == "" && (u.Path == "" || u.Path == "/") && u.Hostname() != "api.netbird.io" {
			href = "https://" + u.Host + "/peers"
			suffix = " (" + html.EscapeString(u.Host) + ")"
		}
		p.Items = append(p.Items, item{"NetBird — " + link(text.Peers, href) + suffix})
	}
	if v.ipv6 {
		p.Items = append(p.Items, item{html.EscapeString(text.IPv6)})
	}
	var kept []string
	for _, k := range []struct {
		on   bool
		text string
	}{{v.accountToken, text.AccountToken}, {v.server, text.Server}, {v.tailscaleKey, text.TailscaleKey}, {v.netbirdKey, text.NetBirdKey}, {v.githubToken, text.GitHubToken}} {
		if k.on {
			kept = append(kept, "<li>"+html.EscapeString(k.text)+"</li>")
		}
	}
	if len(kept) > 0 {
		p.Items = append(p.Items, item{html.EscapeString(text.Kept) + "<ul>" + strings.Join(kept, "") + "</ul>"})
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(append(pages, raw), "", "  ")
}
func compact(v []string) []string {
	out := v[:0]
	for _, s := range v {
		if len(out) == 0 || out[len(out)-1] != s {
			out = append(out, s)
		}
	}
	return out
}
func add(dst *[]string, value string) {
	if label.MatchString(value) {
		*dst = append(*dst, value)
	}
}

// read accepts only bounded, private regular files, never follows inventory
// symlinks or blocks on FIFOs, and ignores all fields outside the typed projection.
func read(dir, path string, into any) (bool, error) {
	parts := strings.Split(path, "/")
	current := dir
	for _, part := range parts {
		current = filepath.Join(current, part)
		st, err := os.Lstat(current)
		if os.IsNotExist(err) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return false, errors.New("unsafe state")
		}
	}
	f, err := os.OpenFile(current, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return false, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return false, err
	}
	if !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || st.Size() > 1<<20 {
		return false, errors.New("unsafe state")
	}
	raw, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	if err != nil {
		return false, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	var extra any
	if d.Decode(into) != nil || d.Decode(&extra) != io.EOF {
		return false, errors.New("invalid state")
	}
	return true, nil
}
func scan(dir string) (inventory, error) {
	var v inventory
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() {
		return v, errors.New("state unavailable")
	}
	var workers struct {
		Records map[string]struct {
			Name string `json:"name"`
		} `json:"records"`
		SignalBridge *struct {
			Script string `json:"script"`
		} `json:"signal_bridge"`
	}
	if _, err = read(dir, "workers/state.json", &workers); err != nil {
		return v, err
	}
	for _, r := range workers.Records {
		add(&v.workers, r.Name)
	}
	if workers.SignalBridge != nil {
		add(&v.workers, workers.SignalBridge.Script)
	}
	v.cloudflare = len(v.workers) > 0
	var domain struct {
		Hostname string   `json:"hostname"`
		TunnelID string   `json:"tunnelId"`
		Routed   []string `json:"routed"`
		Services []struct {
			Hostname string `json:"hostname"`
		} `json:"services"`
	}
	found, err := read(dir, "cloudflare/domain/domain.json", &domain)
	if err != nil {
		return v, err
	}
	if found {
		v.cloudflare = true
		add(&v.dns, domain.Hostname)
		for _, s := range domain.Routed {
			add(&v.dns, s)
		}
		for _, s := range domain.Services {
			add(&v.dns, s.Hostname)
		}
		if uuid.MatchString(domain.TunnelID) {
			v.tunnels = append(v.tunnels, domain.TunnelID)
		}
	}
	var cf struct{ Mode, Hostname string }
	if _, err = read(dir, "cloudflare/settings.json", &cf); err != nil {
		return v, err
	}
	if cf.Mode == "domain" || cf.Mode == "token" {
		v.cloudflare = true
		add(&v.dns, cf.Hostname)
	}
	var ts struct{ Settings json.RawMessage }
	v.tailscale, err = read(dir, "tailscale/settings.json", &ts)
	if err != nil {
		return v, err
	}
	var nb struct {
		Settings struct {
			ManagementURL string `json:"managementUrl"`
		}
	}
	v.netbird, err = read(dir, "netbird/settings.json", &nb)
	if err != nil {
		return v, err
	}
	v.management = nb.Settings.ManagementURL
	var server struct {
		Hostname string `json:"hostname"`
		Routed   bool   `json:"routed"`
	}
	v.server, err = read(dir, "netbird-server/state.json", &server)
	if err != nil {
		return v, err
	}
	if server.Routed {
		v.cloudflare = true
		add(&v.dns, server.Hostname)
	}
	var direct struct {
		Mappings []json.RawMessage `json:"mappings"`
	}
	v.ipv6, err = read(dir, "ipv6.json", &direct)
	if err != nil {
		return v, err
	}
	v.ipv6 = v.ipv6 && len(direct.Mappings) > 0
	v.accountToken = present(dir, "account-token", "credential.json")
	v.tailscaleKey = present(dir, "tailscale", "tailscaled.state")
	v.netbirdKey = present(dir, "netbird", "client", "default.json") || present(dir, "netbird", "identities")
	v.githubToken = present(filepath.Dir(dir), "github-token")
	if !v.cloudflare && !v.tailscale && !v.netbird && !v.server && !v.ipv6 {
		return v, errors.New("inventory unavailable")
	}
	return v, nil
}

// present reports whether a path exists without following a symlink or
// reading it: a kept credential is named, never opened.
func present(dir string, parts ...string) bool {
	_, err := os.Lstat(filepath.Join(append([]string{dir}, parts...)...))
	return err == nil
}
