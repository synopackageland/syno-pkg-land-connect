//go:build !linux

package transient

// macOS development fixtures only; shipped binaries are Linux-only.
func checkTmpfs(string) error { return nil }
