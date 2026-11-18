package app

import (
	"context"
	"dsmapp/internal/ipv6"
	"dsmapp/internal/ports"
	"encoding/json"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

func (cfg Config) ipv6Status(ctx context.Context, _ api.Caller, params json.RawMessage) (any, error) {
	if err := decodeParams(params, &struct{}{}); err != nil {
		return nil, err
	}
	p := cfg.IPv6
	if p == nil {
		p = &ipv6.Provider{Ports: ports.ResolvedSource{Store: ports.Store{Dir: cfg.DataDir}, Detector: cfg.portDetector()}}
	}
	return p.Inspect(ctx), nil
}
