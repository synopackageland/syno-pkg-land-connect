package provider

import (
	"dsmapp/internal/transient"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	runtimeMaxBytes   = 8 << 20
	runtimeMaxEntries = 256
	runtimeSweep      = 15 * time.Second
	runtimePartAge    = 5 * time.Minute
)

var errRuntimeBudget = errors.New("provider tmpfs budget exceeded")

type runtimeFile struct {
	path, rel string
	bytes     int64
	modified  time.Time
}

// This scanner accepts only a verified private /tmp tree. In particular, its
// timer never resolves the persistent identity directory or reads its files.
func runtimeFiles(dir string) ([]runtimeFile, int, error) {
	prefix := fmt.Sprintf("SynoPkgConnect-%d-", os.Geteuid())
	if filepath.Dir(dir) != "/tmp" || !strings.HasPrefix(filepath.Base(dir), prefix) {
		return nil, 0, errors.New("invalid provider tmpfs directory")
	}
	if _, err := transient.Directory(strings.TrimPrefix(filepath.Base(dir), prefix), false); err != nil {
		return nil, 0, err
	}
	var files []runtimeFile
	entries := 0
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if path != dir && os.IsNotExist(err) {
			return nil
		}
		if err != nil || path == dir {
			return err
		}
		entries++
		if entries > 4096 {
			return errRuntimeBudget
		}
		// Never traverse a redirected cache or account for a volume target.
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("symlink in provider tmpfs")
		}
		if entry.IsDir() {
			return nil
		}
		st, err := entry.Info()
		if os.IsNotExist(err) { // An upstream atomic rename won the race.
			return nil
		}
		if err != nil {
			return err
		}
		if !st.Mode().IsRegular() {
			return errors.New("unexpected provider tmpfs entry")
		}
		size := st.Size()
		if s, ok := st.Sys().(*syscall.Stat_t); ok && s.Blocks*512 > size {
			size = s.Blocks * 512
		}
		rel, _ := filepath.Rel(dir, path)
		files = append(files, runtimeFile{path, rel, size, st.ModTime()})
		return nil
	})
	return files, entries, err
}

