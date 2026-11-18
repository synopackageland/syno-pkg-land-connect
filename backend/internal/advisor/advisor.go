// Package advisor turns a stated use and what the NAS reports into ranked
// connection options. catalog.json is the one description of the options:
// the Overview's comparison table imports the same file, and the ranking here
// answers both the Overview and ctl recommend.
package advisor

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
)

//go:embed catalog.json
var catalogJSON []byte

// Option is one way to reach the NAS. Enumerated values are message keys in
// the UI and self-describing codes in ctl output.
type Option struct {
	ID       string `json:"id"`
	Provider string `json:"provider,omitempty"`
	Mode     string `json:"mode,omitempty"`
	// Tier orders one provider's options from the simplest (1).
	Tier     int            `json:"tier,omitempty"`
	Native   bool           `json:"native,omitempty"`
	Goals    map[string]int `json:"goals"`
	Requires []string       `json:"requires,omitempty"`
	Fit      string         `json:"fit"`
	Reach    string         `json:"reach"`
	// Scope is what one connection opens: one web address, chosen
	// services one by one, or a private network the devices join.
	Scope      string   `json:"scope"`
	Cost       string   `json:"cost"`
	Accounts   []string `json:"accounts"`
	Client     string   `json:"client"`
	Difficulty int      `json:"difficulty"`
	// HTTPS names who provides the browser's certificate, if anyone.
	HTTPS  string   `json:"https"`
	Limits []string `json:"limits"`
	Who    string   `json:"who"`
	Via    string   `json:"via"`
	// Gate is the sign-in in front of the NAS's own: Cloudflare Access email
	// sign-in, DSM sign-in before an app, signing in to the private network,
	// or none.
	Gate     string            `json:"gate"`
	Public   bool              `json:"public"`
	PairWith []string          `json:"pairWith"`
	Links    map[string]string `json:"links,omitempty"`
	// Risk is who reaches what: only your devices, a sign-in page anyone
	// with the address sees, or a service itself.
	Risk string `json:"risk"`
	// Protection is what stands between the internet and the NAS: Cloudflare,
	// a private network, Synology's relay, or none, when services face the
	// internet directly.
	Protection string  `json:"protection"`
	Install    Install `json:"install"`
	Pricing    Pricing `json:"pricing"`
	// Guide is the numbered steps of the method's setup, as message keys;
	// GuideFor replaces them for a use that needs other steps.
	Guide    []string            `json:"guide"`
	GuideFor map[string][]string `json:"guideFor,omitempty"`
}

// Install is what a connecting device needs: nothing but a browser, or one
// app, and whether a computer and a phone can use the method at all.
type Install struct {
	App     string            `json:"app"`
	Desktop string            `json:"desktop"` // yes | no | unknown
	Phone   string            `json:"phone"`
	Links   map[string]string `json:"links,omitempty"` // platform -> official download
}

// Pricing is what may cost money or limit a free plan, from the official
// pages in Links as read in Checked (YYYY-MM).
type Pricing struct {
	Limits  []string          `json:"limits"`
	Links   map[string]string `json:"links"`
	Checked string            `json:"checked"`
}

type Catalog struct {
	Version     string      `json:"version"`
	Goals       []string    `json:"goals"`
	Options     []Option    `json:"options"`
	TCPServices []TCPPreset `json:"tcpServices"`
	// Protections are the official guides for each pairWith advice.
	Protections map[string]string `json:"protections"`
}

// TCPPreset is a well-known NAS service a private network carries on the
// listed providers, and where a connecting computer points its app.
type TCPPreset struct {
	ID        string   `json:"id"`
	Providers []string `json:"providers"`
	Port      int      `json:"port"`
	LocalPort int      `json:"localPort"`
	ConnectTo string   `json:"connectTo"`
	Notes     []string `json:"notes"`
	// Goal is the use that publishes this service first.
	Goal string `json:"goal,omitempty"`
}

var builtIn = func() Catalog {
	c, err := Parse(catalogJSON)
	if err != nil {
		panic("advisor catalog: " + err.Error())
	}
	return c
}()

// Default returns the shipped catalog.
func Default() Catalog { return builtIn }

