package appaccess

import (
	"context"
	"dsmapp/internal/provider"
	"testing"
)

// lockedPrivate is a provider whose restore still holds its lock.
type lockedPrivate struct{}

func (lockedPrivate) AppAccessNetwork(context.Context) (provider.AppNetwork, error) {
	panic("availability must not wait on a restoring provider")
}
func (lockedPrivate) RefreshApps(context.Context) error { return nil }

func TestAvailabilityAnswersWhileProvidersRestore(t *testing.T) {
	e := New(context.Background(), Config{Private: lockedPrivate{}})
	defer e.Close()
	e.SetRestoring(true)
	out, err := e.Availability(context.Background())
	if err != nil || !out.Starting || out.Private.Available {
		t.Fatal("a restoring NAS must read as starting", out, err)
	}
}
