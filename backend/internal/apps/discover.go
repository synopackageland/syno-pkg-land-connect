package apps

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

const SDKPing = "0.28.0"
const SDKExt = "0.29.0"
const gatewayLimit = 16 << 10

type ExternalPolicy struct {
	// gateway.json says "optional" or "none"; apps.list says "unavailable"
	// for an App whose SDK has no external policy (before SDK_EXT).
	Connect      string `json:"connect"`
	OriginChange string `json:"origin_change"`
	// SingleOrigin: the App signs in on one address only, set in its own
	// settings (SDK 0.29.1 or later). Turning on its private address first
	// explains that the private URL, App port included, becomes that only
	// address once the administrator saves it in the App's settings.
	SingleOrigin bool `json:"single_origin,omitempty"`
}

type OptionalRule struct {
	Prefixes []string `json:"prefixes"`
}

// Protection describes the App's own settings, not an identity guarantee.
// Keep the exceptions beside the generic login disclosure in every UI.
type Gateway struct {
	IngressPath   string `json:"ingress_path,omitempty"`
	Entry         string `json:"entry,omitempty"`
	Access        string `json:"access,omitempty"`
	Package       string `json:"package"`
	NativeIngress bool   `json:"native_ingress"`
	HasExternal   bool   `json:"-"`
	Listen        struct {
		HTTPS int `json:"https"`
	} `json:"listen"`
	Mode                string                  `json:"mode"`
	UpstreamAccess      string                  `json:"upstream_access"`
	Passthrough         []string                `json:"passthrough"`
	OptionalPassthrough map[string]OptionalRule `json:"optional_passthrough"`
	External            *ExternalPolicy         `json:"external,omitempty"`
}

type App struct {
	Entry                *PackageEntry  `json:"entry,omitempty"`
	DSM                  bool           `json:"dsm,omitempty"`
	Relay                *ScopeView     `json:"relay,omitempty"`
	Media                string         `json:"media,omitempty"`
	Package              string         `json:"package"`
	Name                 string         `json:"name"`
	Port                 int            `json:"port"`
	PingStatus           string         `json:"ping_status"`
	NeedsAppUpdate       bool           `json:"needsUpdate"`
	PublicNeedsAppUpdate bool           `json:"-"`
	External             ExternalPolicy `json:"external"`
	Protection           Protection     `json:"protection"`
	Summary              string         `json:"-"`
	PublicCapable        bool           `json:"publicCapable"`
	Private              *ScopeView     `json:"private,omitempty"`
	Public               *ScopeView     `json:"public,omitempty"`
	Removed              bool           `json:"removed,omitempty"`
	Waiting              bool           `json:"waiting,omitempty"`
	SettingsURL          string         `json:"settingsUrl,omitempty"`
	// Icon: the App's 64-pixel main-menu icon on the DSM origin, from its ui/config.
	Icon string `json:"icon,omitempty"`
	// Custom marks an App the administrator added by URL.
	Custom *CustomView `json:"custom,omitempty"`
}

type Discovery struct {
	PackagesDir string
	DetectPorts func(context.Context) (int, int, error)
	Ping        func(context.Context, string, int) string
	// Known keeps the last DSM ports read, for a list on a busy NAS.
	Known *KnownPorts
	// Listening lists TCP listeners for the custom App pick list; nil reads /proc.
	Listening func() []Listener
}

// KnownPorts remembers DSM's ports between scans. DSM rarely changes them,
// and a list that reuses them still marks every App it could not check.
type KnownPorts struct {
	mu          sync.Mutex
	http, https int
}

