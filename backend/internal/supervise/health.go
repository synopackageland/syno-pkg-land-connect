package supervise

import "errors"

// ErrWaiting marks a health observation that could not finish, such as a
// status read or a DSM check that timed out while the NAS was busy. It is
// neither healthy nor failed: it never counts toward a repair and never
// resets the repair cap.
var ErrWaiting = errors.New("health observation incomplete")

// StepError names the health step that failed and the provider attempt it
// observed, so every provider's log says which check failed. Its code is the
// wrapped one.
type StepError struct {
	Step    string
	Attempt uint64
	Err     error
}

func (e *StepError) Error() string { return e.Err.Error() }
func (e *StepError) Unwrap() error { return e.Err }

// Step reports a failed health step; nil stays nil.
func Step(step string, attempt uint64, err error) error {
	if err == nil {
		return nil
	}
	return &StepError{step, attempt, err}
}

// Wait reports an incomplete health step; errors.Is finds ErrWaiting and err.
func Wait(step string, attempt uint64, err error) error {
	if err == nil {
		return nil
	}
	return Step(step, attempt, errors.Join(ErrWaiting, err))
}
