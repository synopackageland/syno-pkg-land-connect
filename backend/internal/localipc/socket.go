// Package localipc names Connect's private package-account sockets.
package localipc

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

const RunDir = "/var/packages/SynoPkgConnect/var/run"

// Prepare removes stale sockets only while the caller holds the daemon lock.
// It refuses symlinks, foreign owners and non-sockets before SDK admission.
func Prepare(path string) error {
	if filepath.Clean(path) != path || filepath.Dir(path) != RunDir {
		return errors.New("invalid Syno Connect Center socket path")
	}
	if err := os.Mkdir(RunDir, 0700); err != nil && !os.IsExist(err) {
		return err
	}
	dir, err := os.Lstat(RunDir)
	if err != nil {
		return err
	}
	owner, ok := dir.Sys().(*syscall.Stat_t)
	if !ok || !dir.IsDir() || dir.Mode().Perm() != 0700 || owner.Uid != uint32(os.Geteuid()) {
		return errors.New("unsafe Syno Connect Center socket directory")
	}
	st, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	owner, ok = st.Sys().(*syscall.Stat_t)
	if !ok || st.Mode()&os.ModeSocket == 0 || st.Mode().Perm() != 0600 || owner.Uid != uint32(os.Geteuid()) {
		return errors.New("unsafe stale Syno Connect Center socket")
	}
	return os.Remove(path)
}
