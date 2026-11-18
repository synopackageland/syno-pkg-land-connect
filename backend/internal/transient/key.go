package transient

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
)

// For separates runtime files for independent persistent stores.
func For(dir, kind string, create bool) (string, error) {
	canonical, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(canonical))
	return Directory(fmt.Sprintf("%s-%x", kind, sum[:8]), create)
}

// Clear removes only a verified package-owned runtime directory. Callers must
// join all of its writers first; retained identities live outside this tree.
func Clear(dir, kind string) error {
	path, err := For(dir, kind, false)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return os.RemoveAll(path)
}
