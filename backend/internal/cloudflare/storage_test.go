package cloudflare

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"dsmapp/internal/connector"
)

func TestSaveSettingsReportsPermissionFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires an unprivileged package account")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0700) })
	tunnel := NewTunnel(Config{Dir: dir})
	err := tunnel.saveSettings(settings{Enabled: true, Mode: "domain"})
	if connector.ErrorCode(err) != "cloudflare_storage_permission" || !errors.Is(err, os.ErrPermission) {
		t.Fatalf("expected permission diagnosis with original cause, got %v", err)
	}
	if !strings.Contains(err.Error(), dir) {
		t.Fatalf("missing failing file in diagnostic: %v", err)
	}
}

func TestPrepareRetainsStorageFailureCause(t *testing.T) {
	f := newFixture(t)
	if err := os.MkdirAll(f.dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.dir, "home"), []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	_, _, err := f.tunnel.prepare(context.Background())
	var pathErr *os.PathError
	if connector.ErrorCode(err) != "cloudflare_save_failed" || !errors.As(err, &pathErr) {
		t.Fatalf("expected original filesystem failure, got %v", err)
	}
}
