package app

import (
	"context"
	"encoding/json"
	"errors"

	"dsmapp/internal/ports"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

func (cfg Config) portDetector() ports.Detector {
	if cfg.PortDetector != nil {
		return cfg.PortDetector
	}
	return ports.DatastoreDetector{}
}
func portError(e error) error {
	switch {
	case e == nil:
		return nil
	case errors.Is(e, ports.ErrValidation):
		return api.BadRequest("invalid port settings")
	case errors.Is(e, ports.ErrConflict):
		return &api.Error{Code: "port_conflict", Message: "port conflicts with another TCP service"}
	case errors.Is(e, ports.ErrConfirmation):
		return &api.Error{Code: "port_confirmation_required", Message: "administrator must confirm service exposure"}
	case errors.Is(e, ports.ErrDetection):
		return &api.Error{Code: "port_detection_unavailable", Message: "DSM ports could not be detected; mappings are suspended"}
	default:
		return &api.Error{Code: "port_settings_unavailable", Message: "port settings are unavailable"}
	}
}
func (cfg Config) listPorts(ctx context.Context, _ api.Caller, p json.RawMessage) (any, error) {
	if e := decodeParams(p, &struct{}{}); e != nil {
		return nil, e
	}
	v, e := (ports.Store{Dir: cfg.DataDir}).List(ctx, cfg.portDetector())
	return v, portError(e)
}
func (cfg Config) upsertPort(ctx context.Context, _ api.Caller, p json.RawMessage) (any, error) {
	var in ports.Input
	if e := decodeParams(p, &in); e != nil {
		return nil, e
	}
	v, e := (ports.Store{Dir: cfg.DataDir}).Upsert(ctx, cfg.portDetector(), in)
	return v, portError(e)
}
func (cfg Config) deletePort(ctx context.Context, _ api.Caller, p json.RawMessage) (any, error) {
	var in struct {
		ID string `json:"id"`
	}
	if e := decodeParams(p, &in); e != nil {
		return nil, e
	}
	s := ports.Store{Dir: cfg.DataDir}
	if _, e := s.Delete(ctx, in.ID); e != nil {
		return nil, portError(e)
	}
	v, e := s.List(ctx, cfg.portDetector())
	return v, portError(e)
}
func (cfg Config) setDSMPort(ctx context.Context, _ api.Caller, p json.RawMessage) (any, error) {
	var in struct {
		ID              string `json:"id"`
		Enabled         bool   `json:"enabled"`
		ConfirmExposure bool   `json:"confirmExposure"`
	}
	if e := decodeParams(p, &in); e != nil {
		return nil, e
	}
	v, e := (ports.Store{Dir: cfg.DataDir}).SetDSM(ctx, cfg.portDetector(), in.ID, in.Enabled, in.ConfirmExposure)
	return v, portError(e)
}

func (cfg Config) setFollowPorts(ctx context.Context, _ api.Caller, p json.RawMessage) (any, error) {
	var in struct {
		Enabled         bool `json:"enabled"`
		ConfirmExposure bool `json:"confirmExposure"`
	}
	if e := decodeParams(p, &in); e != nil {
		return nil, e
	}
	v, e := (ports.Store{Dir: cfg.DataDir}).SetFollow(ctx, cfg.portDetector(), in.Enabled, in.ConfirmExposure)
	return v, portError(e)
}
