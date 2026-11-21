// Package bigupload carries browser uploads larger than Cloudflare's request
// limit: the page sends a large XHR body as pieces, and this package stores
// them in the package's temp directory and writes them, in order, into one
// request to DSM on loopback. See docs/large-uploads.md.
package bigupload

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Config holds the limits; zero values take the documented defaults.
type Config struct {
	Dir         string        // temp directory on the volume, never /tmp
	Slot        int64         // slot size, 25 MiB
	Window      int64         // stream mode: how far ahead of DSM a piece may start, 2 slots
	MaxPerOwner int           // active sessions per DSM session, 8
	MaxTotal    int           // active sessions, 16
	Margin      int64         // free space kept after reservations, 1 GiB
	SessionIdle time.Duration // a session with no request ends, 30 min
	KeepFailed  time.Duration // data of a failed session waits for reuse, 30 min
	EndedKeep   time.Duration // an ended session's answer stays for the page, 2 min
	ResultWait  time.Duration // result long-poll, 25 s
	ReadIdle    time.Duration // a piece body read with no byte ends, 60 s
	MaxAnswer   int64         // DSM answer kept in memory, 1 MiB
	// Free reports the bytes available to the package on Dir's volume.
	Free func(dir string) (int64, error)
}

func (c *Config) defaults() {
	set := func(v *int64, d int64) {
		if *v <= 0 {
			*v = d
		}
	}
	setD := func(v *time.Duration, d time.Duration) {
		if *v <= 0 {
			*v = d
		}
	}
	set(&c.Slot, 25<<20)
	set(&c.Window, 2*c.Slot)
	set(&c.Margin, 1<<30)
	set(&c.MaxAnswer, 1<<20)
	setD(&c.SessionIdle, 30*time.Minute)
	setD(&c.KeepFailed, 30*time.Minute)
	setD(&c.EndedKeep, 2*time.Minute)
	setD(&c.ResultWait, 25*time.Second)
	setD(&c.ReadIdle, 60*time.Second)
	if c.MaxPerOwner <= 0 {
		c.MaxPerOwner = 8
	}
	if c.MaxTotal <= 0 {
		c.MaxTotal = 16
	}
	if c.Free == nil {
		c.Free = statfsFree
	}
}

func statfsFree(dir string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}

// Manager owns every upload of the process. One Manager serves all public
// DSM bindings; each binding supplies its own way to reach DSM.
type Manager struct {
	cfg Config

	mu       sync.Mutex
	sessions map[string]*session
	retained map[string]*blob // owner+key -> data of a failed session
	reserved int64
	written  atomic.Int64
}

// New empties dir (data from a previous run cannot be completed) and returns
// a Manager that keeps its temp files there.
func New(cfg Config) (*Manager, error) {
	cfg.defaults()
	if cfg.Dir == "" {
		return nil, errors.New("bigupload: no temp directory")
	}
	if err := os.RemoveAll(cfg.Dir); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return nil, err
	}
	return &Manager{cfg: cfg, sessions: map[string]*session{}, retained: map[string]*blob{}}, nil
}

// Usage reports reserved and stored bytes and live sessions, for tests and status.
func (m *Manager) Usage() (reserved, written int64, sessions, retained int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reserved, m.written.Load(), len(m.sessions), len(m.retained)
}

// Close cancels every upload and deletes all data.
func (m *Manager) Close() {
	m.mu.Lock()
	all := make([]*session, 0, len(m.sessions))
	for _, s := range m.sessions {
		all = append(all, s)
	}
	keep := make([]*blob, 0, len(m.retained))
	for _, b := range m.retained {
		keep = append(keep, b)
	}
	m.mu.Unlock()
	for _, s := range all {
		s.finish(stateCancelled, "", nil)
		s.drop()
	}
	for _, b := range keep {
		b.remove()
	}
}

func randomID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ownerOf binds an upload to one DSM session: the session cookie and token.
func ownerOf(r *http.Request) string {
	c, err := r.Cookie("id")
	tok := r.Header.Get("X-SYNO-TOKEN")
	if err != nil || c.Value == "" || tok == "" {
		return ""
	}
	h := sha256.Sum256([]byte(c.Value + "\x00" + tok))
	return hex.EncodeToString(h[:])
}

type fileSpec struct {
	Key  string `json:"key"`
	Size int64  `json:"size"`
}

