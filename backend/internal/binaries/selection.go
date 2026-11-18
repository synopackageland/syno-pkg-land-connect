package binaries

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

var errSelectionUnchanged = errors.New("binary selection already current")

type selection struct{ Current, Previous saved }

func (a *RuntimeAdapter) selectionPath(product string) string {
	return filepath.Join(a.manager.root, product+"-selected.json")
}
func (a *RuntimeAdapter) readSelection(product string) (selection, error) {
	var s selection
	f, e := os.OpenFile(a.selectionPath(product), os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return s, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return s, e
	}
	if !info.Mode().IsRegular() || info.Size() > 32768 {
		return s, errors.New("invalid binary selection")
	}
	raw, e := io.ReadAll(io.LimitReader(f, 32769))
	if e != nil {
		return s, e
	}
	if json.Unmarshal(raw, &s) != nil || s.Current.Product != product || s.Current.Current == "" {
		return s, errors.New("invalid binary selection")
	}
	return s, nil
}

// RestorePreviousSelection lets a provider recover after a failed
// restart. It must first stop its failed child under the selecting lock, then
// restore this pointer and its old process. Downloaded candidates are untouched.
// No previous selection means first activation failed; the provider must remain
// disabled rather than pretend a previous process can be restored.
func (a *RuntimeAdapter) RestorePreviousSelection(ctx context.Context, product string) error {
	if e := validProduct(product); e != nil {
		return e
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	record, e := a.readSelection(product)
	if e != nil {
		return e
	}
	if _, e = a.verify(ctx, record.Previous); e != nil {
		return e
	}
	record.Current, record.Previous = record.Previous, record.Current
	if e = atomicJSON(a.selectionPath(product), record); e != nil {
		return e
	}
	a.selected[product] = record.Current
	return nil
}

// PrepareRefresh captures the exact previous selection, including absence, and
// verifies the new candidate before the provider stops anything. The caller
// holds both the update job lock and provider selecting lock across callbacks.
func (a *RuntimeAdapter) PrepareRefresh(ctx context.Context, product string) (apply func() error, revert func() error, err error) {
	if e := validProduct(product); e != nil {
		return nil, nil, e
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	candidate, e := a.manager.read(product)
	if e != nil {
		return nil, nil, e
	}
	if _, e = a.verify(ctx, candidate); e != nil {
		return nil, nil, e
	}
	previous, e := a.readSelection(product)
	absent := errors.Is(e, os.ErrNotExist)
	if e != nil && !absent {
		return nil, nil, e
	}
	if !absent && previous.Current.CurrentDir == candidate.CurrentDir {
		return nil, nil, errSelectionUnchanged
	}
	apply = func() error {
		a.mu.Lock()
		defer a.mu.Unlock()
		if e := ctx.Err(); e != nil {
			return e
		}
		if _, e := a.verify(ctx, candidate); e != nil {
			return e
		}
		record := selection{Current: candidate, Previous: previous.Current}
		if candidate.CurrentDir == previous.Current.CurrentDir {
			record = previous
		}
		if e := atomicJSON(a.selectionPath(product), record); e != nil {
			return e
		}
		a.selected[product] = candidate
		return nil
	}
	revert = func() error {
		a.mu.Lock()
		defer a.mu.Unlock()
		if absent {
			if e := os.Remove(a.selectionPath(product)); e != nil && !os.IsNotExist(e) {
				return e
			}
			delete(a.selected, product)
			return syncDirectory(a.manager.root)
		}
		if _, e := a.verify(context.Background(), previous.Current); e != nil {
			return e
		}
		if e := atomicJSON(a.selectionPath(product), previous); e != nil {
			return e
		}
		a.selected[product] = previous.Current
		return nil
	}
	return apply, revert, nil
}
