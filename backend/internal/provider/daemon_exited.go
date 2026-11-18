package provider

import "errors"

// ErrDaemonExited reports that an owned daemon is gone. It is the only read
// failure that fails a connection: a read that did not answer, as on a
// swapping NAS, means waiting.
var ErrDaemonExited = errors.Join(ErrUnavailable, errors.New("daemon exited"))
