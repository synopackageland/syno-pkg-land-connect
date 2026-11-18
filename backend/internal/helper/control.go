package helper

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Process identity rejects reboot/PID reuse and executable replacement.
// Linux Stop authenticates the owning supervisor socket; only that parent signals its child.
type Process struct {
	Inventory Inventory `json:"inventory"`
	Mode      string    `json:"mode,omitempty"` // must be supervised; old unsupervised records fail closed
	PID       int       `json:"pid"`
	Boot      string    `json:"boot"`
	Start     string    `json:"start"`
	Device    uint64    `json:"device"`
	Inode     uint64    `json:"inode"`
}

type Processes interface {
	Stop(Process) error
	Start(Inventory) error
}
type Controller struct {
	Client
	Processes Processes
}

func waitHealthy(ctx context.Context, probe func(context.Context) bool) error {
	for ctx.Err() == nil {
		if probe(ctx) {
			return nil
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
	return ctx.Err()
}

func writeProtected(dir, name string, value any) error {
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".transition-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	mode := fs.FileMode(0644)
	if name == "process.json" {
		mode = 0600
	}
	if err := f.Chmod(mode); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(f.Name(), filepath.Join(dir, name)); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

func writeProcess(dir string, p Process) error { return writeProtected(dir, "process.json", p) }

func readProcess(dir string, owner uint32) (Process, error) {
	var p Process
	if err := protected(dir, owner, true); err != nil {
		return p, err
	}
	path := filepath.Join(dir, "process.json")
	if err := protected(path, owner, false); err != nil {
		return p, err
	}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return p, err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 4097))
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil || d.Decode(new(any)) != io.EOF || !p.Inventory.Valid() || p.Mode != "supervised" || p.PID <= 1 || p.Boot == "" || p.Start == "" || p.Inode == 0 {
		return p, errTrust
	}
	return p, nil
}

// Control is called only by the verified root executable from a fixed DSM
// task. Package hooks and CGI handlers never call this function.
func (c Controller) Control(ctx context.Context, action string) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if action != "enable" && action != "recover" && action != "teardown" && action != "disable" {
		return errors.New("unsupported lifecycle action")
	}
	if !c.Expected.Valid() || c.Processes == nil {
		return errTrust
	}
	if c.legacyRuntime() {
		return errors.New("approved legacy helper teardown required")
	}
	if err := protected(c.StateDir, c.Owner, true); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(c.StateDir, "control.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := protected(f.Name(), c.Owner, false); err != nil {
		return err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("lifecycle busy")
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	r, err := readRecord(c.StateDir, c.Owner)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if action == "recover" && (err != nil || !r.Enabled) {
		return errors.New("explicit enable approval required")
	}
	if err != nil {
		r = Record{Inventory: c.Expected}
	}
	if action == "disable" {
		r.Enabled = false
	}
	// Publish the durable gate first. A crashed/failed disable still refuses
	// requests and exposes required cleanup; never report cleanup as successful.
	r.Residual = true
	if err := writeProtected(c.StateDir, "approval.json", r); err != nil {
		return err
	}
	if err := c.teardown(); err != nil {
		return err
	}
	if action == "enable" || action == "recover" {
		r = Record{Inventory: c.Expected, Enabled: true, Residual: true}
		if err := writeProtected(c.StateDir, "approval.json", r); err != nil {
			return err
		}
		if err := c.Processes.Start(c.Expected); err != nil {
			return err
		}
	}
	r.Residual = false
	return writeProtected(c.StateDir, "approval.json", r)
}

func (c Controller) teardown() error {
	p, err := readProcess(c.RunDir, c.Owner)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err == nil {
		if p.Inventory.Package != c.Expected.Package {
			return errTrust
		}
		if err := c.Processes.Stop(p); err != nil {
			return err
		}
	}
	for _, name := range []string{"helper.sock", "helper-root.sock", "supervisor.sock"} {
		path := filepath.Join(c.RunDir, name)
		st, e := os.Lstat(path)
		if e == nil {
			s, ok := st.Sys().(*syscall.Stat_t)
			if !ok || s.Uid != c.Owner || st.Mode()&os.ModeSocket == 0 || st.Mode().Perm()&0002 != 0 {
				return errTrust
			}
			conn, err := net.DialTimeout("unix", path, 100*time.Millisecond)
			if err == nil {
				conn.Close()
				return errors.New("unidentified live socket")
			}
			if !errors.Is(err, syscall.ECONNREFUSED) && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
		} else if !errors.Is(e, fs.ErrNotExist) {
			return e
		}
	}
	if err == nil {
		return os.Remove(filepath.Join(c.RunDir, "process.json"))
	}
	return nil
}
