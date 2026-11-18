package apps

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

const stateLimit = 4 << 20

// Store uses the persistent package DataDir. The flock serializes independent
// CGI and daemon instances, including each operation's runtime side effects.
type Store struct{ Dir string }

func readFile(path string, limit int64, private bool) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() > limit || st.Mode().Perm()&0022 != 0 {
		return nil, ErrCorrupt
	}
	if private && (st.Mode().Perm() != 0600 || st.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid())) {
		return nil, ErrCorrupt
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, ErrCorrupt
	}
	return raw, nil
}

func decode(raw []byte, out any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var extra any
	if d.Decode(out) != nil || d.Decode(&extra) != io.EOF {
		return ErrCorrupt
	}
	return nil
}

// Load returns no approvals on corruption. Callers must retain the error to
// display recovery instead of treating a corrupt file as a writable default.
func (s Store) Load() (State, error) {
	raw, err := readFile(filepath.Join(s.Dir, "apps-state.json"), stateLimit, true)
	if errors.Is(err, os.ErrNotExist) {
		return EmptyState(), nil
	}
	var state State
	if err != nil || decode(raw, &state) != nil || state.Validate() != nil {
		return EmptyState(), ErrCorrupt
	}
	return state, nil
}

// atomicWrite never reports success until the directory entry is durable.
// stage is an unexported fault-injection seam used by subprocess crash tests.
func atomicWrite(path string, raw []byte, mode os.FileMode, stage func(string) error) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".apps-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	step := func(name string) error {
		if stage != nil {
			return stage(name)
		}
		return nil
	}
	if err = f.Chmod(mode); err != nil {
		return err
	}
	if _, err = f.Write(raw); err != nil {
		return err
	}
	if err = step("temporary"); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = step("file-sync"); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	if err = step("rename"); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	if err = dir.Sync(); err != nil {
		return err
	}
	return step("directory-sync")
}

type transaction struct {
	store Store
	state State
}

func (t *transaction) save() error {
	if err := t.state.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(t.state)
	if err != nil || len(raw) > stateLimit {
		return ErrInvalid
	}
	return atomicWrite(filepath.Join(t.store.Dir, "apps-state.json"), raw, 0600, nil)
}

func (s Store) locked(ctx context.Context, fn func(*transaction, error) error) error {
	f, err := os.OpenFile(filepath.Join(s.Dir, "apps-state.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || st.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return ErrCorrupt
	}
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Millisecond):
		}
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	state, loadErr := s.Load()
	return fn(&transaction{store: s, state: state}, loadErr)
}

// Update is the runtime's durable state boundary. A failed load never resets
// revision history. Mutation callbacks allocate revisions with State.Revision.
func (s Store) Update(ctx context.Context, mutate func(*State) error) error {
	return s.locked(ctx, func(t *transaction, err error) error {
		if err != nil {
			return err
		}
		if err = mutate(&t.state); err != nil {
			return err
		}
		return t.save()
	})
}
