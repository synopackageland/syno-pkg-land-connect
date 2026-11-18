package apps

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type ProjectedApp struct {
	Port   int    `json:"port"`
	Rev    uint64 `json:"rev"`
	Public struct {
		Origin string `json:"origin"`
	} `json:"public"`
}

type Projection struct {
	Schema int `json:"schema"`
	DSM    struct {
		Origin string `json:"origin"`
	} `json:"dsm"`
	Apps map[string]ProjectedApp `json:"apps"`
}

// Project includes paused public scopes, so the gateway's revision changes
// immediately. Private network URLs, pins, sockets and reasons never escape.
func Project(state State) Projection {
	p := Projection{Schema: Schema, Apps: map[string]ProjectedApp{}}
	if state.Validate() != nil {
		return p
	}
	if state.DSM != nil {
		p.DSM.Origin = state.DSM.Origin
	}
	for pkg, app := range state.Apps {
		// Custom Apps have no SDK gateway reading this file.
		if app.Public == nil || IsCustom(pkg) {
			continue
		}
		a := ProjectedApp{Port: app.Public.Binding.Port, Rev: app.Rev}
		a.Public.Origin = app.Public.Origin
		p.Apps[pkg] = a
	}
	return p
}

// WriteProjection uses an explicitly configured path: var/spl/external.json,
// or target/spl/external.json when var cannot be traversed by other packages.
// It never falls back silently after an I/O failure. The menu launch map is
// rewritten beside it on every call.
func WriteProjection(path string, state State) error {
	if err := state.Validate(); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	st, err := os.Lstat(dir)
	if err != nil || !st.IsDir() || st.Mode()&os.ModeSymlink != 0 || st.Mode().Perm()&0022 != 0 {
		return ErrCorrupt
	}
	raw, err := json.Marshal(Project(state))
	if err != nil {
		return err
	}
	if err = atomicWrite(path, raw, 0644, nil); err != nil {
		return err
	}
	return writeLaunchMap(dir, state)
}
