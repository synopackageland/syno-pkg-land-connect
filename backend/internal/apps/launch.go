package apps

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// LaunchFile sits beside the projection. Only the SDK's DSM menu launcher
// reads it, to open an App on the provider the browser used to reach DSM.
const LaunchFile = "launch.json"

type LaunchApp struct {
	Origin string `json:"origin"`
	Port   int    `json:"port"`
	State  string `json:"state"`
}

type LaunchRoute struct {
	Provider string               `json:"provider"`
	DSM      string               `json:"dsm"`
	Apps     map[string]LaunchApp `json:"apps"`
}

type LaunchMap struct {
	Schema int           `json:"schema"`
	Routes []LaunchRoute `json:"routes"`
}

// DSMPublicProvider names the provider of the single public DSM endpoint. The
// schema has one, so DSM is public on a Cloudflare domain or on workers.dev,
// never both at once.
func DSMPublicProvider(authority string) string {
	if strings.HasSuffix(authority, ".workers.dev") {
		return Workers
	}
	return "cloudflare"
}

// Launch maps each DSM endpoint to the Apps approved on the same provider.
// Unlike the projection it includes the private (Tailscale) authorities: those
// names are already public in Certificate Transparency logs, and the launcher
// uses them only to choose a menu destination, never for sign-in. Pins,
// sockets, reasons and cleanup details never escape.
func Launch(state State) LaunchMap {
	m := LaunchMap{Schema: Schema, Routes: []LaunchRoute{}}
	if state.Validate() != nil || state.DSM == nil {
		return m
	}
	if d := state.DSM.Public; d != nil {
		provider := DSMPublicProvider(d.Authority)
		route := LaunchRoute{Provider: provider, DSM: "https://" + d.Authority, Apps: map[string]LaunchApp{}}
		for pkg, app := range state.Apps {
			if s := app.Public; s != nil && publicProvider(s.Provider) == provider && !IsCustom(pkg) {
				route.Apps[pkg] = LaunchApp{Origin: s.Origin, Port: s.Binding.Port, State: s.State}
			}
		}
		m.Routes = append(m.Routes, route)
	}
	if d := state.DSM.Private; d != nil {
		route := LaunchRoute{Provider: "tailscale", DSM: "https://" + d.Authority, Apps: map[string]LaunchApp{}}
		for pkg, app := range state.Apps {
			if s := app.Private; s != nil {
				route.Apps[pkg] = LaunchApp{Origin: "https://" + s.Authority, Port: s.Binding.Port, State: s.State}
			}
		}
		m.Routes = append(m.Routes, route)
	}
	return m
}

func publicProvider(provider string) string {
	if provider == Workers {
		return Workers
	}
	return "cloudflare"
}

// writeLaunchMap is called by WriteProjection, so every projection update
// rewrites the launch map in the same validated directory.
func writeLaunchMap(dir string, state State) error {
	raw, err := json.Marshal(Launch(state))
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, LaunchFile), raw, 0644, nil)
}
