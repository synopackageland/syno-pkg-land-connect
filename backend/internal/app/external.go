package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"dsmapp/internal/dsmexternal"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

const dsmOrigin = "http://127.0.0.1:5000"

// readExternal and writeExternal act with the request's own, freshly verified
// administrator session; DSM itself authorizes the change. Connect never
// stores the session, runs as root or adds a helper for this.
func (cfg Config) readExternal(ctx context.Context, request *http.Request) (dsmexternal.External, error) {
	cookie, token := request.Header.Get("Cookie"), request.Header.Get("X-SYNO-TOKEN")
	if cfg.ExternalGet != nil {
		return cfg.ExternalGet(ctx, cookie, token)
	}
	return dsmexternal.ReadExternal(ctx, dsmOrigin, cookie, token)
}

func (cfg Config) writeExternal(ctx context.Context, request *http.Request, u dsmexternal.Update) error {
	cookie, token := request.Header.Get("Cookie"), request.Header.Get("X-SYNO-TOKEN")
	if cfg.ExternalSet != nil {
		return cfg.ExternalSet(ctx, cookie, token, u)
	}
	return dsmexternal.WriteExternal(ctx, dsmOrigin, cookie, token, u)
}

// externalAdvice compares DSM's external access address with the stable
// address of a running method; a failed read is state unknown.
func (cfg Config) externalAdvice(ctx context.Context, caller api.Caller, request *http.Request) dsmexternal.Advice {
	var current *dsmexternal.External
	if e, err := cfg.readExternal(ctx, request); err == nil {
		current = &e
	}
	return dsmexternal.Advise(current, cfg.liveConnections(ctx, caller))
}

func (cfg Config) externalStatus(ctx context.Context, caller api.Caller, request *http.Request, params json.RawMessage) (any, error) {
	if err := decodeParams(params, &struct{}{}); err != nil {
		return nil, err
	}
	return cfg.externalAdvice(ctx, caller, request), nil
}

type externalSetResult struct {
	Changed  bool                 `json:"changed"`
	Previous dsmexternal.External `json:"previous"`
	Advice   dsmexternal.Advice   `json:"advice"`
}

// externalSet changes DSM's advertised external address (Control Panel >
// External Access > Advanced) to exactly the given values, never DSM's
// listening ports. The same values again change nothing.
func (cfg Config) externalSet(ctx context.Context, caller api.Caller, request *http.Request, params json.RawMessage) (any, error) {
	var in struct {
		Hostname  *string `json:"hostname"`
		HTTPPort  *int    `json:"httpPort"`
		HTTPSPort *int    `json:"httpsPort"`
	}
	if err := decodeParams(params, &in); err != nil {
		return nil, err
	}
	if in.Hostname == nil || !dsmexternal.ValidHostname(*in.Hostname) {
		return nil, api.BadRequest("hostname must be a host name or IP address, or empty to clear")
	}
	for _, p := range []*int{in.HTTPPort, in.HTTPSPort} {
		if p != nil && (*p < 1 || *p > 65535) {
			return nil, api.BadRequest("ports must be 1-65535")
		}
	}
	if in.HTTPPort != nil && in.HTTPSPort != nil && *in.HTTPPort == *in.HTTPSPort {
		return nil, api.BadRequest("HTTP and HTTPS ports must differ")
	}
	previous, err := cfg.readExternal(ctx, request)
	if err != nil {
		return nil, &api.Error{Code: "external_unavailable", Message: "DSM external settings could not be read"}
	}
	result := externalSetResult{Previous: previous}
	if !applied(previous, in.Hostname, in.HTTPPort, in.HTTPSPort) {
		var refused *dsmexternal.Refused
		switch err := cfg.writeExternal(ctx, request, dsmexternal.Update{Hostname: in.Hostname, HTTPPort: in.HTTPPort, HTTPSPort: in.HTTPSPort}); {
		case errors.As(err, &refused) && refused.Code == 3510:
			return nil, &api.Error{Code: "external_invalid", Message: "DSM refused the address or port"}
		case errors.As(err, &refused) && refused.Code == 3511:
			return nil, &api.Error{Code: "external_port_conflict", Message: "DSM's external HTTP and HTTPS ports would be equal"}
		case errors.As(err, &refused):
			return nil, &api.Error{Code: "external_denied", Message: "DSM refused the change for this session"}
		case err != nil:
			return nil, &api.Error{Code: "external_unavailable", Message: "DSM external settings could not be changed"}
		}
		result.Changed = true
	}
	result.Advice = cfg.externalAdvice(ctx, caller, request)
	if result.Advice.Current == nil || !applied(*result.Advice.Current, in.Hostname, in.HTTPPort, in.HTTPSPort) {
		return nil, &api.Error{Code: "external_unconfirmed", Message: "DSM did not read back the new external address"}
	}
	return result, nil
}

// applied reports whether DSM already holds every given value.
func applied(e dsmexternal.External, host *string, httpPort, httpsPort *int) bool {
	same := func(have, want *int) bool { return want == nil || (have != nil && *have == *want) }
	return dsmexternal.SameHost(e.Hostname, *host) && same(e.HTTPPort, httpPort) && same(e.HTTPSPort, httpsPort)
}
