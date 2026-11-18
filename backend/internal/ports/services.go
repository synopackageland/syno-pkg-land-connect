package ports

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/synopackageland/syno-pkg-land-sdk/dsmservices"
)

const MaxFollowed = 16

type Service struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Port int    `json:"port"`
}
type ServiceScan struct {
	Services   []Service `json:"services"`
	UDPSkipped bool      `json:"udpSkipped"`
	Overflow   bool      `json:"overflow"`
}
type serviceCache struct {
	stat os.FileInfo
	scan ServiceScan
}

// ServiceDetector polls only the system services directory. The cache is memory
// only; every tick readdir/stat checks replacement, mtime, size and inode.
// Declarations describe installation, not running listeners or reachability.
type ServiceDetector struct {
	Dir   string
	mu    sync.Mutex
	cache map[string]serviceCache
	read  func(string) ([]byte, error)
}

var defaultServices ServiceDetector

func (d *ServiceDetector) DetectServices(ctx context.Context) (ServiceScan, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := ServiceScan{Services: []Service{}}
	dir := d.Dir
	if dir == "" {
		dir = "/usr/local/etc/services.d"
	}
	entries, e := os.ReadDir(dir)
	if e != nil || len(entries) > 512 {
		return out, ErrDetection
	}
	next := map[string]serviceCache{}
	seen := map[int]bool{}
	for _, entry := range entries {
		if e := ctx.Err(); e != nil {
			return ServiceScan{}, e
		}
		if !strings.HasSuffix(entry.Name(), ".sc") {
			continue
		}
		st, e := entry.Info()
		if e != nil || !st.Mode().IsRegular() || st.Size() > portConfigLimit {
			return ServiceScan{}, ErrDetection
		}
		cached, ok := d.cache[entry.Name()]
		if !ok || !sameServiceFile(st, cached.stat) {
			path := filepath.Join(dir, entry.Name())
			var raw []byte
			if d.read != nil {
				raw, e = d.read(path)
			} else {
				var f *os.File
				f, e = openPortConfig(path, portConfigLimit)
				if e == nil {
					raw, e = io.ReadAll(io.LimitReader(f, portConfigLimit+1))
					f.Close()
				}
			}
			if e != nil || len(raw) > portConfigLimit {
				return ServiceScan{}, ErrDetection
			}
			scan, err := parseServices(entry.Name(), raw)
			if err != nil {
				return ServiceScan{}, err
			}
			// Refuse a racing replacement; never cache a different file under old metadata.
			after, err := os.Lstat(path)
			if err != nil || !sameServiceFile(st, after) {
				return ServiceScan{}, ErrDetection
			}
			cached = serviceCache{st, scan}
		}
		next[entry.Name()] = cached
		out.UDPSkipped = out.UDPSkipped || cached.scan.UDPSkipped
		out.Overflow = out.Overflow || cached.scan.Overflow
		for _, s := range cached.scan.Services {
			if !seen[s.Port] {
				seen[s.Port] = true
				if len(out.Services) < MaxFollowed+1 {
					out.Services = append(out.Services, s)
				} else {
					out.Overflow = true
				}
			}
		}
	}
	d.cache = next
	out.Overflow = out.Overflow || len(out.Services) > MaxFollowed
	sort.Slice(out.Services, func(i, j int) bool { return out.Services[i].Port < out.Services[j].Port })
	return out, nil
}
func sameServiceFile(a, b os.FileInfo) bool {
	return os.SameFile(a, b) && a.ModTime().Equal(b.ModTime()) && a.Size() == b.Size() && a.Mode() == b.Mode()
}
func parseServices(file string, raw []byte) (ServiceScan, error) {
	out := ServiceScan{Services: []Service{}}
	defs, err := dsmservices.ParseStrict(file, raw)
	if err != nil {
		return out, ErrDetection
	}
	seen := map[int]bool{}
	for _, def := range defs {
		for _, interval := range def.Destinations {
			out.UDPSkipped = out.UDPSkipped || interval.UDP
			if !interval.TCP {
				continue
			}
			for port := interval.First; port <= interval.Last; port++ {
				if seen[port] {
					continue
				}
				seen[port] = true
				if len(out.Services) == MaxFollowed+1 {
					out.Overflow = true
					break
				}
				name := strings.TrimSuffix(file, ".sc") + " / " + def.ID
				if def.Title != "" {
					name = strings.TrimSuffix(file, ".sc") + " / " + def.Title
				}
				sum := sha256.Sum256([]byte(fmt.Sprintf("%s/%s/%d", file, def.ID, port)))
				out.Services = append(out.Services, Service{fmt.Sprintf("detected-%x", sum[:12]), name, port})
			}
		}
	}
	out.Overflow = out.Overflow || len(out.Services) > MaxFollowed
	return out, nil
}
