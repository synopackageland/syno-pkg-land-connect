package connector

import "errors"

// DSMPortChanged retains the detected ports through route withdrawal and restore.
// The refusal still requires the existing administrator review and trust flow.
func DSMPortChanged(from, to int) error {
	return &dsmPortChangeError{
		cause:  Coded("pin_proxy_required", "DSM HTTPS port changed"),
		change: DSMPortChange{From: from, To: to},
	}
}

type dsmPortChangeError struct {
	cause  error
	change DSMPortChange
}

func (e *dsmPortChangeError) Error() string { return e.cause.Error() }
func (e *dsmPortChangeError) Unwrap() error { return e.cause }

func PortChange(err error) *DSMPortChange {
	var e *dsmPortChangeError
	if errors.As(err, &e) {
		change := e.change
		return &change
	}
	return nil
}
