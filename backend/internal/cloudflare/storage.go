package cloudflare

import (
	"errors"
	"fmt"
	"os"

	"dsmapp/internal/connector"
)

var errStoragePermission = connector.Coded("cloudflare_storage_permission", "the package account cannot write Cloudflare connection data; an administrator must repair the data directory permissions")

// Keep the failing operation/path for the service log, but expose a stable,
// translated reason to the UI. Never try to gain privileges to repair ownership.
func storageError(cause error) error {
	code := errSave
	if errors.Is(cause, os.ErrPermission) {
		code = errStoragePermission
	}
	return fmt.Errorf("%w: %w", code, cause)
}

func privateDir(dir string) error {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return storageError(err)
	}
	if err := os.Chmod(dir, 0700); err != nil {
		return storageError(err)
	}
	return nil
}