var (
	risks       = map[string]bool{"own-devices": true, "login-page": true}
	protections = map[string]bool{"cloudflare": true, "private-network": true, "synology-relay": true, "none": true}
	answers     = map[string]bool{"yes": true, "no": true, "unknown": true}
)

// Parse decodes a catalog strictly: unknown fields and goals are errors.
func Parse(raw []byte) (Catalog, error) {
	var c Catalog
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return Catalog{}, err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return Catalog{}, errors.New("trailing data")
	}
	goals := map[string]bool{}
	for _, g := range c.Goals {
		goals[g] = true
	}
	seen := map[string]bool{}
	for _, o := range c.Options {
		if o.ID == "" || seen[o.ID] || o.Fit == "" || o.Reach == "" || o.Scope == "" || o.Cost == "" || o.Client == "" || o.Who == "" || o.Via == "" || o.Gate == "" || o.HTTPS == "" || o.Difficulty < 1 || o.Difficulty > 4 {
			return Catalog{}, fmt.Errorf("invalid option %q", o.ID)
		}
		if !risks[o.Risk] || !protections[o.Protection] || o.Install.App == "" || !answers[o.Install.Desktop] || !answers[o.Install.Phone] || o.Pricing.Checked == "" {
			return Catalog{}, fmt.Errorf("invalid option %q", o.ID)
		}
		seen[o.ID] = true
		for g := range o.Goals {
			if !goals[g] {
				return Catalog{}, fmt.Errorf("option %q names unknown goal %q", o.ID, g)
			}
		}
		for g, steps := range o.GuideFor {
			if _, ok := o.Goals[g]; !ok || len(steps) == 0 {
				return Catalog{}, fmt.Errorf("option %q guides unknown use %q", o.ID, g)
			}
		}
	}
	for _, p := range c.TCPServices {
		if p.ID == "" || p.Port < 1 || p.Port > 65535 || p.LocalPort < 1 || p.LocalPort > 65535 || p.ConnectTo == "" || len(p.Providers) == 0 {
			return Catalog{}, fmt.Errorf("invalid TCP service %q", p.ID)
		}
		seenProviders := map[string]bool{}
		for _, provider := range p.Providers {
			if seenProviders[provider] || (provider != "tailscale" && provider != "netbird") {
				return Catalog{}, fmt.Errorf("invalid TCP provider %q", provider)
			}
			seenProviders[provider] = true
		}
	}
	return c, nil
}

// Facts are what the NAS reports; a nil IPv6Global means it could not tell.
type Facts struct {
	IPv6Global  *bool
	States      map[string]string // provider -> connection state
	Modes       map[string]string // provider -> running mode, such as cloudflare's domain
	DSMHTTPPort int               // 0 when not detected
}

// Step is the next thing to do: a ctl command with its input, or something
// only the user can do. Needs lists what the caller must obtain first.
type Step struct {
	Kind    string         `json:"kind"` // command | user
	Command []string       `json:"command,omitempty"`
	Input   map[string]any `json:"input,omitempty"`
	Needs   []string       `json:"needs,omitempty"`
	Action  string         `json:"action,omitempty"`
	URL     string         `json:"url,omitempty"`
}

type Recommendation struct {
	Option
	Rank        int      `json:"rank"`
	Suitability string   `json:"suitability"` // recommended | alternative | other | unavailable
	Reasons     []string `json:"reasons,omitempty"`
	State       string   `json:"state,omitempty"`
	Next        []Step   `json:"next"`
}

type Result struct {
	CatalogVersion string           `json:"catalogVersion"`
	Goal           string           `json:"goal,omitempty"`
	Options        []Recommendation `json:"options"`
}

