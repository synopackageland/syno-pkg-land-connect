package daemon

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

func ReadPrivate(path string) ([]byte, error) {
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 8192 {
		return nil, fmt.Errorf("invalid private file")
	}
	return io.ReadAll(io.LimitReader(f, 8193))
}
func WritePrivate(path string, raw []byte) error {
	// Every private file is synced before it is published, so identical content
	// is already durable. Skipping it saves fsyncs that a loaded NAS makes slow.
	if old, e := ReadPrivate(path); e == nil && bytes.Equal(old, raw) {
		return nil
	}
	f, e := os.CreateTemp(filepath.Dir(path), ".private-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	if _, e = f.Write(raw); e != nil {
		f.Close()
		return e
	}
	if e = f.Sync(); e != nil {
		f.Close()
		return e
	}
	if e = f.Close(); e != nil {
		return e
	}
	if e = os.Rename(f.Name(), path); e != nil {
		return e
	}
	d, e := os.Open(filepath.Dir(path))
	if e != nil {
		return e
	}
	defer d.Close()
	return d.Sync()
}
