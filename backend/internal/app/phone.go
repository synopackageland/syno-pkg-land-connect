package app

import (
	"context"
	"encoding/json"
	"os"
	"sync"

	"dsmapp/internal/apps"
	"dsmapp/internal/phonelink"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

// phoneAddNAS lists DSM's name and LAN origins for the "Connect your phone"
// QR code, with the leaf certificate fingerprint of an https origin a phone
// would not trust. It is read-only and returns no secret: the QR code is
// meant to be seen. The external ways come from the existing statuses.
func (cfg Config) phoneAddNAS(ctx context.Context, _ api.Caller, params json.RawMessage) (any, error) {
	var in phonelink.Current
	if err := decodeParams(params, &in); err != nil {
		return nil, err
	}
	if len(in.Host) > 253 || len(in.Protocol) > 8 || in.Port < 0 || in.Port > 65535 {
		return nil, api.BadRequest("invalid params: current address")
	}
	src := phonelink.Source{}
	if cfg.PhoneLink != nil {
		src = *cfg.PhoneLink
	}
	if src.Ports == nil {
		src.Ports = cfg.portDetector().Detect
	}
	if src.Hostname == nil {
		src.Hostname = os.Hostname
	}
	if src.Roots == nil {
		src.Roots = sync.OnceValue(apps.ProductRoots)
	}
	return src.Collect(ctx, in), nil
}
