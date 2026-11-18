package connector

import "errors"

// Preserved reports a rejected/rolled-back change whose existing connection is
// still owned. Callers must not turn this refusal into a disconnect or revoke.
type PreservedError struct{ Cause error }

func (e *PreservedError) Error() string { return e.Cause.Error() }
func (e *PreservedError) Unwrap() error { return e.Cause }
func Preserved(e error) error {
	if e == nil {
		return nil
	}
	return &PreservedError{Cause: e}
}
func IsPreserved(e error) bool { var p *PreservedError; return errors.As(e, &p) }