func (k *KnownPorts) remember(httpPort, httpsPort int) {
	if k == nil {
		return
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	k.http, k.https = httpPort, httpsPort
}
func (k *KnownPorts) last() (int, int, bool) {
	if k == nil {
		return 0, 0, false
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.http, k.https, k.http != 0
}

func (d Discovery) root() string {
	if d.PackagesDir != "" {
		return d.PackagesDir
	}
	return "/var/packages"
}

// ReadGateway deliberately accepts unrelated SDK fields: old real packages
// include oidc, headers and runtime settings that are not discovery contracts.
// Only the bounded, non-writable final file is opened, with O_NOFOLLOW/fstat.
func (d Discovery) ReadGateway(pkg string) (Gateway, error) {
	var g Gateway
	if !packagePattern.MatchString(pkg) || IsCustom(pkg) {
		return g, ErrInvalid
	}
	raw, err := readFile(filepath.Join(d.root(), pkg, "target", "sso", "gateway.json"), gatewayLimit, false)
	if err != nil {
		return g, err
	}
	if json.Unmarshal(raw, &g) != nil || g.Package != pkg || !g.NativeIngress || !(g.IngressPath != "" && validIngressPath(pkg, g.IngressPath) && g.Listen.HTTPS == 0 || g.IngressPath == "" && validPort(g.Listen.HTTPS)) {
		return Gateway{}, ErrInvalid
	}
	if g.Mode != "oidc" && g.Mode != "header" && g.Mode != "none" && g.Mode != "token" && g.Mode != "account" {
		return Gateway{}, ErrInvalid
	}
	if g.UpstreamAccess != "" && g.UpstreamAccess != "public" {
		return Gateway{}, ErrInvalid
	}
	g.HasExternal = g.External != nil
	if g.External == nil {
		g.External = &ExternalPolicy{Connect: "optional", OriginChange: "none"}
	}
	if g.External.Connect == "" {
		g.External.Connect = "optional"
	}
	if g.External.OriginChange == "" {
		g.External.OriginChange = "none"
	}
	if g.External.Connect != "optional" && g.External.Connect != "none" || g.External.OriginChange != "none" && g.External.OriginChange != "confirm" {
		return Gateway{}, ErrInvalid
	}
	return g, nil
}

func ProtectionOf(g Gateway) (Protection, string) {
	p := Protection{Entry: g.Entry, Access: g.Access, Mode: g.Mode, UpstreamAccess: g.UpstreamAccess, Passthrough: append([]string{}, g.Passthrough...), OptionalPassthrough: []string{}}
	sort.Strings(p.Passthrough)
	for key, value := range g.OptionalPassthrough {
		prefixes := append([]string{}, value.Prefixes...)
		sort.Strings(prefixes)
		_ = key
		p.OptionalPassthrough = append(p.OptionalPassthrough, prefixes...)
	}
	sort.Strings(p.OptionalPassthrough)
	raw, _ := json.Marshal(struct {
		Protection Protection
		Rules      map[string]OptionalRule
	}{p, g.OptionalPassthrough})
	sum := sha256.Sum256(raw)
	return p, hex.EncodeToString(sum[:])
}

// Scan is called at daemon startup and an explicit page open/refresh. It is
// read-only and never persists approvals or creates provider resources.
func (d Discovery) Scan(ctx context.Context) ([]App, error) { return d.scan(ctx, false) }

// ScanList reads the Apps for the list page. A busy NAS never fails the
// whole list: DSM's ports fall back to the last ones read, and an App that
// could not be checked in time waits. Without any DSM ports every App waits.
// Changes keep using Scan, which refuses instead.
func (d Discovery) ScanList(ctx context.Context) ([]App, error) { return d.scan(ctx, true) }

func (d Discovery) scan(ctx context.Context, list bool) ([]App, error) {
	if d.DetectPorts == nil {
		return nil, ErrUnavailable
	}
	httpPort, httpsPort, err := d.DetectPorts(ctx)
	known := err == nil && validPort(httpPort) && validPort(httpsPort) && httpPort != httpsPort
	if known {
		d.Known.remember(httpPort, httpsPort)
	} else if list {
		httpPort, httpsPort, known = d.Known.last()
	}
	if !known && !list {
		return nil, ErrUnavailable
	}
	entries, err := os.ReadDir(d.root())
	if err != nil {
		return nil, err
	}
	if len(entries) > 4096 {
		return nil, ErrInvalid
	}
	var candidates []Gateway
	counts := map[int]int{}
	for _, entry := range entries {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		g, e := d.ReadGateway(entry.Name())
		if e != nil {
			continue
		}
		candidates = append(candidates, g)
		if g.IngressPath == "" {
			counts[g.Listen.HTTPS]++
		}
	}
	out := []App{}
	for _, g := range candidates {
		port := g.Listen.HTTPS
		if g.IngressPath != "" {
			port = httpsPort
		}
		if g.External.Connect == "none" || g.IngressPath == "" && (known && (port == httpPort || port == httpsPort) || counts[port] > 1) {
			continue
		}
		protection, summary := ProtectionOf(g)
		a := App{Entry: &PackageEntry{Package: g.Package, Port: port, Path: g.IngressPath}, Package: g.Package, Name: d.displayName(g.Package), Icon: d.icon(g.Package), Port: port, PublicNeedsAppUpdate: !g.HasExternal, PublicCapable: g.HasExternal, External: *g.External, Protection: protection, Summary: summary}
		if !a.PublicNeedsAppUpdate {
			a.SettingsURL = "/webman/3rdparty/" + g.Package + "/sso/settings.cgi"
		} else {
			// The defaulted "optional" only lets the private scope work; the
			// list says what the UI shows: no public address until updated.
			a.External.Connect = "unavailable"
		}
		out = append(out, a)
	}
	if !known {
		for i := range out {
			out[i].PingStatus, out[i].Waiting = Waiting, true
		}
		return out, nil
	}
	// Bound concurrent diagnostics; an unreachable App must not turn into an
	// update warning or make page-open wait once per installed App.
	ping := d.Ping
	if ping == nil {
		ping = ProbePing
	}
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4)
	for i := range out {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				out[i].PingStatus = Waiting
				return
			}
			defer func() { <-slots }()
			status := ""
			if d.Ping == nil && out[i].Entry.Path != "" {
				status = ProbeEntryPing(ctx, *out[i].Entry)
			} else {
				status = ping(ctx, out[i].Package, out[i].Port)
			}
			if status != Ready && status != NeedsUpdate {
				status = Waiting
			}
			out[i].PingStatus = status
			out[i].Waiting = status == Waiting
			out[i].NeedsAppUpdate = status == NeedsUpdate
		}(i)
	}
	wg.Wait()
	if list {
		return out, nil
	}
	return out, ctx.Err()
}

