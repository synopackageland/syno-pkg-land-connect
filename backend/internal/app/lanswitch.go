package app

import (
	"context"
	"encoding/json"

	"dsmapp/internal/control"
	"dsmapp/internal/lanswitch"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

// LANSwitchControl reaches the LAN switch owner in connectd.
type LANSwitchControl interface {
	Status(context.Context) (lanswitch.Status, error)
	Set(context.Context, bool) (lanswitch.Status, error)
	Probe(context.Context) (lanswitch.Probe, error)
}

func (cfg Config) lanSwitch() LANSwitchControl {
	if cfg.LANSwitch != nil {
		return cfg.LANSwitch
	}
	return lanswitch.Client{Socket: control.Socket}
}

func lanSwitchUnavailable() error {
	return &api.Error{Code: "lan_switch_unavailable", Message: "the LAN switch setting could not be reached"}
}

// lanSwitchGet reports the "switch to the LAN address" option.
func (cfg Config) lanSwitchGet(ctx context.Context, _ api.Caller, params json.RawMessage) (any, error) {
	if err := decodeParams(params, &struct{}{}); err != nil {
		return nil, err
	}
	st, err := cfg.lanSwitch().Status(ctx)
	if err != nil {
		return nil, lanSwitchUnavailable()
	}
	return st, nil
}

// lanSwitchSet turns the option on or off.
func (cfg Config) lanSwitchSet(ctx context.Context, _ api.Caller, params json.RawMessage) (any, error) {
	var in struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeParams(params, &in); err != nil {
		return nil, err
	}
	if in.Enabled == nil {
		return nil, api.BadRequest("invalid params: enabled is required")
	}
	st, err := cfg.lanSwitch().Set(ctx, *in.Enabled)
	if err != nil {
		return nil, lanSwitchUnavailable()
	}
	return st, nil
}

// phoneLANProbe gives a signed-in page what it needs to check, from its own
// browser, whether it reaches DSM directly on the LAN: one-use ICE
// credentials and the LAN addresses with the DSM URL each leads to. With the
// option off it returns enabled false and nothing else.
func (cfg Config) phoneLANProbe(ctx context.Context, _ api.Caller, params json.RawMessage) (any, error) {
	if err := decodeParams(params, &struct{}{}); err != nil {
		return nil, err
	}
	p, err := cfg.lanSwitch().Probe(ctx)
	if err != nil {
		return lanswitch.Probe{Candidates: []lanswitch.Candidate{}}, nil
	}
	return p, nil
}
