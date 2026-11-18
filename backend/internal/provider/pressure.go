package provider

import (
	"bufio"
	"bytes"
	"context"
	"os"
	"strconv"
	"sync"
	"time"
)

// SwapPressure samples the kernel's swap-in rate. On a NAS short of memory a
// daemon's pages wait seconds each on a disk swap device, so its status reads
// stall for a minute; restarting it then only adds load. High stays true for
// a while after the last busy sample so a repair waits until the NAS settles.
type SwapPressure struct {
	// Path is /proc/vmstat unless a test sets it.
	Path string
	// Rate is the swap-in rate, in pages per second, that counts as pressure.
	Rate float64
	// Hold keeps pressure reported this long after the last busy sample.
	Hold time.Duration
	mu   sync.Mutex
	last uint64
	at   time.Time
	busy time.Time
}

// NewSwapPressure uses 256 pages/s (1 MiB/s with 4 KiB pages) held 2 minutes;
// an idle loaded NAS was measured near 2 pages/s, a swapping one near 1400.
func NewSwapPressure() *SwapPressure {
	return &SwapPressure{Path: "/proc/vmstat", Rate: 256, Hold: 2 * time.Minute}
}

// Run samples every interval until ctx ends.
func (p *SwapPressure) Run(ctx context.Context, interval time.Duration) {
	tick := time.NewTicker(interval)
	defer tick.Stop()
	p.Sample(time.Now())
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			p.Sample(now)
		}
	}
}

// Sample reads the swap-in counter once. An unreadable counter is no evidence.
func (p *SwapPressure) Sample(now time.Time) {
	value, ok := readSwapIns(p.Path)
	if !ok {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.at.IsZero() && value >= p.last && now.After(p.at) {
		if float64(value-p.last)/now.Sub(p.at).Seconds() >= p.Rate {
			p.busy = now
		}
	}
	p.last, p.at = value, now
}

// High reports whether the NAS swapped heavily within Hold.
func (p *SwapPressure) High() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return !p.busy.IsZero() && time.Since(p.busy) < p.Hold
}

func readSwapIns(path string) (uint64, bool) {
	raw, e := os.ReadFile(path)
	if e != nil || len(raw) > 1<<20 {
		return 0, false
	}
	lines := bufio.NewScanner(bytes.NewReader(raw))
	for lines.Scan() {
		name, value, ok := bytes.Cut(lines.Bytes(), []byte(" "))
		if ok && string(name) == "pswpin" {
			n, e := strconv.ParseUint(string(bytes.TrimSpace(value)), 10, 64)
			return n, e == nil
		}
	}
	return 0, false
}