// iconFile is a ui/config icon template under ui/images: DSM puts the size in for {0}.
var iconFile = regexp.MustCompile(`^images/[A-Za-z0-9_.-]*\{0\}[A-Za-z0-9_.-]*\.png$`)

// icon reads the App's own main-menu entry in ui/config, as DSM does, and
// returns its 64-pixel icon under /webman/3rdparty, or "" for anything else.
func (d Discovery) icon(pkg string) string {
	raw, err := readFile(filepath.Join(d.root(), pkg, "target", "ui", "config"), 64<<10, false)
	if err != nil {
		return ""
	}
	var config struct {
		URL map[string]struct {
			Icon string `json:"icon"`
		} `json:".url"`
	}
	if json.Unmarshal(raw, &config) != nil {
		return ""
	}
	// The SDK keys the entry by package name; an older App has its one entry under another key.
	entry, ok := config.URL[pkg]
	if !ok && len(config.URL) == 1 {
		for _, only := range config.URL {
			entry, ok = only, true
		}
	}
	if !ok || !iconFile.MatchString(entry.Icon) {
		return ""
	}
	return "/webman/3rdparty/" + pkg + "/" + strings.Replace(entry.Icon, "{0}", "64", 1)
}

func (d Discovery) displayName(pkg string) string {
	raw, err := readFile(filepath.Join(d.root(), pkg, "INFO"), 128<<10, false)
	if err != nil {
		return fallbackName(pkg)
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "displayname=") {
			continue
		}
		value := strings.TrimPrefix(line, "displayname=")
		if len(value) > 256 {
			return fallbackName(pkg)
		}
		name, err := strconv.Unquote(value)
		if err != nil || strings.TrimSpace(name) == "" {
			return fallbackName(pkg)
		}
		for _, c := range name {
			if c < 32 || c == 127 {
				return fallbackName(pkg)
			}
		}
		return name
	}
	return fallbackName(pkg)
}

// fallbackName names an App whose INFO is gone (uninstalled) or carries no
// usable display name: a Package Land package name without its SynoPkg
// prefix ("SynoPkgNavidrome" reads "Navidrome"), any other name as it is.
func fallbackName(pkg string) string {
	if name := strings.TrimPrefix(pkg, "SynoPkg"); name != pkg && name != "" {
		return name
	}
	return pkg
}
