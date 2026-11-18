// Package accounttoken stores the one credential shared by Workers, Signal
// bridge and optional Access. No feature needs to activate another to use it.
package accounttoken

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"dsmapp/internal/connector"
)

type Credential struct {
	Account string `json:"account"`
	Token   string `json:"token"`
}

func (Credential) String() string   { return "[private account credential]" }
func (Credential) GoString() string { return "[private account credential]" }

var ErrStore = errors.New("account credential unavailable")

type Store struct {
	Path string
	mu   sync.Mutex
}

func (s *Store) Load() (Credential, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.load() }
func (s *Store) load() (Credential, error) {
	var v Credential
	raw, e := connector.ReadPrivate(s.Path)
	if errors.Is(e, os.ErrNotExist) {
		return v, nil
	}
	if e != nil {
		return v, ErrStore
	}
	st, e := os.Lstat(s.Path)
	if e != nil || st.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) || json.Unmarshal(raw, &v) != nil || v.Account == "" || v.Token == "" {
		return Credential{}, ErrStore
	}
	return v, nil
}
func (s *Store) Save(v Credential) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, e := s.load()
	if e != nil {
		return e
	}
	if v.Account == "" || len(v.Token) > 4096 || strings.ContainsAny(v.Token, "\r\n\x00") || v.Token == "" || old.Account != "" && old.Account != v.Account {
		return ErrStore
	}
	dir := filepath.Dir(s.Path)
	if os.MkdirAll(dir, 0700) != nil {
		return ErrStore
	}
	st, e := os.Lstat(dir)
	if e != nil || !st.IsDir() || st.Mode().Perm() != 0700 || st.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return ErrStore
	}
	raw, _ := json.Marshal(v)
	if connector.WritePrivate(s.Path, raw) != nil {
		return ErrStore
	}
	return nil
}
func (s *Store) Remove() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, e := s.load(); e != nil {
		return e
	}
	if e := os.Remove(s.Path); e != nil && !errors.Is(e, os.ErrNotExist) {
		return ErrStore
	}
	d, e := os.Open(filepath.Dir(s.Path))
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return ErrStore
	}
	defer d.Close()
	return d.Sync()
}
