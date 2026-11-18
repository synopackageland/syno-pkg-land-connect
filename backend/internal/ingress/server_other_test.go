//go:build !linux

package ingress

import (
	"context"
	"dsmapp/internal/apps"
	"errors"
	"testing"
)

func TestUnsupportedIngressFailsClosed(t *testing.T) {
	if _, err := Start(context.Background(), Socket, &apps.Service{}, nil); !errors.Is(err, apps.ErrUnavailable) {
		t.Fatal(err)
	}
	if _, err := Call(context.Background(), Socket, Request{}); !errors.Is(err, apps.ErrUnavailable) {
		t.Fatal(err)
	}
	if _, err := ResolveSubject("", "ExampleApp", 1001); !errors.Is(err, apps.ErrUnavailable) {
		t.Fatal(err)
	}
}
