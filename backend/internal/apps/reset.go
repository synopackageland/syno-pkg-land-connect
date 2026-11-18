package apps

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// StateResetAPI starts App URL records over after the state file broke.
type StateResetAPI interface {
	ResetState(context.Context) (StateReset, error)
}

// StateReset names the kept copy of the unreadable records.
type StateReset struct {
	Backup string `json:"backup"`
}

// Reset moves an unreadable state file aside and starts with no Apps, so
// every App stays closed until it is approved again. A readable state is
// never reset. Revisions continue past any the old file could have issued.
func (s Store) Reset(ctx context.Context, now time.Time) (StateReset, error) {
	var out StateReset
	err := s.locked(ctx, func(t *transaction, loadErr error) error {
		if loadErr == nil {
			return ErrInvalid
		}
		path := filepath.Join(s.Dir, "apps-state.json")
		next := uint64(now.Unix()) * 1000
		if raw, e := readFile(path, stateLimit, true); e == nil {
			var partial struct {
				NextRev uint64 `json:"next_rev"`
			}
			if json.Unmarshal(raw, &partial) == nil && partial.NextRev >= next && partial.NextRev < 1<<53 {
				next = partial.NextRev + 1
			}
		}
		out.Backup = "apps-state.corrupt-" + now.UTC().Format("20060102T150405Z") + ".json"
		if e := os.Rename(path, filepath.Join(s.Dir, out.Backup)); e != nil && !os.IsNotExist(e) {
			return e
		}
		t.state = EmptyState()
		t.state.NextRev = next
		return t.save()
	})
	return out, err
}

func (s *Service) ResetState(ctx context.Context) (StateReset, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Lifecycle.Store.Reset(ctx, time.Now())
}
func (c Client) ResetState(ctx context.Context) (out StateReset, err error) {
	err = c.exchange(ctx, "resetstate", struct{}{}, &out)
	return
}