func removeRuntimeFile(p string) error {
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func oldestFirst(files []runtimeFile) {
	sort.Slice(files, func(i, j int) bool {
		if files[i].modified.Equal(files[j].modified) {
			return files[i].rel < files[j].rel
		}
		return files[i].modified.Before(files[j].modified)
	})
}

// Evict certificate/key pairs together; the ACME account itself is retained.
func trimRuntimeCache(files []runtimeFile, prefix string, bytes int64, count int, pairs bool) error {
	type group struct {
		files []runtimeFile
		last  time.Time
	}
	groups := map[string]*group{}
	var total int64
	n := 0
	for _, f := range files {
		if !strings.HasPrefix(f.rel, prefix+"/") {
			continue
		}
		total += f.bytes
		n++
		if pairs && filepath.Base(f.rel) == "acme-account.key.pem" {
			continue
		}
		// Atomic-write parts may still be open. They count toward the session
		// budget, but only completed cache entries may be evicted.
		if runtimePart(f.rel) {
			continue
		}
		key := f.rel
		if pairs && (strings.HasSuffix(key, ".crt") || strings.HasSuffix(key, ".key")) {
			key = strings.TrimSuffix(strings.TrimSuffix(key, ".crt"), ".key")
		}
		g := groups[key]
		if g == nil {
			g = &group{}
			groups[key] = g
		}
		g.files = append(g.files, f)
		if f.modified.After(g.last) {
			g.last = f.modified
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		a, b := groups[keys[i]].last, groups[keys[j]].last
		if a.Equal(b) {
			return keys[i] < keys[j]
		}
		return a.Before(b)
	})
	for _, key := range keys {
		if total <= bytes && n <= count {
			break
		}
		for _, f := range groups[key].files {
			if err := removeRuntimeFile(f.path); err != nil {
				return err
			}
			total -= f.bytes
			n--
		}
	}
	return nil
}

func runtimePart(rel string) bool {
	name := filepath.Base(rel)
	return strings.HasPrefix(name, ".") || atomicRuntimePart(name)
}

func atomicRuntimePart(name string) bool {
	i := strings.LastIndex(name, ".tmp")
	if i < 0 {
		return false
	}
	// os.CreateTemp appends decimal random digits. A domain containing
	// ".tmp" is not a temporary file.
	for _, c := range name[i+4:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func runtimeUsage(files []runtimeFile) int64 {
	var total int64
	for _, f := range files {
		total += f.bytes
	}
	return total
}

func runtimeOver(files []runtimeFile, entries int) bool {
	return runtimeUsage(files) > runtimeMaxBytes || entries > runtimeMaxEntries
}

// The normal cache limits are retention targets, not a reason to cut access.
// Under overall pressure, sacrifice more regenerable cache and old backups
// before considering a stop. Current state, ACME identity and TKA are retained.
func relieveRuntime(dir, kind string) error {
	files, entries, err := runtimeFiles(dir)
	if err != nil && !errors.Is(err, errRuntimeBudget) {
		return err
	}
	if kind == "tailscale" {
		for _, prefix := range []string{"dns-cache", "certs"} {
			var cacheBytes int64
			cacheCount := 0
			for _, f := range files {
				if strings.HasPrefix(f.rel, prefix+"/") {
					cacheBytes += f.bytes
					cacheCount++
				}
			}
			if runtimeOver(files, entries) {
				// Reduce this cache only as far as necessary; the helper
				// evicts oldest first and preserves certificate pairs.
				if err := trimRuntimeCache(files, prefix,
					max(0, runtimeMaxBytes-(runtimeUsage(files)-cacheBytes)),
					max(0, runtimeMaxEntries-(entries-cacheCount)), prefix == "certs"); err != nil {
					return err
				}
			}
			files, entries, err = runtimeFiles(dir)
			if err != nil && !errors.Is(err, errRuntimeBudget) {
				return err
			}
		}
	}
	oldestFirst(files)
	remaining := files
	for _, f := range files {
		if !runtimeOver(remaining, entries) && err == nil {
			return nil
		}
		safe := kind == "netbird" && strings.HasPrefix(filepath.Base(f.rel), "state.json.corrupted.")
		safe = safe || kind == "tailscale" && f.rel == "derpmap.cached.json"
		if !safe {
			continue
		}
		if err := removeRuntimeFile(f.path); err != nil {
			return err
		}
		// Rescan after eviction: concurrent atomic writes may have changed
		// both allocated bytes and the entry count.
		current, count, scanErr := runtimeFiles(dir)
		if scanErr != nil && !errors.Is(scanErr, errRuntimeBudget) {
			return scanErr
		}
		remaining, entries, err = current, count, scanErr
		// Keep iteration over the original candidates, but use current
		// accounting when deciding whether another eviction is needed.
		if !runtimeOver(current, count) && scanErr == nil {
			return nil
		}
	}
	files, entries, err = runtimeFiles(dir)
	if err != nil {
		return err
	}
	if runtimeOver(files, entries) {
		return errRuntimeBudget
	}
	return nil
}

func pruneRuntime(dir, kind string, now time.Time) error {
	files, _, err := runtimeFiles(dir)
	if err != nil && !errors.Is(err, errRuntimeBudget) {
		return err
	}
	switch kind {
	case "netbird":
		var backups []runtimeFile
		for _, f := range files {
			name := filepath.Base(f.rel)
			if strings.HasPrefix(name, "state.json.corrupted.") {
				backups = append(backups, f)
			}
			// NetBird CreateTemp(".*"+configFileName) can leave failed
			// writes. Protect recent writes and all current state files.
			if strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".json") && now.Sub(f.modified) > runtimePartAge {
				if err := removeRuntimeFile(f.path); err != nil {
					return err
				}
			}
		}
		oldestFirst(backups)
		for len(backups) > 2 {
			if err := removeRuntimeFile(backups[0].path); err != nil {
				return err
			}
			backups = backups[1:]
		}
	case "tailscale":
		for _, f := range files {
			// Upstream atomicfile uses <name>.tmp<random>. These parts
			// normally clean themselves up; remove only stale leftovers.
			if atomicRuntimePart(filepath.Base(f.rel)) && now.Sub(f.modified) > runtimePartAge {
				if err := removeRuntimeFile(f.path); err != nil {
					return err
				}
			}
		}
		if err := trimRuntimeCache(files, "certs", 2<<20, 64, true); err != nil {
			return err
		}
		if err := trimRuntimeCache(files, "dns-cache", 512<<10, 32, false); err != nil {
			return err
		}
	default:
		return errors.New("unknown provider storage policy")
	}
	return relieveRuntime(dir, kind)
}

type runtimeMonitor struct {
	stop, done chan struct{}
	once       sync.Once
	failed     atomic.Bool
	reason     atomic.Value
}

func (m *runtimeMonitor) failureReason() string {
	if reason, ok := m.reason.Load().(string); ok {
		return reason
	}
	return "tmpfs_budget_exceeded"
}

func watchRuntime(dir, kind string, interval time.Duration, stopWriter func() error, exited <-chan struct{}) *runtimeMonitor {
	m := &runtimeMonitor{stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(m.done)
		select {
		case <-m.stop:
			return
		case <-exited:
			return
		default:
		}
		tick := time.NewTicker(interval)
		defer tick.Stop()
		for {
			select {
			case <-m.stop:
				return
			case <-exited:
				return
			case now := <-tick.C:
				select {
				case <-m.stop:
					return
				case <-exited:
					return
				default:
				}
				if err := pruneRuntime(dir, kind, now); err != nil {
					reason := "tmpfs_storage_unavailable"
					if errors.Is(err, errRuntimeBudget) {
						reason = "tmpfs_budget_exceeded"
					}
					m.reason.Store(reason)
					m.failed.Store(true)
					_ = stopWriter()
					return
				}
			}
		}
	}()
	return m
}

func (m *runtimeMonitor) Close() {
	if m != nil {
		m.once.Do(func() { close(m.stop) })
		<-m.done
	}
}
