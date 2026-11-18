package ports

import (
	"context"
	"errors"
	"os"
	"syscall"
)

var (
	ErrDetection    = errors.New("DSM ports could not be detected")
	ErrValidation   = errors.New("invalid port settings")
	ErrConflict     = errors.New("port conflicts with another TCP service")
	ErrConfirmation = errors.New("explicit administrator exposure confirmation required")
	ErrSettings     = errors.New("port settings unavailable")
)

// openPortConfig rejects links, nonregular files and oversized input before reading.
func openPortConfig(path string, limit int64) (*os.File, error) {
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, ErrDetection
	}
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Size() > limit {
		f.Close()
		return nil, ErrDetection
	}
	return f, nil
}

func validDSM(p DSMPorts) bool {
	return p.HTTP >= 1 && p.HTTP <= 65535 && p.HTTPS >= 1 && p.HTTPS <= 65535 && p.HTTP != p.HTTPS
}
func detect(ctx context.Context, d Detector) (DSMPorts, bool) {
	if d == nil {
		return DSMPorts{}, false
	}
	p, e := d.Detect(ctx)
	return p, e == nil && validDSM(p)
}
