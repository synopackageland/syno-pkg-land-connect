package binaries

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"dsmapp/internal/provider"
)

// RuntimeAdapter pins a verified generation for each product. Refresh must be
// called by the provider under its selecting lock, while its old child is
// stopped. Separate Resolve calls for tailscale and tailscaled then use one pair.
type RuntimeAdapter struct {
	manager  *Manager
	mu       sync.Mutex
	selected map[string]saved
}

var _ provider.BinaryManager = (*RuntimeAdapter)(nil)

func NewRuntimeAdapter(m *Manager) *RuntimeAdapter {
	return &RuntimeAdapter{manager: m, selected: map[string]saved{}}
}
func productFor(name string) (string, error) {
	switch name {
	case "cloudflared", "netbird", "netbird-server":
		return name, nil
	case "tailscale", "tailscaled":
		return "tailscale", nil
	}
	return "", errors.New("unknown binary")
}
func (a *RuntimeAdapter) Resolve(ctx context.Context, name string) (provider.Binary, error) {
	product, e := productFor(name)
	if e != nil {
		return provider.Binary{}, e
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	s, ok := a.selected[product]
	initial := false
	if !ok {
		record, err := a.readSelection(product)
		if errors.Is(err, os.ErrNotExist) {
			s, e = a.manager.read(product)
			initial = true
		} else {
			s = record.Current
			e = err
		}
		if e != nil {
			return provider.Binary{}, e
		}
	}
	paths, e := a.verify(ctx, s)
	if e != nil {
		return provider.Binary{}, e
	}
	if initial {
		if e = atomicJSON(a.selectionPath(product), selection{Current: s}); e != nil {
			return provider.Binary{}, e
		}
	}
	a.selected[product] = s
	return provider.Binary{Path: paths[name], Version: s.Current}, nil
}
func (a *RuntimeAdapter) Version(ctx context.Context, name string) (string, error) {
	b, e := a.Resolve(ctx, name)
	return b.Version, e
}
func (a *RuntimeAdapter) Refresh(ctx context.Context, product string) error {
	if e := validProduct(product); e != nil {
		return e
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	s, e := a.manager.read(product)
	if e != nil {
		return e
	}
	if _, e = a.verify(ctx, s); e != nil {
		return e
	}
	record, err := a.readSelection(product)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if record.Current.CurrentDir != s.CurrentDir {
		if e = atomicJSON(a.selectionPath(product), selection{Current: s, Previous: record.Current}); e != nil {
			return e
		}
	}
	a.selected[product] = s
	return nil
}
func (a *RuntimeAdapter) verify(ctx context.Context, s saved) (map[string]string, error) {
	if s.Current == "" || s.Verification == "" {
		return nil, errors.New("verified binary unavailable")
	}
	dir, e := a.manager.directory(s.CurrentDir)
	if e != nil {
		return nil, e
	}
	names := []string{s.Product}
	if s.Product == "tailscale" {
		names = append(names, "tailscaled")
	}
	paths := map[string]string{}
	for _, n := range names {
		path := filepath.Join(dir, n)
		hash, e := fileDigest(ctx, path)
		if e != nil || hash != s.Digests[n] {
			return nil, errors.New("installed binary verification failed")
		}
		paths[n] = path
	}
	return paths, nil
}
func fileDigest(ctx context.Context, path string) (string, error) {
	if e := ctx.Err(); e != nil {
		return "", e
	}
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return "", e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil {
		return "", e
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || info.Size() <= 0 || info.Size() > 512<<20 {
		return "", errors.New("invalid binary file")
	}
	h := sha256.New()
	if _, e = io.Copy(h, io.LimitReader(f, 512<<20+1)); e != nil {
		return "", e
	}
	if e = ctx.Err(); e != nil {
		return "", e
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// SelectedVersion reports inventory already selected by a provider. Reading UI
// status must never pin or activate the newest downloaded candidate.
func (a *RuntimeAdapter) SelectedVersion(name string) string {
	product, e := productFor(name)
	if e != nil {
		return ""
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if v, ok := a.selected[product]; ok {
		return v.Current
	}
	record, e := a.readSelection(product)
	if e != nil {
		return ""
	}
	return record.Current.Current
}
