package cloudflare

import (
	"errors"
	"os"
)

// Retain runs after the package supervisor has stopped. Retaining App Data
// retains own-domain identity, authorization and intent, including an explicit
// disabled intent. The SDK alone deletes App Data when the user requests it.
// Temporary and legacy pasted-token connections keep their uninstall behavior.
func Retain(dir string) (bool, error) {
	t := NewTunnel(Config{Dir: dir})
	if info, err := os.Lstat(t.domainDir()); errors.Is(err, os.ErrNotExist) {
		return false, Revoke(dir)
	} else if err != nil {
		return false, storageError(err)
	} else if !info.IsDir() {
		return false, errSave
	}
	// Incomplete recovery state belongs to retained App Data too.
	if err := os.Remove(t.tokenFile()); err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, storageError(err)
	}
	s := t.loadSettings()
	if s.Mode == "domain" {
		return true, nil
	}
	s.Enabled = false
	return false, t.saveSettings(s)
}

// RetainIntent lets the selection owner persist every disabled gate before
// any credential cleanup can fail. It never writes or starts a provider.
func RetainIntent(dir string) (bool, error) {
	t := NewTunnel(Config{Dir: dir})
	info, err := os.Lstat(t.domainDir())
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, storageError(err)
	}
	if !info.IsDir() {
		return false, errSave
	}
	return t.loadSettings().Mode == "domain", nil
}
