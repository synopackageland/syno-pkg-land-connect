package provider

import (
	"bytes"
	"context"
	"dsmapp/internal/connector"
	"dsmapp/internal/ipv6"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// IPv6Runtime implements the selecting manager's contract without changing
// listeners, firewall or DNS. Connected means local candidate prerequisites,
// never public TCP/HTTPS proof. Stop/Disable cannot revoke manually opened rules.
type IPv6Runtime struct {
	CheckLocal  func(context.Context, []PortMapping) error
	mu          sync.Mutex
	dir         string
	source      ipv6.Source
	config      ipv6Config
	running     bool
	storeFailed bool
	loaded      bool
}
type ipv6Config struct {
	Schema          int           `json:"schema"`
	Enabled         bool          `json:"enabled"`
	ConfirmExposure bool          `json:"confirmExposure"`
	Mappings        []PortMapping `json:"mappings"`
}

var _ Runtime = (*IPv6Runtime)(nil)

// dir must be the trusted package-owned private state directory. Registration
// and administrator policy belong to C-PROV; callers never supply dir/source.
func NewIPv6Runtime(dir string, source ipv6.Source) *IPv6Runtime {
	return &IPv6Runtime{dir: dir, source: source, config: ipv6Config{Schema: 1}}
}
func (*IPv6Runtime) ID() string { return "ipv6" }
func (*IPv6Runtime) Schema() []Field {
	return []Field{{Name: "confirmExposure", Kind: "boolean", Required: true}, {Name: "mappings", Kind: "ports", Required: true}}
}
func (*IPv6Runtime) Binaries() []BinaryRequirement { return nil }

func validateIPv6Mappings(m []PortMapping) error {
	for _, port := range m {
		if port.LocalPort < 1 || port.LocalPort > 65535 || port.PublicPort != port.LocalPort || (port.Protocol != "http" && port.Protocol != "https" && port.Protocol != "tcp") {
			return errors.New("IPv6 requires supported direct ports without translation")
		}
	}
	return nil
}
func (r *IPv6Runtime) save(c ipv6Config) error {
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err = connector.WritePrivate(filepath.Join(r.dir, "ipv6.json"), raw); err != nil {
		return errors.New("IPv6 private settings unavailable")
	}
	r.loaded = true
	return nil
}

type ipv6Ports []PortMapping

func (p ipv6Ports) Ports(ctx context.Context) ([]ipv6.Port, error) {
	ports := make([]ipv6.Port, 0, len(p))
	for _, m := range p {
		ports = append(ports, ipv6.Port{Number: m.LocalPort, Scheme: m.Protocol})
	}
	return ports, ctx.Err()
}
func (r *IPv6Runtime) inspect(ctx context.Context, c ipv6Config) ipv6.Status {
	return (ipv6.Provider{Source: r.source, Ports: ipv6Ports(c.Mappings)}).Inspect(ctx)
}

func (r *IPv6Runtime) Start(ctx context.Context, s Settings) (connector.Status, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return connector.Status{}, err
	}
	if !s.ConfirmExposure || len(s.Mappings) == 0 || s.Token != "" || s.Hostname != "" {
		return connector.Status{}, errors.New("IPv6 requires explicit administrator exposure confirmation and ports only")
	}
	if err := validateIPv6Mappings(s.Mappings); err != nil {
		return connector.Status{}, err
	}
	c := ipv6Config{Schema: 1, Enabled: true, ConfirmExposure: true, Mappings: append([]PortMapping{}, s.Mappings...)}
	if snapshot := r.inspect(ctx, c); snapshot.State != "available" {
		return connector.Status{}, errors.New("IPv6 local prerequisites unavailable")
	}
	if err := ctx.Err(); err != nil {
		return connector.Status{}, err
	}
	if err := r.save(c); err != nil {
		return connector.Status{}, err
	}
	r.config = c
	r.running = true
	r.storeFailed = false
	return r.status(), nil
}
func (r *IPv6Runtime) Restore(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	// Clear existing observations before validating the durable intent.
	r.running = false
	r.storeFailed = false
	r.config = ipv6Config{Schema: 1}
	c, err := r.read()
	if os.IsNotExist(err) {
		r.loaded = true
		return nil
	}
	if err != nil {
		r.storeFailed = true
		return err
	}
	r.config = c
	r.loaded = true
	r.running = c.Enabled
	return nil
}

