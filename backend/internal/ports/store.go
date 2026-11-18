package ports

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"
)

type custom struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Protocol  Protocol `json:"protocol"`
	Port      int      `json:"port"`
	Enabled   bool     `json:"enabled"`
	Confirmed bool     `json:"confirmed"`
}
type settings struct {
	Schema         int      `json:"schema"`
	DSMHTTP        bool     `json:"dsmHTTP"`
	DSMHTTPS       bool     `json:"dsmHTTPS"`
	Revision       uint64   `json:"revision"`
	Custom         []custom `json:"custom"`
	FollowServices bool     `json:"followServices,omitempty"`
}

// Store is safe across independently instantiated CGI processes. Only desired
// settings are persisted: never a detected port, session, token or provider state.
type Store struct {
	Dir      string
	Services *ServiceDetector
}

var customID = regexp.MustCompile(`^custom-[a-f0-9]{32}$`)

func validName(name string) bool {
	if !utf8.ValidString(name) || strings.TrimSpace(name) != name || utf8.RuneCountInString(name) < 1 || utf8.RuneCountInString(name) > 80 {
		return false
	}
	for _, c := range name {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}
func validCustom(c custom) bool {
	return customID.MatchString(c.ID) && validName(c.Name) && (c.Protocol == HTTP || c.Protocol == HTTPS || c.Protocol == TCP) && c.Port >= 1 && c.Port <= 65535 && (!c.Enabled || c.Confirmed)
}
func (s Store) locked(ctx context.Context, fn func(*settings) (bool, error)) error {
	lock, e := os.OpenFile(filepath.Join(s.Dir, "ports.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if e != nil {
		return ErrSettings
	}
	defer lock.Close()
	st, e := lock.Stat()
	if e != nil || !st.Mode().IsRegular() {
		return ErrSettings
	}
	for {
		if e = ctx.Err(); e != nil {
			return e
		}
		e = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if e == nil {
			break
		}
		if e != syscall.EWOULDBLOCK && e != syscall.EAGAIN {
			return ErrSettings
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	path := filepath.Join(s.Dir, "ports.json")
	p, e := load(path)
	if e != nil {
		return e
	}
	changed, e := fn(&p)
	if e != nil {
		return e
	}
	if !changed {
		return nil
	}
	if p.Revision == ^uint64(0) {
		return ErrSettings
	}
	p.Revision++
	raw, e := json.Marshal(p)
	if e != nil {
		return ErrSettings
	}
	if e = atomicSave(path, raw); e != nil {
		return ErrSettings
	}
	return nil
}

// load reads and validates desired settings; a missing file is the default.
func load(path string) (settings, error) {
	p := settings{Schema: 1, Custom: []custom{}}
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(e, os.ErrNotExist) {
		return p, nil
	}
	if e != nil {
		return p, ErrSettings
	}
	defer f.Close()
	p = settings{}
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() > 65536 || st.Mode().Perm()&0077 != 0 {
		return p, ErrSettings
	}
	raw, e := io.ReadAll(io.LimitReader(f, 65537))
	if e != nil {
		return p, ErrSettings
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var extra any
	if dec.Decode(&p) != nil || dec.Decode(&extra) != io.EOF || p.Schema != 1 || p.Custom == nil || len(p.Custom) > MaxCustom {
		return p, ErrSettings
	}
	ids := map[string]bool{}
	ports := map[int]bool{}
	for _, c := range p.Custom {
		if !validCustom(c) || ids[c.ID] || ports[c.Port] {
			return p, ErrSettings
		}
		ids[c.ID] = true
		ports[c.Port] = true
	}
	return p, nil
}
func atomicSave(path string, raw []byte) error {
	f, e := os.CreateTemp(filepath.Dir(path), ".ports-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(raw); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return e
	}
	dir, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer dir.Close()
	return dir.Sync()
}
func snapshot(p settings, d DSMPorts, known bool) Snapshot {
	v := Snapshot{Entries: []Entry{}, Detection: "ready", MappingState: "pending-provider", Revision: p.Revision, Plan: Plan{Mappings: []Mapping{}}}
	if !known {
		v.Detection = "needs-attention"
	}
	add := func(id, name string, protocol Protocol, port int, source string, enabled bool, conflict bool) {
		state := "disabled"
		if enabled {
			state = "ready"
		}
		if !known {
			state = "needs-attention"
		} else if conflict {
			state = "conflict"
		}
		var number *int
		if source == "custom" || known {
			n := port
			number = &n
		}
		v.Entries = append(v.Entries, Entry{id, name, protocol, number, source, enabled, state})
		if state == "ready" {
			v.Plan.Mappings = append(v.Plan.Mappings, Mapping{id, protocol, port, source, "127.0.0.1"})
		}
	}
	add(DSMHTTP, "DSM HTTP", HTTP, d.HTTP, "dsm", p.DSMHTTP, false)
	add(DSMHTTPS, "DSM HTTPS", HTTPS, d.HTTPS, "dsm", p.DSMHTTPS, false)
	for _, c := range p.Custom {
		add(c.ID, c.Name, c.Protocol, c.Port, "custom", c.Enabled, known && (c.Port == d.HTTP || c.Port == d.HTTPS))
	}
	return v
}

// List reads without the writer lock: writers replace ports.json atomically,
// so a slow writer fsync on a loaded NAS never delays ports.list or the monitor.
func (s Store) List(ctx context.Context, d Detector) (Snapshot, error) {
	if e := ctx.Err(); e != nil {
		return Snapshot{}, e
	}
	p, e := load(filepath.Join(s.Dir, "ports.json"))
	if e != nil {
		return Snapshot{}, e
	}
	ports, known := detect(ctx, d)
	v := snapshot(p, ports, known)
	s.followSnapshot(ctx, p, &v)
	s.overlayObservation(&v)
	return v, nil
}
func (s Store) Upsert(ctx context.Context, d Detector, in Input) (Snapshot, error) {
	// DSM detection is independent of the settings; keep it out of the lock.
	ports, known := detect(ctx, d)
	var v Snapshot
	var followed bool
	e := s.locked(ctx, func(p *settings) (bool, error) {
		if !validName(in.Name) || (in.Protocol != HTTP && in.Protocol != HTTPS && in.Protocol != TCP) || in.Port < 1 || in.Port > 65535 {
			return false, ErrValidation
		}
		if known && (in.Port == ports.HTTP || in.Port == ports.HTTPS) {
			return false, ErrConflict
		}
		index := -1
		for i, c := range p.Custom {
			if c.ID == in.ID {
				index = i
			}
			if c.ID != in.ID && c.Port == in.Port {
				return false, ErrConflict
			}
		}
		if in.ID != "" && index < 0 {
			return false, ErrValidation
		}
		if index < 0 && len(p.Custom) >= MaxCustom {
			return false, ErrValidation
		}
		consent := index >= 0 && p.Custom[index].Confirmed && p.Custom[index].Enabled && p.Custom[index].Port == in.Port && p.Custom[index].Protocol == in.Protocol
		if in.Enabled && (!known || (!in.ConfirmExposure && !consent)) {
			if !known {
				return false, ErrDetection
			}
			return false, ErrConfirmation
		}
		id := in.ID
		if id == "" {
			raw := make([]byte, 16)
			if _, e := rand.Read(raw); e != nil {
				return false, ErrSettings
			}
			id = "custom-" + hex.EncodeToString(raw)
		}
		c := custom{id, in.Name, in.Protocol, in.Port, in.Enabled, in.Enabled && (in.ConfirmExposure || consent)}
		if index < 0 {
			p.Custom = append(p.Custom, c)
		} else {
			p.Custom[index] = c
		}
		v = snapshot(*p, ports, known)
		followed = p.FollowServices
		v.Revision = p.Revision + 1
		return true, nil
	})
	if e == nil {
		s.followSnapshot(ctx, settings{FollowServices: followed}, &v)
	}
	return v, e
}
func (s Store) SetDSM(ctx context.Context, d Detector, id string, enabled, confirm bool) (Snapshot, error) {
	ports, known := detect(ctx, d)
	var v Snapshot
	var followed bool
	e := s.locked(ctx, func(p *settings) (bool, error) {
		if id != DSMHTTP && id != DSMHTTPS {
			return false, ErrValidation
		}
		if enabled {
			if !confirm {
				return false, ErrConfirmation
			}
			if !known {
				return false, ErrDetection
			}
		}
		if id == DSMHTTP {
			p.DSMHTTP = enabled
		} else {
			p.DSMHTTPS = enabled
		}
		v = snapshot(*p, ports, known)
		followed = p.FollowServices
		v.Revision = p.Revision + 1
		return true, nil
	})
	if e == nil {
		s.followSnapshot(ctx, settings{FollowServices: followed}, &v)
	}
	return v, e
}
func (s Store) Delete(ctx context.Context, id string) (bool, error) {
	if !customID.MatchString(id) {
		return false, ErrValidation
	}
	removed := false
	e := s.locked(ctx, func(p *settings) (bool, error) {
		for i, c := range p.Custom {
			if c.ID == id {
				p.Custom = append(p.Custom[:i], p.Custom[i+1:]...)
				removed = true
				break
			}
		}
		if !removed {
			return false, ErrValidation
		}
		return true, nil
	})
	return removed, e
}
