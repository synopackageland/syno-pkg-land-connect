//go:build !linux

package apps

import (
	"context"
	"errors"
	"testing"
)

func TestUnsupportedIngressOwnershipFailsClosed(t *testing.T) {
	if err := CheckIngressListener(1001, 18080); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if _, err := DialIngress(context.Background(), 1001, "127.0.0.1:18080"); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}
