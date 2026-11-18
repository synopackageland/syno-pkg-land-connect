package main

import (
	"bytes"
	"cmp"
	"context"
	"dsmapp/internal/app"
	"dsmapp/internal/connector"
	"dsmapp/internal/dsmexternal"
	"dsmapp/internal/dsmsecurity"
	"dsmapp/internal/ports"
	"dsmapp/internal/provider"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
)

// These in-process sessions are explicitly opt-in, loopback-only test fixtures.
// They open no tunnel, change no firewall and are never packaged in an SPK.
type fixtureDetector struct{}

func (fixtureDetector) Detect(context.Context) (ports.DSMPorts, error) {
	return ports.DSMPorts{HTTP: 5000, HTTPS: 5001}, nil
}

type fixtureSession struct{}

func (*fixtureSession) Login(context.Context, string) error { return nil }
func (*fixtureSession) Inspect(context.Context) (provider.TailscaleSnapshot, error) {
	return provider.TailscaleSnapshot{BackendState: "Running", IPs: []string{"100.64.0.1"}, DNSName: "nas.fixture.ts.net", Tailnet: "fixture", Serving: true}, nil
}
func (*fixtureSession) Configure(context.Context, []provider.PortMapping) error { return nil }
func (*fixtureSession) Stop() error                                             { return nil }

type fixtureDriver struct{}

func (fixtureDriver) Open(context.Context) (provider.TailscaleSession, error) {
	return &fixtureSession{}, nil
}

type fixtureCloudflare struct {
	mu     sync.Mutex
	state  connector.Status
	domain string
}

func (*fixtureCloudflare) ID() string { return "cloudflare" }
func (p *fixtureCloudflare) Start(_ context.Context, s provider.Settings) (connector.Status, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	host := map[string]string{"quick": "fixture-quick.trycloudflare.com", "domain": s.Subdomain + ".example.com", "token": s.Hostname}[s.Mode]
	if s.Mode == "domain" && s.Subdomain == "" {
		host = cmp.Or(p.domain, "nas.example.com")
	}
	if s.Mode == "domain" {
		p.domain = host
	}
	p.state = connector.Status{Provider: "cloudflare", State: "connected", Mode: s.Mode, Hostname: host, Endpoints: []connector.Endpoint{{URL: "https://" + host, Public: true, LocalPort: 5000}}}
	return p.state, nil
}
func (p *fixtureCloudflare) Restore(context.Context) error { return nil }
func (p *fixtureCloudflare) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.state = connector.Status{Provider: "cloudflare", State: "disconnected"}
	return nil
}
func (p *fixtureCloudflare) Disable() error { return p.Stop() }
func (p *fixtureCloudflare) Status() connector.Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.state.Provider == "" {
		return connector.Status{Provider: "cloudflare", State: "disconnected"}
	}
	return p.state
}
func (*fixtureCloudflare) Schema() []provider.Field                    { return nil }
func (*fixtureCloudflare) Endpoints() []provider.Endpoint              { return nil }
func (*fixtureCloudflare) Binaries() []provider.BinaryRequirement      { return nil }
func (*fixtureCloudflare) AcceptMappings([]provider.PortMapping) error { return nil }
func configureConnectionsFixture(cfg *app.Config) (func(), error) {
	dir := filepath.Join(cfg.DataDir, "connect")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	ts := provider.NewTailscale(filepath.Join(dir, "tailscale"), fixtureDriver{})
	m := provider.NewMultiSelection(dir, &fixtureCloudflare{}, ts)
	cfg.PortDetector = fixtureDetector{}
	phoneFixture(cfg)
	external := &fixtureExternal{}
	cfg.ExternalGet, cfg.ExternalSet = external.get, external.set
	// A fresh DSM: Auto Block off, 2-factor enforced for administrators.
	cfg.SecurityGet = func(context.Context, string, string) dsmsecurity.Status {
		return dsmsecurity.Status{AutoBlock: dsmsecurity.Off, TwoFactor: dsmsecurity.On}
	}
	m.SetPortSource(ports.ResolvedSource{Store: ports.Store{Dir: cfg.DataDir}, Detector: cfg.PortDetector})
	if e := m.Restore(context.Background()); e != nil {
		return nil, e
	}
	checker := connector.NewChecker(connector.StatusProbe(m.StatusFor))
	cfg.ConnectorControl = fixtureControl{connector.Handler(checkedFixture{m, checker}, func() {})}
	return func() { checker.Close(); m.Stop() }, nil
}

// Exercise the real worker handler in process without claiming a DSM package
// socket from this unshipped, loopback-only fixture.
type fixtureControl struct{ handler http.Handler }

func (f fixtureControl) exchange(ctx context.Context, method string, input any) (connector.Status, connector.CheckResult, error) {
	raw, err := json.Marshal(input)
	if err != nil {
		return connector.Status{}, connector.CheckResult{}, err
	}
	r := httptest.NewRequest(http.MethodPost, "/"+method, bytes.NewReader(raw)).WithContext(ctx)
	w := httptest.NewRecorder()
	f.handler.ServeHTTP(w, r)
	var reply struct {
		Status connector.Status      `json:"status"`
		Check  connector.CheckResult `json:"check"`
		Error  string                `json:"error"`
		Code   string                `json:"code"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &reply); err != nil {
		return reply.Status, reply.Check, err
	}
	if reply.Error != "" {
		if reply.Code != "" {
			return reply.Status, reply.Check, connector.Coded(reply.Code, reply.Error)
		}
		return reply.Status, reply.Check, errors.New(reply.Error)
	}
	return reply.Status, reply.Check, nil
}

func (f fixtureControl) Call(ctx context.Context, method string, input any) (connector.Status, error) {
	status, _, err := f.exchange(ctx, method, input)
	return status, err
}

func (f fixtureControl) Check(ctx context.Context, id string) (connector.CheckResult, error) {
	_, check, err := f.exchange(ctx, "check", map[string]string{"provider": id})
	return check, err
}

// checkedFixture answers connection checks like connectd does.
type checkedFixture struct {
	*provider.MultiSelection
	checker *connector.Checker
}

func (f checkedFixture) Connect(ctx context.Context, r connector.Request) (connector.Status, error) {
	f.checker.Reset(r.Provider)
	return f.MultiSelection.Connect(ctx, r)
}

func (f checkedFixture) Check(ctx context.Context, id string) (connector.CheckResult, error) {
	return f.checker.Check(ctx, id), nil
}

// fixtureExternal stands in for DSM's External Access setting; it starts unset.
type fixtureExternal struct {
	mu  sync.Mutex
	ext dsmexternal.External
}

func (f *fixtureExternal) get(context.Context, string, string) (dsmexternal.External, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ext, nil
}

func (f *fixtureExternal) set(_ context.Context, _, _ string, u dsmexternal.Update) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if u.Hostname != nil {
		f.ext.Hostname = *u.Hostname
	}
	if u.HTTPPort != nil {
		f.ext.HTTPPort = u.HTTPPort
	}
	if u.HTTPSPort != nil {
		f.ext.HTTPSPort = u.HTTPSPort
	}
	return nil
}
