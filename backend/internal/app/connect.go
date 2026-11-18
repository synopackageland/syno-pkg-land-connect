package app

import (
	"context"
	"dsmapp/internal/control"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"dsmapp/internal/connector"
	"dsmapp/internal/ports"
	"dsmapp/internal/provider"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

type connectionState struct {
	Schema     int                 `json:"schema"`
	Provider   string              `json:"provider"`
	Connection provider.Connection `json:"connection"`
}

type connectionView struct {
	Provider   string `json:"provider"`
	State      string `json:"state"`
	Simulation bool   `json:"simulation"`
	ShareURL   string `json:"shareUrl"`
}

// RevokeLegacySimulation clears retained demo intent during real uninstall.
// Stop and upgrade must preserve it. Reuse the CGI's locked, bounded state
// reader so malformed files and symlinks refuse cleanup rather than being followed.
func RevokeLegacySimulation(dir string) error {
	if _, err := os.Lstat(filepath.Join(dir, "connection.json")); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	_, err := (Config{DataDir: dir}).withConnection(context.Background(), func(s *connectionState) (bool, error) {
		changed := s.Connection != provider.Connection{}
		s.Connection = provider.Connection{}
		return changed, nil
	})
	return err
}

// Every CGI opens its own lock. No process-local variable represents saved state.
func (cfg Config) withConnection(ctx context.Context, op func(*connectionState) (bool, error)) (any, error) {
	lock, err := os.OpenFile(filepath.Join(cfg.DataDir, "connection.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, settingsError()
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		return nil, settingsError()
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	path := filepath.Join(cfg.DataDir, "connection.json")
	s := connectionState{Schema: 1, Provider: "fake"}
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err == nil {
		info, e := f.Stat()
		if e != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
			f.Close()
			return nil, settingsError()
		}
		dec := json.NewDecoder(io.LimitReader(f, 4097))
		dec.DisallowUnknownFields()
		e = dec.Decode(&s)
		var extra any
		end := dec.Decode(&extra)
		f.Close()
		if e != nil || end != io.EOF || s.Schema != 1 || s.Provider != "fake" || (s.Connection.Connected && s.Connection.ShareURL != "https://nas-demo.invalid/share/example") || (!s.Connection.Connected && s.Connection.ShareURL != "") {
			return nil, settingsError()
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, settingsError()
	}
	changed, err := op(&s)
	if err != nil {
		return nil, err
	}
	if changed {
		raw, e := json.Marshal(s)
		if e != nil {
			return nil, settingsError()
		}
		if e = saveConnection(path, raw); e != nil {
			return nil, settingsError()
		}
	}
	state := "disconnected"
	if s.Connection.Connected {
		state = "connected"
	}
	return connectionView{s.Provider, state, true, s.Connection.ShareURL}, nil
}

func settingsError() error {
	return &api.Error{Code: "settings_unavailable", Message: "package settings are unavailable"}
}

func saveConnection(path string, raw []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".connection-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func (cfg Config) connectionStatus(ctx context.Context, _ api.Caller, params json.RawMessage) (any, error) {
	if err := decodeParams(params, &struct{}{}); err != nil {
		return nil, err
	}
	if cfg.activeProvider() == "unknown" {
		return nil, settingsError()
	}
	if isRuntimeProvider(cfg.activeProvider()) || cfg.activeProvider() == "multiple" {
		result, e := cfg.connectorClient().Call(ctx, "status", nil)
		if e != nil {
			return nil, cfg.daemonError()
		}
		return result, nil
	}
	return cfg.withConnection(ctx, func(s *connectionState) (bool, error) {
		fresh, err := (provider.Fake{}).Status(ctx, s.Connection)
		if err == nil {
			s.Connection = fresh
		}
		return false, err
	})
}
func (cfg Config) connectionConnect(ctx context.Context, _ api.Caller, params json.RawMessage) (any, error) {
	var in connector.Request
	if err := decodeParams(params, &in); err != nil {
		return nil, err
	}
	if in.Retry {
		if !connector.RetryOnly(in) {
			return nil, api.BadRequest("retry accepts only a provider")
		}
		result, err := cfg.connectorClient().Call(ctx, "connect", in)
		if err != nil {
			return nil, connectorError(err)
		}
		return result, nil
	}
	if (in.Provider != "ipv6" && in.ServiceIDs != nil) || len(in.ServiceIDs) > 16 {
		return nil, api.BadRequest("invalid services")
	}
	if len(in.Token) > 4096 {
		return nil, api.BadRequest("token too long")
	}
	if in.Provider != "cloudflare" && (in.ReuseName != nil || in.Mode != "" || in.Subdomain != "") {
		return nil, cloudflareError()
	}
	if in.Provider != "tailscale" && (in.LoginServer != "" || in.LoginOrigin != "") {
		return nil, cloudflareError()
	}
	if in.Provider != "netbird" && (in.ManagementURL != "" || in.ConfirmManagementPolicy || len(in.LoginOrigins) != 0) {
		return nil, cloudflareError()
	}
	// Resolve IPv6 ports only from trusted package settings.
	if in.Provider == "ipv6" {
		if len(in.Mappings) != 0 || in.Token != "" || in.Hostname != "" || !in.ConfirmExposure {
			return nil, cloudflareError()
		}
		mappings, e := (ports.ResolvedSource{Store: ports.Store{Dir: cfg.DataDir}, Detector: cfg.portDetector()}).RuntimeMappings(ctx)
		if e != nil {
			return nil, portError(e)
		}
		if len(mappings) == 0 {
			return nil, &api.Error{Code: "ports_required", Message: "enable a resolved service port before connecting"}
		}
	}
	if isRuntimeProvider(in.Provider) {
		if cfg.activeProvider() == "unknown" {
			return nil, settingsError()
		}
		settings := provider.Settings{LoginOrigins: in.LoginOrigins, LoginServer: in.LoginServer, LoginOrigin: in.LoginOrigin, ManagementURL: in.ManagementURL, ConfirmManagementPolicy: in.ConfirmManagementPolicy, ReuseName: in.ReuseName, Mode: in.Mode, Subdomain: in.Subdomain, Token: in.Token, Hostname: in.Hostname, Mappings: in.Mappings, ConfirmExposure: in.ConfirmExposure}
		if in.Provider == "cloudflare" {
			if e := (&provider.CloudflareTunnel{}).Validate(settings); e != nil {
				return nil, connectorError(e)
			}
		}
		if in.Provider == "netbird" {
			if e := provider.NewNetBird("", nil).Validate(settings); e != nil {
				return nil, connectorError(e)
			}
		}
		if in.Provider == "tailscale" {
			if e := provider.NewTailscale("", nil).Validate(settings); e != nil {
				return nil, cloudflareError()
			}
		}
		if _, e := cfg.connectorClient().Call(ctx, "ping", nil); e != nil {
			return nil, cloudflareError()
		}
		if in.Provider != "netbird" && in.Provider != "tailscale" && !in.ConfirmExposure {
			return nil, &api.Error{Code: "provider_unavailable", Message: "administrator exposure confirmation required"}
		}
		// Route all real providers through one daemon-owned selector. Save the
		// routing marker before starting, so a CGI crash cannot hide a live child.
		if e := connector.WritePrivate(filepath.Join(cfg.DataDir, "active-provider"), []byte("multiple")); e != nil {
			return nil, settingsError()
		}
		// connectd accepts a slow start and finishes it detached from this CGI;
		// a "connecting" result is then confirmed by status polling.
		result, e := cfg.connectorClient().Call(ctx, "connect", in)
		if e != nil {
			return nil, connectorError(e)
		}
		return result, nil
	}
	// Unregistered providers fail closed before receiving credentials.
	var p provider.Provider = provider.Fake{}
	if in.Provider != p.ID() {
		return nil, &api.Error{Code: "provider_unavailable", Message: "this connection service is not enabled in this build"}
	}
	if cfg.activeProvider() == "unknown" {
		return nil, settingsError()
	}
	// Validate a demo replacement before stopping an existing real connection.
	if _, e := p.Connect(ctx, in.Token); e != nil {
		if errors.Is(e, provider.ErrInvalidToken) {
			return nil, &api.Error{Code: "invalid_demo_token", Message: "use the displayed test code"}
		}
		return nil, cloudflareError()
	}
	if isRuntimeProvider(cfg.activeProvider()) || cfg.activeProvider() == "multiple" {
		stopped, e := cfg.connectorClient().Call(ctx, "disconnect", nil)
		if e != nil {
			return nil, cloudflareError()
		}
		// An accepted disconnect may still be stopping; keep routing to the daemon.
		if stopped.Stopping() {
			return nil, &api.Error{Code: "disconnect_busy", Message: "a connection is still stopping; try again shortly"}
		}
	}
	return cfg.withConnection(ctx, func(s *connectionState) (bool, error) {
		result, err := p.Connect(ctx, in.Token)
		if errors.Is(err, provider.ErrInvalidToken) {
			return false, &api.Error{Code: "invalid_demo_token", Message: "use the displayed test code"}
		}
		if err != nil {
			return false, &api.Error{Code: "provider_unavailable", Message: "the connection could not be completed; existing settings were kept"}
		}
		*s = connectionState{1, p.ID(), result}
		if e := connector.WritePrivate(filepath.Join(cfg.DataDir, "active-provider"), []byte("fake")); e != nil {
			return false, settingsError()
		}
		return true, nil
	})
}
func (cfg Config) connectionDisconnect(ctx context.Context, _ api.Caller, params json.RawMessage) (any, error) {
	var input struct {
		Provider string `json:"provider,omitempty"`
	}
	if err := decodeParams(params, &input); err != nil {
		return nil, err
	}
	if input.Provider != "" && !isRuntimeProvider(input.Provider) {
		return nil, api.BadRequest("unknown provider")
	}
	if cfg.activeProvider() == "unknown" {
		return nil, settingsError()
	}
	if isRuntimeProvider(cfg.activeProvider()) || cfg.activeProvider() == "multiple" {
		result, e := cfg.connectorClient().Call(ctx, "disconnect", input)
		if e != nil {
			return nil, connectorError(e)
		}
		return result, nil
	}
	return cfg.withConnection(ctx, func(s *connectionState) (bool, error) {
		if err := (provider.Fake{}).Disconnect(ctx, s.Connection); err != nil {
			return false, err
		}
		changed := s.Connection != provider.Connection{}
		s.Connection = provider.Connection{}
		return changed, nil
	})
}

// connectionForget asks connectd to clear the services a disconnected
// provider kept; without a real connection there is nothing to forget.
func (cfg Config) connectionForget(ctx context.Context, c api.Caller, params json.RawMessage) (any, error) {
	var input struct {
		Provider string `json:"provider"`
	}
	if err := decodeParams(params, &input); err != nil {
		return nil, err
	}
	if !isRuntimeProvider(input.Provider) {
		return nil, api.BadRequest("unknown provider")
	}
	switch cfg.activeProvider() {
	case "unknown":
		return nil, settingsError()
	case "fake":
		return cfg.connectionStatus(ctx, c, nil)
	}
	result, e := cfg.connectorClient().Call(ctx, "forget", input)
	if e != nil {
		return nil, connectorError(e)
	}
	// Older versions kept Access emails for Cloudflare TCP services here.
	if input.Provider == "cloudflare" {
		if e := os.Remove(filepath.Join(cfg.DataDir, "cloudflare-access.json")); e != nil && !errors.Is(e, os.ErrNotExist) {
			return nil, settingsError()
		}
	}
	return result, nil
}

// connectionCheck reads one provider's check from connectd, which runs,
// rate-limits and caches it; nothing is checked before a real connection.
func (cfg Config) connectionCheck(ctx context.Context, _ api.Caller, params json.RawMessage) (any, error) {
	var input struct {
		Provider string `json:"provider"`
	}
	if err := decodeParams(params, &input); err != nil {
		return nil, err
	}
	if !connector.Checkable(input.Provider) {
		return nil, api.BadRequest("unknown provider")
	}
	switch cfg.activeProvider() {
	case "unknown":
		return nil, settingsError()
	case "fake":
		return connector.CheckResult{Provider: input.Provider, State: "not-checked"}, nil
	}
	result, e := cfg.connectorClient().Check(ctx, input.Provider)
	if e != nil {
		return nil, connectorError(e)
	}
	return result, nil
}

func (cfg Config) connectorClient() connector.Control {
	if cfg.ConnectorControl != nil {
		return cfg.ConnectorControl
	}
	return connector.Client{Socket: control.Socket}
}
func (cfg Config) activeProvider() string {
	raw, e := connector.ReadPrivate(filepath.Join(cfg.DataDir, "active-provider"))
	if os.IsNotExist(e) {
		return "fake"
	}
	if e != nil || (!isRuntimeProvider(string(raw)) && string(raw) != "fake" && string(raw) != "multiple") {
		return "unknown"
	}
	return string(raw)
}

// connectorError keeps the daemon's safe refusal codes; others stay generic.
func connectorError(e error) error {
	var coded *connector.CodedError
	if errors.As(e, &coded) {
		return api.WithDetails(&api.Error{Code: coded.Code, Message: coded.Message}, connector.Conflict(e))
	}
	return cloudflareError()
}

// daemonError names a daemon that gave up after repeated crashes; any other
// unreachable daemon stays the generic provider error.
func (cfg Config) daemonError() error {
	if _, e := os.Lstat(filepath.Join(cfg.DataDir, "connect", "stopped-after-crashes.json")); e == nil {
		return &api.Error{Code: "connect_stopped", Message: "Syno Connect Center stopped after crashing several times"}
	}
	return cloudflareError()
}
func cloudflareError() error {
	return &api.Error{Code: "provider_unavailable", Message: "connector or connection settings unavailable"}
}

func (cfg Config) lockedConnectionConnect(ctx context.Context, c api.Caller, p json.RawMessage) (any, error) {
	return cfg.lockMutation(func() (any, error) { return cfg.connectionConnect(ctx, c, p) })
}
func (cfg Config) lockedConnectionDisconnect(ctx context.Context, c api.Caller, p json.RawMessage) (any, error) {
	return cfg.lockMutation(func() (any, error) { return cfg.connectionDisconnect(ctx, c, p) })
}
func (cfg Config) lockedConnectionForget(ctx context.Context, c api.Caller, p json.RawMessage) (any, error) {
	return cfg.lockMutation(func() (any, error) { return cfg.connectionForget(ctx, c, p) })
}
func (cfg Config) lockMutation(fn func() (any, error)) (any, error) {
	f, e := os.OpenFile(filepath.Join(cfg.DataDir, "mutation.lock"), os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return nil, settingsError()
	}
	defer f.Close()
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); e != nil {
		return nil, settingsError()
	}
	defer syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return fn()
}

func isRuntimeProvider(id string) bool {
	return id == "cloudflare" || id == "tailscale" || id == "ipv6" || id == "netbird"
}
