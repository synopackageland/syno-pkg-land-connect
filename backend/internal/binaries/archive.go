package binaries

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

func extract(r io.Reader, dir string, limit int64, wanted ...string) error {
	if len(wanted) == 0 {
		wanted = []string{"tailscale", "tailscaled"}
	}
	allowed := map[string]bool{}
	for _, name := range wanted {
		allowed[name] = true
	}
	z, e := gzip.NewReader(r)
	if e != nil {
		return e
	}
	defer z.Close()
	tr := tar.NewReader(z)
	seen := map[string]bool{}
	var total int64
	for count := 0; ; count++ {
		h, e := tr.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
		if count > 64 {
			return errors.New("too many archive entries")
		}
		name := strings.TrimSuffix(h.Name, "/")
		if name == "" || path.IsAbs(name) || path.Clean(name) != name || name == ".." || strings.HasPrefix(name, "../") || strings.Contains(name, "\\") {
			return errors.New("unsafe archive path")
		}
		if h.Typeflag == tar.TypeDir {
			continue
		}
		if h.Typeflag != tar.TypeReg {
			return errors.New("archive links and special files rejected")
		}
		if h.Size < 0 || h.Size > limit-total {
			return errors.New("expanded archive too large")
		}
		total += h.Size
		base := path.Base(name)
		if !allowed[base] {
			continue
		}
		if seen[base] || h.Size == 0 {
			return errors.New("duplicate or empty binary")
		}
		seen[base] = true
		f, e := os.OpenFile(filepath.Join(dir, base), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
		if e != nil {
			return e
		}
		_, e = io.Copy(f, tr)
		if e == nil {
			e = f.Sync()
		}
		ce := f.Close()
		if e != nil {
			return e
		}
		if ce != nil {
			return ce
		}
	}
	// Consume gzip footer to catch a truncated/corrupt compressed stream.
	n, e := io.Copy(io.Discard, io.LimitReader(z, limit+1))
	if e != nil {
		return e
	}
	if n > limit {
		return errors.New("trailing archive data too large")
	}
	for _, name := range wanted {
		if !seen[name] {
			return errors.New("archive missing binaries")
		}
	}
	return nil
}
