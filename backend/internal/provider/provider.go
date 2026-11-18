// Package provider is the replaceable connection boundary. It accepts only
// provider credentials, never shell commands, DSM credentials or arbitrary URLs.
package provider

import (
	"context"
	"errors"
)

var ErrInvalidToken = errors.New("invalid demo token")
var ErrUnavailable = errors.New("provider unavailable")

type Connection struct {
	Connected       bool   `json:"connected"`
	ShareURL        string `json:"shareUrl"`
	State           string `json:"state,omitempty"`
	ShareAcceptance string `json:"shareAcceptance,omitempty"`
}

// A real provider will own its daemon lifecycle and fresh health checks.
// Connection is public state only; credentials require a separate approved store.
type Provider interface {
	ID() string
	Connect(context.Context, string) (Connection, error)
	Status(context.Context, Connection) (Connection, error)
	Disconnect(context.Context, Connection) error
}

// Fake simulates a connection. It performs no DNS, network or process operations.
// Its .invalid URL cannot be mistaken for a functional public NAS link.
type Fake struct{}

func (Fake) ID() string { return "fake" }
func (Fake) Connect(ctx context.Context, token string) (Connection, error) {
	if err := ctx.Err(); err != nil {
		return Connection{}, err
	}
	switch token {
	case "demo-connect":
		return Connection{Connected: true, ShareURL: "https://nas-demo.invalid/share/example"}, nil
	case "demo-fail":
		return Connection{}, ErrUnavailable
	default:
		return Connection{}, ErrInvalidToken
	}
}
func (Fake) Status(ctx context.Context, c Connection) (Connection, error) {
	if err := ctx.Err(); err != nil {
		return Connection{}, err
	}
	if c.Connected {
		return Connection{Connected: true, ShareURL: "https://nas-demo.invalid/share/example"}, nil
	}
	return Connection{}, nil
}
func (Fake) Disconnect(ctx context.Context, _ Connection) error { return ctx.Err() }
