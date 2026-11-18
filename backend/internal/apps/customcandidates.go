package apps

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/synopackageland/syno-pkg-land-sdk/dsmservices"
)

// CustomCandidates lists what the add dialog can pre-fill, one per URL: DSM
// packages that declare a web port first, then the services DSM's port
// definitions declare, then other listening sockets, which have no name, so
// the cap of 64 drops them first. DSM has an explicit HTTPS entry; its raw
// listeners, SDK gateway ports, excluded ports and non-web services stay out. Picking a
// candidate only fills the form.
func (d Discovery) CustomCandidates(ctx context.Context, services []dsmservices.Definition, exclude map[int]bool) []Candidate {
	skip := map[int]bool{}
	dsmHTTPS := 0
	for port := range exclude {
		skip[port] = true
	}
	if d.DetectPorts != nil {
		if http, https, err := d.DetectPorts(ctx); err == nil {
			skip[http], skip[https] = true, true
			dsmHTTPS = https
		}
	}
	out := []Candidate{}
	if validPort(dsmHTTPS) && !exclude[dsmHTTPS] {
		out = append(out, Candidate{Name: "DSM", URL: "https://127.0.0.1:" + strconv.Itoa(dsmHTTPS), Source: "synology"})
	}
	seen := map[string]bool{}
	represented := map[int]bool{}
	add := func(c Candidate) {
		u, err := ParseTarget(c.URL)
		if err != nil {
			return
		}
		port := (CustomApp{URL: u.String()}).Port()
		if skip[port] || seen[u.String()] {
			return
		}
		seen[u.String()] = true
		represented[port] = true
		c.URL = u.String()
		out = append(out, c)
	}
	entries, _ := os.ReadDir(d.root())
	sdk := []Candidate{}
	for _, entry := range entries {
		pkg := entry.Name()
		if !packagePattern.MatchString(pkg) || IsCustom(pkg) {
			continue
		}
		if g, err := d.ReadGateway(pkg); err == nil {
			skip[g.Listen.HTTPS] = true
			if g.IngressPath != "" && g.External.Connect != "none" && validPort(dsmHTTPS) {
				port := dsmHTTPS
				{
					out = append(out, Candidate{Package: pkg, Name: d.displayName(pkg), Icon: d.icon(pkg), URL: (PackageEntry{Package: pkg, Port: port, Path: g.IngressPath}).String(), Source: "package"})
				}
			}
			continue
		}
		if c, ok := d.packageCandidate(pkg); ok {
			sdk = append(sdk, c)
		}
	}
	sort.Slice(sdk, func(i, j int) bool { return strings.ToLower(sdk[i].Name) < strings.ToLower(sdk[j].Name) })
	for _, c := range sdk {
		add(c)
	}
	for _, c := range serviceCandidates(services) {
		add(c)
	}
	listen := d.Listening
	if listen == nil {
		listen = ListeningTCP
	}
	listening := listen()
	sort.Slice(listening, func(i, j int) bool { return listening[i].Port < listening[j].Port })
	// A port a definition declares is already a service above, or never
	// serves a web page.
	for _, l := range listening {
		if !represented[l.Port] && !notWeb[l.Port] && (l.Addr.IsLoopback() || l.Addr.IsUnspecified() || l.Addr.IsPrivate()) {
			add(Candidate{URL: "http://127.0.0.1:" + strconv.Itoa(l.Port), Source: "listening"})
		}
	}
	return uniqueCandidates(out)
}

// packageCandidate reads INFO's adminport and adminprotocol, which packages
// set for their "Open" button.
func (d Discovery) packageCandidate(pkg string) (Candidate, bool) {
	raw, err := readFile(filepath.Join(d.root(), pkg, "INFO"), 128<<10, false)
	if err != nil {
		return Candidate{}, false
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || key != "adminport" && key != "adminprotocol" && key != "adminurl" {
			continue
		}
		if unquoted, err := strconv.Unquote(value); err == nil {
			value = unquoted
		}
		values[key] = strings.TrimSpace(value)
	}
	port, err := strconv.Atoi(values["adminport"])
	if err != nil || !validPort(port) {
		return Candidate{}, false
	}
	scheme := "http"
	if values["adminprotocol"] == "https" {
		scheme = "https"
	}
	target := scheme + "://127.0.0.1:" + strconv.Itoa(port)
	if p := values["adminurl"]; p != "" {
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		target += p
	}
	if _, err := ParseTarget(target); err != nil {
		return Candidate{}, false
	}
	return Candidate{Name: d.displayName(pkg), Icon: d.icon(pkg), URL: target, Source: "package"}, true
}

// notWeb holds ports on a NAS that never serve a web page: remote shell,
// file transfer and sharing (SMB, AFP, NFS and its helpers), mail and sieve,
// DNS and its control port, directory, SNMP, rexec, RTSP, block storage,
// backup, sync clients, UPS, PostgreSQL, UPnP and Virtual Machine Manager's
// cluster ports. The pick list offers neither a definition nor a listener on
// one of them.
var notWeb = map[int]bool{21: true, 22: true, 23: true, 25: true, 53: true, 110: true, 111: true, 137: true,
	138: true, 139: true, 143: true, 161: true, 389: true, 445: true, 465: true, 512: true, 548: true,
	554: true, 587: true, 636: true, 662: true, 873: true, 892: true, 953: true, 993: true, 995: true,
	2049: true, 2385: true, 3260: true, 3261: true, 3262: true, 3263: true, 3264: true, 3265: true,
	3493: true, 4045: true, 4190: true, 5357: true, 5432: true, 5510: true, 5566: true, 6281: true,
	6690: true, 16509: true, 16514: true, 30300: true, 55001: true, 55002: true}

// serviceCandidates turns DSM's port definitions into pick-list entries, one
// per single TCP port: definitions that name HTTP, HTTPS or the web, and
// Container Manager projects, first. A port whose definition names HTTPS
// gets an https URL.
func serviceCandidates(defs []dsmservices.Definition) []Candidate {
	type entry struct {
		c   Candidate
		web bool
	}
	list := []entry{}
	for _, def := range defs {
		text := strings.ToLower(def.ID + " " + def.Title + " " + def.Desc)
		web := strings.Contains(text, "http") || strings.Contains(text, "web") || strings.HasPrefix(def.File, "docker_")
		secure := strings.Contains(strings.ToLower(def.ID+" "+def.Title), "https")
		for _, port := range def.Ports {
			if notWeb[port] {
				continue
			}
			scheme := "http"
			if secure || port == 443 {
				scheme = "https"
			}
			list = append(list, entry{Candidate{Name: serviceName(def), URL: scheme + "://127.0.0.1:" + strconv.Itoa(port), Source: "service", Service: serviceLabel(def)}, web})
		}
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].web != list[j].web {
			return list[i].web
		}
		return strings.ToLower(list[i].c.Name) < strings.ToLower(list[j].c.Name)
	})
	out := make([]Candidate, len(list))
	for i, e := range list {
		out[i] = e.c
	}
	return out
}

// serviceName is the untranslated name; the page prefers DSM's translation.
func serviceName(def dsmservices.Definition) string {
	for _, name := range []string{def.Title, def.Desc, def.ID} {
		if name = strings.TrimSpace(name); name != "" {
			return name
		}
	}
	return ""
}

func serviceLabel(def dsmservices.Definition) *ServiceLabel {
	return &ServiceLabel{ID: def.ID, Title: def.Title, Desc: def.Desc, TitleKey: def.TitleKey, DescKey: def.DescKey}
}