var (
	errSpace = errors.New("not enough space")
	errBusy  = errors.New("too many uploads")
)

// reserve checks the session limits and space, then attaches or creates the
// blobs of a new session. On error nothing stays reserved.
func (m *Manager) reserve(owner string, files []fileSpec) ([]*blob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	active, mine := 0, 0
	for _, s := range m.sessions {
		if s.isActive() {
			active++
			if s.owner == owner {
				mine++
			}
		}
	}
	if active >= m.cfg.MaxTotal || mine >= m.cfg.MaxPerOwner {
		return nil, errBusy
	}
	out := make([]*blob, len(files))
	var need int64
	reuse := map[string]bool{}
	for i, f := range files {
		k := owner + "\x00" + f.Key
		if b := m.retained[k]; b != nil && b.size == f.Size && !reuse[k] {
			reuse[k] = true
			out[i] = b
			continue
		}
		need += f.Size
	}
	free, err := m.cfg.Free(m.cfg.Dir)
	if err != nil {
		return nil, err
	}
	outstanding := m.reserved - m.written.Load()
	if free-outstanding-need < m.cfg.Margin {
		return nil, errSpace
	}
	created := []*blob{}
	for i, f := range files {
		if out[i] != nil {
			continue
		}
		b, err := m.newBlob(owner, f)
		if err != nil {
			for _, c := range created {
				_ = c.f.Close()
				_ = os.Remove(c.path)
			}
			return nil, err
		}
		created = append(created, b)
		out[i] = b
	}
	for _, b := range out {
		if _, isNew := indexOf(created, b); !isNew {
			delete(m.retained, owner+"\x00"+b.key)
			b.mu.Lock()
			if b.timer != nil {
				b.timer.Stop()
				b.timer = nil
			}
			b.mu.Unlock()
		}
	}
	m.reserved += need
	return out, nil
}

func indexOf(list []*blob, b *blob) (int, bool) {
	for i, x := range list {
		if x == b {
			return i, true
		}
	}
	return -1, false
}

func (m *Manager) newBlob(owner string, f fileSpec) (*blob, error) {
	path := filepath.Join(m.cfg.Dir, randomID())
	fh, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, err
	}
	n := int((f.Size + m.cfg.Slot - 1) / m.cfg.Slot)
	b := &blob{m: m, owner: owner, key: f.Key, size: f.Size, path: path, f: fh, slots: make([]slot, n)}
	for i := range b.slots {
		b.slots[i].h = sha256.New()
	}
	return b, nil
}

// release is called once per blob by blob.remove.
func (m *Manager) release(b *blob, written int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reserved -= b.size
	m.written.Add(-written)
	k := b.owner + "\x00" + b.key
	if m.retained[k] == b {
		delete(m.retained, k)
	}
}

// retain keeps the data of a failed session for a new request of the App.
// An older retained blob under the same key is deleted.
func (m *Manager) retain(b *blob) {
	k := b.owner + "\x00" + b.key
	m.mu.Lock()
	old := m.retained[k]
	m.retained[k] = b
	b.mu.Lock()
	b.poke = nil
	b.timer = time.AfterFunc(m.cfg.KeepFailed, func() { m.expire(b) })
	b.mu.Unlock()
	m.mu.Unlock()
	if old != nil && old != b {
		old.remove()
	}
}

// expire deletes retained data that no new request reused in time.
func (m *Manager) expire(b *blob) {
	k := b.owner + "\x00" + b.key
	m.mu.Lock()
	if m.retained[k] != b {
		m.mu.Unlock()
		return
	}
	delete(m.retained, k)
	m.mu.Unlock()
	b.remove()
}

// byNonce finds the owner's session created with nonce.
func (m *Manager) byNonce(owner, nonce string) *session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.byNonceLocked(owner, nonce)
}

func (m *Manager) byNonceLocked(owner, nonce string) *session {
	if nonce == "" {
		return nil
	}
	for _, s := range m.sessions {
		if s.nonce == nonce && s.owner == owner {
			return s
		}
	}
	return nil
}

func (m *Manager) session(id string) *session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[id]
}

func (m *Manager) String() string {
	r, w, s, k := m.Usage()
	return fmt.Sprintf("bigupload reserved=%d written=%d sessions=%d retained=%d", r, w, s, k)
}
