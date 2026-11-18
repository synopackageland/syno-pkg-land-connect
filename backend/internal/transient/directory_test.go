package transient

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeDirectoryRefusesPrecreatedLinksAndBroadPermissions(t *testing.T) {
	sum := sha256.Sum256([]byte(t.TempDir()))
	key := fmt.Sprintf("test-%x", sum[:8])
	path := filepath.Join("/tmp", fmt.Sprintf("SynoPkgConnect-%d-%s", os.Geteuid(), key))
	target := t.TempDir()
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(path) })
	if _, err := Directory(key, true); err == nil {
		t.Fatal("followed precreated symlink")
	}
	os.Remove(path)
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := Directory(key, true); err == nil {
		t.Fatal("accepted broad permissions")
	}
	if err := os.Chmod(path, 0700); err != nil {
		t.Fatal(err)
	}
	if got, err := Directory(key, true); err != nil || got != path {
		t.Fatal(got, err)
	}
	if _, err := Directory("../escape", true); err == nil {
		t.Fatal("accepted path traversal")
	}
}

func TestDataDirectoryAliasesShareTheSameRuntimeKey(t *testing.T) {
	dir := t.TempDir()
	alias := filepath.Join(t.TempDir(), "var")
	if err := os.Symlink(dir, alias); err != nil {
		t.Fatal(err)
	}
	first, err := For(dir, "alias", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(first) })
	second, err := For(alias, "alias", true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(second) })
	if first != second {
		t.Fatalf("daemon and CGI aliases disagree: %s != %s", first, second)
	}
}
