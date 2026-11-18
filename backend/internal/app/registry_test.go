package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	appapi "dsmapp/internal/api"
	"dsmapp/internal/binaries"
	"dsmapp/internal/connector"
	"dsmapp/internal/dsmexternal"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

func TestRegistryHandlerResults(t *testing.T) {
	var registry struct {
		Operations map[string]struct{ AI, Preview string }
	}
	if err := json.Unmarshal(appapi.Operations(), &registry); err != nil {
		t.Fatal(err)
	}
	results := map[string][]any{}
	for name, op := range registry.Operations {
		if op.AI == "hidden" || op.AI == "preview" {
			continue
		}
		cfg := testConfig(t)
		cfg.PortDetector = &portFixture{http: 15000, https: 15001}
		params := sampleParams[name]
		if name == "external.set" {
			params = `{"hostname":"service.example.com","httpsPort":443}`
			value := dsmexternal.External{}
			cfg.ExternalGet = func(context.Context, string, string) (dsmexternal.External, error) { return value, nil }
			cfg.ExternalSet = func(_ context.Context, _, _ string, u dsmexternal.Update) error {
				value.Hostname = *u.Hostname
				value.HTTPSPort = u.HTTPSPort
				return nil
			}
		}
		if name == "binaries.rollback" {
			for _, version := range []string{"1.0.0", "1.1.0"} {
				if err := binaries.New(filepath.Join(cfg.DataDir, "binaries"), binaryFixtureSource{version: version}).Update(context.Background(), "cloudflared"); err != nil {
					t.Fatal(err)
				}
			}
		}
		if name == "ctl.ports.delete" {
			code, out := call(t, cfg, alice, "ports.upsert", sampleParams["ports.upsert"])
			if code != "" {
				t.Fatal(code)
			}
			id := out["data"].(map[string]any)["entries"].([]any)[2].(map[string]any)["id"]
			raw, _ := json.Marshal(map[string]any{"id": id})
			params = string(raw)
		}
		if name == "ctl.connection.connect" {
			params = `{"provider":"netbird"}`
			cfg.ConnectorControl = &registryConnector{status: connector.Status{Provider: "netbird", State: "needs-login", LoginURL: "https://login.example.com/device", UserCode: "device-code"}}
		}
		if name == "binaries.check" || name == "binaries.update" {
			source := blockedBinarySource{release: make(chan struct{})}
			service := binaries.NewService(binaries.New(filepath.Join(cfg.DataDir, "binaries"), source), nil)
			defer service.Close()
			defer close(source.release)
			cfg.BinaryControl = service
		}
		if op.Preview != "" {
			before, _ := os.ReadDir(cfg.DataDir)
			code, out := call(t, cfg, alice, op.Preview, params)
			after, _ := os.ReadDir(cfg.DataDir)
			if code != "" || len(before) != len(after) {
				t.Fatalf("preview %s mutated or refused: %s %v", name, code, out)
			}
			results[op.Preview] = append(results[op.Preview], out["data"])
		}
		code, out := call(t, cfg, alice, name, params)
		if code != "" {
			t.Fatalf("%s: %s %v", name, code, out)
		}
		results[name] = append(results[name], out["data"])
	}
	// Preserve accepted connection state and the browser ceremony, including the
	// device code. Neither is a completed connection or automated authorization.
	for _, state := range []string{"connecting", "needs-login", "connected"} {
		cfg := testConfig(t)
		cfg.ConnectorControl = &registryConnector{status: connector.Status{Provider: "netbird", State: state, LoginURL: "https://login.example.com/device", UserCode: "device-code", LoginDeadline: "2030-01-01T00:00:00Z"}}
		for _, name := range []string{"ctl.connection.connect", "ctl.connection.status"} {
			if name == "ctl.connection.status" {
				if err := connector.WritePrivate(filepath.Join(cfg.DataDir, "active-provider"), []byte("multiple")); err != nil {
					t.Fatal(err)
				}
			}
			params := `{}`
			if name == "ctl.connection.connect" {
				params = `{"provider":"netbird"}`
			}
			code, out := call(t, cfg, alice, name, params)
			if code != "" {
				t.Fatal(code, out)
			}
			results[name] = append(results[name], out["data"])
		}
	}
	cfg := testConfig(t)
	cfg.ConnectorControl = &registryConnector{err: connector.Coded("binary_required", "connector not installed")}
	code, out := call(t, cfg, alice, "ctl.connection.connect", `{"provider":"netbird"}`)
	if code != "" {
		t.Fatal(code, out)
	}
	results["ctl.connection.connect"] = append(results["ctl.connection.connect"], out["data"])
	if file := os.Getenv("CONNECT_REGISTRY_RESULTS"); file != "" {
		raw, _ := json.Marshal(results)
		if err := os.WriteFile(file, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

type registryConnector struct {
	status connector.Status
	err    error
}

func (c *registryConnector) Call(_ context.Context, method string, _ any) (connector.Status, error) {
	if method == "ping" {
		return connector.Status{}, nil
	}
	return c.status, c.err
}
func (c *registryConnector) Check(context.Context, string) (connector.CheckResult, error) {
	return connector.CheckResult{Provider: "netbird", State: "checking"}, nil
}

func TestDraftPreviewsNeverCallMutationDependencies(t *testing.T) {
	cfg := testConfig(t)
	cfg.ConnectorControl = &registryConnector{}
	cfg.ExternalSet = func(context.Context, string, string, dsmexternal.Update) error {
		t.Fatal("preview called DSM write")
		return nil
	}
	code, out := call(t, cfg, alice, "external.set.preview", `{"hostname":"service.example.com"}`)
	if code != "" || out["data"].(map[string]any)["requiresConfirmation"] != true {
		t.Fatal(code, out)
	}
	assertDataDirEmpty(t, cfg)
	// Unknown preview inputs are not reflected, and cannot turn into execution.
	code, _ = call(t, cfg, alice, "external.set.preview", `[]`)
	if code != "bad_request" {
		t.Fatal(code)
	}
}

func TestRuntimeRegistryRejectsMissingHandlersAndPermissionDrift(t *testing.T) {
	permissions, err := appapi.Permissions()
	if err != nil {
		t.Fatal(err)
	}
	routes := Routes(testConfig(t))
	if err := api.ValidateOperationRegistry(appapi.Operations(), permissions, routes...); err != nil {
		t.Fatal(err)
	}
	if err := api.ValidateOperationRegistry(appapi.Operations(), permissions, routes[1:]...); err == nil {
		t.Fatal("missing handler accepted")
	}
	var doc map[string]any
	json.Unmarshal(appapi.Operations(), &doc)
	doc["operations"].(map[string]any)["app.status"].(map[string]any)["permission"] = "authenticated"
	raw, _ := json.Marshal(doc)
	if err := api.ValidateOperationRegistry(raw, permissions, routes...); err == nil {
		t.Fatal("permission drift accepted")
	}
}
