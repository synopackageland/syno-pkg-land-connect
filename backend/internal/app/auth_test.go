package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

type flakyAuth struct {
	errs  []error
	calls int
}

func (a *flakyAuth) Authenticate(*http.Request) (api.Identity, error) {
	a.calls++
	if a.calls <= len(a.errs) {
		return api.Identity{}, a.errs[a.calls-1]
	}
	return api.Identity{User: "admin", Admin: true}, nil
}

var errTransient = errors.New("run authenticate.cgi: resource temporarily unavailable")

func TestAuthenticationRetriesATransientFailure(t *testing.T) {
	next := &flakyAuth{errs: []error{errTransient}}
	id, e := retryAuthentication(next, time.Millisecond).Authenticate(httptest.NewRequest("POST", "/", nil))
	if e != nil || id.User != "admin" || next.calls != 2 {
		t.Fatal("a transient DSM authentication failure must be retried", id, e, next.calls)
	}
}
func TestAuthenticationRetryIsBounded(t *testing.T) {
	next := &flakyAuth{errs: []error{errTransient, errTransient, errTransient, errTransient}}
	if _, e := retryAuthentication(next, time.Millisecond).Authenticate(httptest.NewRequest("POST", "/", nil)); !errors.Is(e, errTransient) || next.calls != 3 {
		t.Fatal("authentication must give up after three attempts", e, next.calls)
	}
}
func TestAuthenticationNeverRetriesADecision(t *testing.T) {
	for _, refusal := range []error{api.ErrNoSession, api.ErrAdminRequired, api.ErrAppPrivilegeDenied} {
		next := &flakyAuth{errs: []error{refusal}}
		if _, e := retryAuthentication(next, time.Millisecond).Authenticate(httptest.NewRequest("POST", "/", nil)); !errors.Is(e, refusal) || next.calls != 1 {
			t.Fatal("a DSM decision is final", refusal, e, next.calls)
		}
	}
}
func TestAuthenticationRetryStopsWithTheRequest(t *testing.T) {
	next := &flakyAuth{errs: []error{errTransient, errTransient}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("POST", "/", nil).WithContext(ctx)
	if _, e := retryAuthentication(next, time.Millisecond).Authenticate(r); e == nil || next.calls != 1 {
		t.Fatal("a canceled request must not be retried", e, next.calls)
	}
}