// read decodes the saved intent strictly.
func (r *IPv6Runtime) read() (ipv6Config, error) {
	var c ipv6Config
	raw, err := connector.ReadPrivate(filepath.Join(r.dir, "ipv6.json"))
	if os.IsNotExist(err) {
		return c, err
	}
	if err != nil {
		return c, errors.New("IPv6 private settings unavailable")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var extra any
	if dec.Decode(&c) != nil || dec.Decode(&extra) != io.EOF || c.Schema != 1 || c.Enabled != c.ConfirmExposure || validateIPv6Mappings(c.Mappings) != nil {
		return ipv6Config{}, errors.New("invalid saved IPv6 settings")
	}
	return c, nil
}
func (r *IPv6Runtime) Stop() error { r.mu.Lock(); defer r.mu.Unlock(); r.running = false; return nil }
func (r *IPv6Runtime) Disable() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := r.config
	// A disabled connection is never restored; keep what an earlier process saved.
	if !r.loaded {
		if saved, err := r.read(); err == nil {
			c = saved
		}
	}
	c.Enabled = false
	c.ConfirmExposure = false
	if err := r.save(c); err != nil {
		return err
	}
	r.config = c
	r.running = false
	r.storeFailed = false
	return nil
}

// Forget clears the services a disabled connection kept for display and the
// next start. An enabled connection keeps them: disconnect first.
func (r *IPv6Runtime) Forget() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.config.Enabled {
		return errors.New("disconnect IPv6 before forgetting its services")
	}
	c := r.config
	c.Mappings = nil
	if err := r.save(c); err != nil {
		return err
	}
	r.config = c
	return nil
}
func (r *IPv6Runtime) AcceptMappings(m []PortMapping) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := validateIPv6Mappings(m); err != nil {
		return err
	}
	c := r.config
	c.Mappings = append([]PortMapping{}, m...)
	// Preserve restart intent when DSM changes its listen ports. Persist before
	// exposing the new snapshot; invalid or failed updates leave the old state.
	if c.Enabled {
		if err := r.save(c); err != nil {
			return err
		}
	}
	r.config = c
	return nil
}

// KeepMappings saves the services a disabled connection shows and restarts
// with. An enabled connection changes them through AcceptMappings.
func (r *IPv6Runtime) KeepMappings(m []PortMapping) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.config.Enabled {
		return errors.New("disconnect IPv6 before keeping its services")
	}
	if err := validateIPv6Mappings(m); err != nil {
		return err
	}
	c := r.config
	c.Mappings = append([]PortMapping{}, m...)
	if err := r.save(c); err != nil {
		return err
	}
	r.config = c
	return nil
}
func (r *IPv6Runtime) snapshot() ipv6.Status {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return r.inspect(ctx, r.config)
}
func (r *IPv6Runtime) status() connector.Status {
	s := connector.Status{Provider: "ipv6", State: "disconnected", ShareAcceptance: "not-applicable", Mappings: append([]PortMapping(nil), r.config.Mappings...)}
	if r.storeFailed {
		s.State = "failed"
		return s
	}
	if r.running && r.config.Enabled {
		s.State = "failed"
		snapshot := r.snapshot()
		if snapshot.State == "available" {
			s.State = "connected"
			for _, endpoint := range snapshot.Endpoints {
				s.Endpoints = append(s.Endpoints, Endpoint{URL: endpoint.URL, Public: true, LocalPort: endpoint.Port.Number})
			}
		}
	}
	return s
}
func (r *IPv6Runtime) Status() connector.Status { r.mu.Lock(); defer r.mu.Unlock(); return r.status() }

// Inspect preserves rich reasons without changing the common Runtime contract.
// app.Config.IPv6 accepts this read-only diagnostic extension.
func (r *IPv6Runtime) Inspect(ctx context.Context) ipv6.Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.inspect(ctx, r.config)
}

func (r *IPv6Runtime) Endpoints() []Endpoint {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.running || !r.config.Enabled || r.storeFailed {
		return nil
	}
	s := r.snapshot()
	if s.State != "available" {
		return nil
	}
	out := make([]Endpoint, 0, len(s.Endpoints))
	for _, e := range s.Endpoints {
		out = append(out, Endpoint{URL: e.URL, Public: true, LocalPort: e.Port.Number})
	}
	return out
}