// Recommend lists every option: those matching the goal first in their
// catalog rank, the rest in catalog order, unavailable ones last. Nothing is
// enabled; a running method is marked instead of suggested again.
func Recommend(c Catalog, goal string, f Facts) Result {
	out := Result{CatalogVersion: c.Version, Goal: goal, Options: []Recommendation{}}
	for i, o := range c.Options {
		ports := servicePorts(c, goal, o.Provider)
		r := Recommendation{Option: o, Rank: 100 + i, Suitability: "other"}
		if n, ok := o.Goals[goal]; ok && goal != "" {
			r.Rank, r.Suitability = n, "alternative"
		}
		for _, need := range o.Requires {
			if need == "ipv6-global" && f.IPv6Global == nil {
				r.Reasons = append(r.Reasons, "ipv6_unknown")
			}
			if need == "ipv6-global" && f.IPv6Global != nil && !*f.IPv6Global {
				r.Rank, r.Suitability = 1000+i, "unavailable"
				r.Reasons = append(r.Reasons, "ipv6_no_global_address")
			}
		}
		if o.Protection == "none" && r.Suitability == "alternative" {
			r.Reasons = append(r.Reasons, "no_protection")
		}
		if o.Provider != "" {
			r.State = f.States[o.Provider]
			if r.State == "" {
				r.State = "disconnected"
			}
		}
		switch {
		case o.Provider == "cloudflare" && len(ports) == 0:
			// Cloudflare carries web pages only, not a use's TCP service.
			r.Next = []Step{}
		case r.State != "" && r.State != "disconnected":
			r.Reasons = append(r.Reasons, "already_running")
			r.Next = []Step{}
		case r.Suitability != "unavailable":
			r.Next = []Step{start(o)}
		default:
			r.Next = []Step{}
		}
		out.Options = append(out.Options, r)
	}
	sort.SliceStable(out.Options, func(a, b int) bool { return out.Options[a].Rank < out.Options[b].Rank })
	// An option without protection is never the one recommended.
	if len(out.Options) > 0 && out.Options[0].Suitability == "alternative" && out.Options[0].Protection != "none" {
		out.Options[0].Suitability = "recommended"
	}
	return out
}

var connect = []string{"connection", "connect"}

// servicePorts are the TCP services a use publishes; Drive Client when the
// use names none.
func servicePorts(c Catalog, goal, provider string) []int {
	ports := []int{}
	knownGoal := false
	for _, p := range c.TCPServices {
		if p.Goal != "" && p.Goal == goal {
			knownGoal = true
			if contains(p.Providers, provider) {
				ports = append(ports, p.Port)
			}
		}
	}
	if len(ports) == 0 && !knownGoal {
		ports = []int{6690}
	}
	return ports
}

// start is the first step of an option, using the same API as the UI's button.
func start(o Option) Step {
	switch {
	case o.ID == "ddns":
		// DSM's own DDNS: set up in Control Panel, with port forwarding on the router.
		return Step{Kind: "user", Action: "open-dsm-ddns", Needs: []string{"ddns-provider", "router-setup"}}
	case o.Native:
		return Step{Kind: "user", Action: "open-dsm-quickconnect", Needs: []string{"synology-account"}}
	case o.ID == "cloudflare-workers":
		// The Free link starts from the Cloudflare page with an API token; ctl has no command for it.
		return Step{Kind: "user", Action: "open-free-link", Needs: []string{"cloudflare-token"}}
	case o.Provider == "cloudflare" && o.Mode == "quick":
		return Step{Kind: "command", Command: connect, Input: map[string]any{"provider": "cloudflare", "mode": "quick", "confirmExposure": true}, Needs: []string{"exposure-consent"}}
	case o.Provider == "cloudflare":
		return Step{Kind: "command", Command: connect, Input: map[string]any{"provider": "cloudflare", "mode": "domain", "subdomain": "nas", "confirmExposure": true},
			Needs: []string{"exposure-consent", "domain-on-cloudflare", "browser-login"}, URL: o.Links["registrar"]}
	case o.Provider == "tailscale":
		// Peers reach every NAS port at its own Tailscale address: nothing to map.
		return Step{Kind: "command", Command: connect, Input: map[string]any{"provider": "tailscale"}, Needs: []string{"browser-login"}}
	case o.Provider == "netbird":
		return Step{Kind: "command", Command: connect, Input: map[string]any{"provider": "netbird"}, Needs: []string{"browser-login"}}
	default:
		return Step{Kind: "command", Command: connect, Input: map[string]any{"provider": o.Provider, "confirmExposure": true}, Needs: []string{"exposure-consent", "router-setup"}}
	}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
