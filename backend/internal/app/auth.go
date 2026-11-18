package app

import (
	"errors"
	"net/http"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

// RetryAuthentication retries a transient DSM authentication failure, such as
// authenticate.cgi failing to start on a loaded NAS, which would otherwise
// reach the UI as auth_unavailable. A DSM decision is never retried.
func RetryAuthentication(next api.Authenticator) api.Authenticator {
	return retryAuthentication(next, 250*time.Millisecond)
}
func retryAuthentication(next api.Authenticator, pause time.Duration) api.Authenticator {
	return retryingAuth{next, pause}
}

type retryingAuth struct {
	next  api.Authenticator
	pause time.Duration
}

func (a retryingAuth) Authenticate(r *http.Request) (api.Identity, error) {
	for attempt := 1; ; attempt++ {
		id, e := a.next.Authenticate(r)
		if e == nil || attempt == 3 || errors.Is(e, api.ErrNoSession) || errors.Is(e, api.ErrAdminRequired) || errors.Is(e, api.ErrAppPrivilegeDenied) {
			return id, e
		}
		select {
		case <-r.Context().Done():
			return id, e
		case <-time.After(a.pause):
		}
	}
}
