package ports

import (
	"bytes"
	"context"
	"dsmapp/internal/transient"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"time"
)

const maxObservationBytes = 65536

type observation struct {
	Schema    int       `json:"schema"`
	CheckedAt time.Time `json:"checkedAt"`
	State     string    `json:"state"`
	Revision  uint64    `json:"revision"`
	Plan      Plan      `json:"plan"`
}

func validState(state string) bool {
	switch state {
	case "applied", "debouncing", "pending-provider", "needs-attention", "cleanup-required":
		return true
	}
	return false
}

// RecordObservation publishes only non-secret state. CGI readers accept it only
// for their freshly resolved plan and revision, with a bounded freshness window.
func (s Store) RecordObservation(ctx context.Context, v Snapshot) error {
	if !validState(v.MappingState) {
		return ErrValidation
	}
	// No writer lock: readers accept the observation only for their own
	// revision and plan, so a revision changed meanwhile reads as pending.
	if e := ctx.Err(); e != nil {
		return e
	}
	p, e := load(filepath.Join(s.Dir, "ports.json"))
	if e != nil {
		return e
	}
	if p.Revision != v.Revision {
		return nil
	}
	raw, e := json.Marshal(observation{1, time.Now().UTC(), v.MappingState, v.Revision, clone(v.Plan)})
	if e != nil || len(raw) > maxObservationBytes {
		return ErrSettings
	}
	path, e := s.observationPath(true)
	if e != nil {
		return ErrSettings
	}
	if atomicSave(path, raw) != nil {
		return ErrSettings
	}
	return nil
}

// ClearObservation removes the published state once no connection is on, so
// readers show the plan as waiting for a connection.
func (s Store) ClearObservation() error {
	_, e := s.observationPath(false)
	if os.IsNotExist(e) {
		return nil
	}
	if e != nil {
		return e
	}
	return transient.Clear(s.Dir, "ports")
}
func (s Store) overlayObservation(v *Snapshot) {
	path, e := s.observationPath(false)
	if os.IsNotExist(e) {
		return
	}
	if e != nil {
		v.MappingState = "needs-attention"
		return
	}
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if os.IsNotExist(e) {
		return
	}
	v.MappingState = "needs-attention"
	if e != nil {
		return
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() > 65536 || st.Mode().Perm()&0077 != 0 {
		return
	}
	raw, e := io.ReadAll(io.LimitReader(f, 65537))
	if e != nil {
		return
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var o observation
	var extra any
	if dec.Decode(&o) != nil || dec.Decode(&extra) != io.EOF || o.Schema != 1 || !validState(o.State) {
		return
	}
	now := time.Now()
	if o.CheckedAt.Before(now.Add(-15*time.Second)) || o.CheckedAt.After(now.Add(time.Second)) {
		return
	}
	if o.Revision != v.Revision || !reflect.DeepEqual(o.Plan, v.Plan) {
		v.MappingState = "pending-provider"
		return
	}
	v.MappingState = o.State
}
