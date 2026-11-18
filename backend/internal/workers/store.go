// Package workers owns only Workers created by this Connect installation.
package workers

import (
	"bytes"
	"dsmapp/internal/accounttoken"
	"dsmapp/internal/connector"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
)

var ErrStore = errors.New("workers private state unavailable")
var accountPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)
var labelPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

type Record struct {
	Previous []string `json:"previous,omitempty"`
	Name     string   `json:"name"`
	Secret   string   `json:"secret"`
	Deployed bool     `json:"deployed"`
}
type privateState struct {
	Schema    int    `json:"schema"`
	Token     string `json:"token"`
	AccountID string `json:"account_id"`
	// Subdomain is empty only while a token saved for the NetBird server
	// waits for the Free link's name, before any Worker of the Free link.
	Subdomain        string            `json:"subdomain"`
	Owner            string            `json:"owner"`
	Records          map[string]Record `json:"records"`
	Invalid          bool              `json:"invalid,omitempty"`
	PendingSubdomain bool              `json:"pending_subdomain,omitempty"`
	Disconnecting    bool              `json:"disconnecting"`
	// FreeLinkOff separates relay activation from the shared account and Signal bridge.
	FreeLinkOff bool `json:"free_link_off,omitempty"`
	// SignalBridge is the NetBird server's Signal Worker and route, kept
	// from before the first change so removal can always find them.
	SignalBridge *SignalBridge `json:"signal_bridge,omitempty"`
}
type Store struct {
	Dir         string
	Credentials *accounttoken.Store
}

func (s Store) prepare() error {
	if err := os.MkdirAll(s.Dir, 0700); err != nil {
		return ErrStore
	}
	st, err := os.Lstat(s.Dir)
	if err != nil || !st.IsDir() || st.Mode().Perm() != 0700 || st.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return ErrStore
	}
	return nil
}
func (s Store) load() (privateState, error) {
	v := privateState{Schema: 1, Records: map[string]Record{}}
	if err := s.prepare(); err != nil {
		return v, err
	}
	f, err := os.OpenFile(filepath.Join(s.Dir, "state.json"), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		if s.Credentials != nil {
			c, e := s.Credentials.Load()
			if e != nil {
				return v, ErrStore
			}
			v.Token = c.Token
			v.AccountID = c.Account
			v.PendingSubdomain = c.Token != ""
		}
		return v, nil
	}
	if err != nil {
		return v, ErrStore
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || st.Size() > 1<<20 || st.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return v, ErrStore
	}
	raw, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if err != nil {
		return v, ErrStore
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var extra any
	if d.Decode(&v) != nil || d.Decode(&extra) != io.EOF {
		return privateState{}, ErrStore
	}
	if s.Credentials != nil {
		c, e := s.Credentials.Load()
		if e != nil {
			return privateState{}, ErrStore
		}
		if v.Token != "" {
			if c.Token == "" {
				if s.Credentials.Save(accounttoken.Credential{Account: v.AccountID, Token: v.Token}) != nil {
					return privateState{}, ErrStore
				}
				c = accounttoken.Credential{Account: v.AccountID, Token: v.Token}
			}
			if c.Account != v.AccountID {
				return privateState{}, ErrStore
			}
			v.Token = c.Token
			if s.save(v) != nil {
				return privateState{}, ErrStore
			}
		} else {
			v.Token = c.Token
			if c.Account != "" && c.Account != v.AccountID {
				return privateState{}, ErrStore
			}
		}
	}
	if v.Schema != 1 || !accountPattern.MatchString(v.AccountID) || !accountPattern.MatchString(v.Owner) || !(labelPattern.MatchString(v.Subdomain) || v.Subdomain == "" && v.PendingSubdomain && len(v.Records) == 0) || v.Token == "" && s.Credentials == nil || v.Records == nil || len(v.Records) > 1025 {
		return privateState{}, ErrStore
	}
	for _, r := range v.Records {
		if len(r.Previous) > 100 {
			return privateState{}, ErrStore
		}
		for _, name := range r.Previous {
			if !labelPattern.MatchString(name) {
				return privateState{}, ErrStore
			}
		}
		if !labelPattern.MatchString(r.Name) || len(r.Secret) != 64 {
			return privateState{}, ErrStore
		}
	}
	return v, nil
}
func (s Store) save(v privateState) error {
	if err := s.prepare(); err != nil {
		return err
	}
	if s.Credentials != nil {
		v.Token = ""
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return ErrStore
	}
	f, err := os.CreateTemp(s.Dir, ".state-*")
	if err != nil {
		return ErrStore
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if f.Chmod(0600) != nil {
		return ErrStore
	}
	if _, err = f.Write(raw); err != nil {
		return ErrStore
	}
	if f.Sync() != nil || f.Close() != nil {
		return ErrStore
	}
	if os.Rename(f.Name(), filepath.Join(s.Dir, "state.json")) != nil {
		return ErrStore
	}
	return s.sync()
}
func (s Store) sync() error {
	d, err := os.Open(s.Dir)
	if err != nil {
		return ErrStore
	}
	defer d.Close()
	if d.Sync() != nil {
		return ErrStore
	}
	return nil
}
func (s Store) remove() error {
	files, err := filepath.Glob(filepath.Join(s.Dir, ".state-*"))
	if err != nil {
		return ErrStore
	}
	for _, path := range files {
		st, err := os.Lstat(path)
		if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || st.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
			return ErrStore
		}
		if os.Remove(path) != nil {
			return ErrStore
		}
	}
	if err := os.Remove(filepath.Join(s.Dir, "state.json")); err != nil && !errors.Is(err, os.ErrNotExist) {
		return ErrStore
	}
	return s.sync()
}

// DisableOffline turns the Free link off in the saved state without starting
// anything, for an uninstall: a reinstall then starts with no relay running.
// The token and the Worker inventory stay, so the page can still delete the
// Workers or turn the Free link on again. Only the one field changes; an
// unreadable state already keeps every relay off, so it is left alone.
func DisableOffline(dir string) error {
	path := filepath.Join(dir, "state.json")
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return ErrStore
	}
	raw, err := io.ReadAll(io.LimitReader(f, 1<<20+1))
	f.Close()
	if err != nil {
		return ErrStore
	}
	var v map[string]json.RawMessage
	if json.Unmarshal(raw, &v) != nil || v == nil {
		return nil
	}
	if string(v["free_link_off"]) == "true" {
		return nil
	}
	v["free_link_off"] = json.RawMessage("true")
	out, _ := json.Marshal(v)
	if connector.WritePrivate(path, out) != nil {
		return ErrStore
	}
	return nil
}
