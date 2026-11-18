package provider

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestSwapPressureFollowsTheSwapInRate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vmstat")
	write := func(n int) {
		if e := os.WriteFile(path, []byte("nr_free_pages 1\npswpin "+strconv.Itoa(n)+"\npswpout 9\n"), 0600); e != nil {
			t.Fatal(e)
		}
	}
	p := &SwapPressure{Path: path, Rate: 256, Hold: time.Hour}
	start := time.Now().Add(-time.Minute)
	write(1000)
	p.Sample(start)
	write(1020)
	p.Sample(start.Add(10 * time.Second))
	if p.High() {
		t.Fatal("an idle swap-in rate counted as pressure")
	}
	write(15020)
	p.Sample(start.Add(20 * time.Second))
	if !p.High() {
		t.Fatal("a swapping NAS did not count as pressure")
	}
	p.Hold = time.Second
	if p.High() {
		t.Fatal("pressure outlived its hold")
	}
	missing := &SwapPressure{Path: filepath.Join(t.TempDir(), "none"), Rate: 1, Hold: time.Hour}
	missing.Sample(start)
	if missing.High() {
		t.Fatal("an unreadable counter counted as pressure")
	}
}
