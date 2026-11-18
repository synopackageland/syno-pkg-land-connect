package supervise

import (
	"errors"
	"testing"

	"dsmapp/internal/connector"
)

func TestHealthStepsNameTheStepAndAttemptAndKeepTheCode(t *testing.T) {
	coded := connector.Coded("apply_reauth_required", "sign in again")
	e := Step("login", 7, coded)
	var step *StepError
	if !errors.As(e, &step) || step.Step != "login" || step.Attempt != 7 || connector.ErrorCode(e) != "apply_reauth_required" {
		t.Fatal("a failed step lost its name, attempt or code", e)
	}
	if errors.Is(e, ErrWaiting) {
		t.Fatal("a failed step reads as waiting")
	}
	w := Wait("status", 7, errors.New("read timed out"))
	if !errors.Is(w, ErrWaiting) || !errors.As(w, &step) || step.Step != "status" {
		t.Fatal("an incomplete observation must read as waiting at its step", w)
	}
	if Step("x", 1, nil) != nil || Wait("x", 1, nil) != nil {
		t.Fatal("a passed step must be nil")
	}
}
