package ports

import (
	"path/filepath"

	"dsmapp/internal/transient"
)

// A separate key per data directory keeps isolated fixtures independent. No
// observation is ever read from the legacy, persistent data-directory path.
func (s Store) observationPath(create bool) (string, error) {
	dir, err := transient.For(s.Dir, "ports", create)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "ports-observation.json"), nil
}
