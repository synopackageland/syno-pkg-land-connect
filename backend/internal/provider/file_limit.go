package provider

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// ProviderFileLimit is a kernel-enforced per-file ceiling, including temporary
// files and the upstream identity stores. It is not a directory quota.
const ProviderFileLimit = 1 << 20

// ExecLimited replaces the trampoline with the official, unmodified binary.
// Limits are set in a separate process so downloading binaries in the worker
// remains possible. The hard limit cannot be raised by its unprivileged child.
func ExecLimited(args []string) error {
	if len(args) == 0 || !filepath.IsAbs(args[0]) {
		return errors.New("invalid provider executable")
	}
	if err := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: ProviderFileLimit, Max: ProviderFileLimit}); err != nil {
		return err
	}
	if err := syscall.Setrlimit(syscall.RLIMIT_CORE, &syscall.Rlimit{}); err != nil {
		return err
	}
	return syscall.Exec(args[0], args, os.Environ())
}
