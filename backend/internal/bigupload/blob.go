package bigupload

import (
	"hash"
	"os"
	"sync"
	"time"
)

// slot is one fixed-size range of a file part. Bytes are accepted only where
// the slot continues, so the slot is hashed while it is written.
type slot struct {
	fill     int64
	h        hash.Hash // while the slot fills; created on its first byte
	sum      []byte
	gen      uint64
	verified bool // complete and written or confirmed in this attachment
}

// blob is the temp file of one file part. It is referenced by at most one
// active session; a failed session leaves it retained for reuse.
type blob struct {
	m     *Manager
	owner string
	key   string
	size  int64
	path  string
	f     *os.File

	mu      sync.Mutex
	slots   []slot
	written int64 // bytes accepted, all slots
	poke    func()

	once  sync.Once
	timer *time.Timer // retention timer while not attached
}

func (b *blob) slotLen(i int) int64 {
	s := b.m.cfg.Slot
	return min(s, b.size-int64(i)*s)
}

// attach prepares a retained blob for a new session: complete slots must be
// confirmed again (or rewritten) before DSM reads them; partial ones restart.
func (b *blob) attach(poke func()) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.poke = poke
	for i := range b.slots {
		s := &b.slots[i]
		s.gen++
		s.verified = false
		if s.fill != b.slotLen(i) {
			b.resetLocked(i)
		}
	}
}

func (b *blob) resetLocked(i int) {
	s := &b.slots[i]
	b.written -= s.fill
	b.m.written.Add(-s.fill)
	s.fill, s.h, s.sum, s.verified = 0, nil, nil, false
	s.gen++
}

// contiguous returns how many leading bytes DSM may read.
func (b *blob) contiguous() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.contiguousLocked()
}

func (b *blob) contiguousLocked() int64 {
	var n int64
	for i := range b.slots {
		s := &b.slots[i]
		l := b.slotLen(i)
		if s.fill == l && s.verified {
			n += l
			continue
		}
		if s.fill < l && s.fill > 0 {
			n += s.fill
		}
		break
	}
	return n
}

func (b *blob) complete() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.contiguousLocked() == b.size
}

// haveSlots lists complete slots the client may offer to reuse.
func (b *blob) haveSlots() []int {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := []int{}
	for i := range b.slots {
		if b.slots[i].fill == b.slotLen(i) && b.slots[i].sum != nil {
			out = append(out, i)
		}
	}
	return out
}

// remove deletes the temp file and releases its space exactly once.
func (b *blob) remove() {
	b.once.Do(func() {
		b.mu.Lock()
		if b.timer != nil {
			b.timer.Stop()
		}
		w := b.written
		b.written = 0
		for i := range b.slots {
			b.slots[i].gen++
		}
		b.mu.Unlock()
		_ = b.f.Close()
		_ = os.Remove(b.path)
		b.m.release(b, w)
	})
}
