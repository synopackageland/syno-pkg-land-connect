// Package transient owns package-private files that may be lost at reboot.
package transient

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
)

var validKey = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

// Directory never follows a precreated link or repairs someone else's directory.
// Production Linux must have tmpfs at /tmp; no persistent fallback is allowed.
func Directory(key string, create bool) (string, error) {
	if !validKey.MatchString(key) {
		return "", errors.New("invalid runtime key")
	}
	if err := checkTmpfs("/tmp"); err != nil {
		return "", err
	}
	path := filepath.Join("/tmp", fmt.Sprintf("SynoPkgConnect-%d-%s", os.Geteuid(), key))
	if create {
		if err := os.Mkdir(path, 0700); err != nil && !os.IsExist(err) {
			return "", err
		}
	}
	st, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	owner, ok := st.Sys().(*syscall.Stat_t)
	if !ok || !st.IsDir() || st.Mode().Perm() != 0700 || owner.Uid != uint32(os.Geteuid()) {
		return "", errors.New("unsafe runtime directory")
	}
	return path, nil
}
