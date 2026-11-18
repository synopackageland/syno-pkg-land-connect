package appaccess

import (
	"context"
	"dsmapp/internal/apps"
	"os"
	"path/filepath"
	"testing"
)

func TestPublicHostnameSelection(t *testing.T) {
	if got, err := selectHostname("example-old.example.com", "chosen.example.com", "example.com"); err != nil || got != "chosen.example.com" {
		t.Fatalf("custom name: %q %v", got, err)
	}
	for _, host := range []string{"evil.invalid", "nested.name.example.com", "https://name.example.com", "name.example.com:443", "-bad.example.com"} {
		if _, err := selectHostname("default.example.com", host, "example.com"); err == nil {
			t.Errorf("accepted %q", host)
		}
	}
}

func TestPublicNamesAreReadableAndKeepSavedNames(t *testing.T) {
	ctx := context.Background()
	first, second := newFixture(t, false), newFixture(t, false)
	a, err := first.api.Inspect(ctx, apps.InspectRequest{Package: "ExampleApp", Scope: apps.Public})
	if err != nil {
		t.Fatal(err)
	}
	b, err := second.api.Inspect(ctx, apps.InspectRequest{Package: "ExampleApp", Scope: apps.Public})
	if err != nil || a.Hostname != "exampleapp.example.com" || b.Hostname != a.Hostname {
		t.Fatalf("colliding defaults: %+v %+v %v", a, b, err)
	}
	custom, err := first.api.Inspect(ctx, apps.InspectRequest{Package: "ExampleApp", Scope: apps.Public, Hostname: "chosen.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	approved := apps.Approved{Hostname: custom.Hostname, Port: custom.Port, Pin: *custom.Pin, DSM: custom.DSM, Token: custom.Token}
	tampered := approved
	tampered.Hostname = "different.example.com"
	if _, err = first.api.SetPublic(ctx, apps.PublicRequest{Packages: []string{"ExampleApp"}, Enabled: true, Approvals: map[string]apps.Approved{"ExampleApp": tampered}}); apps.ErrorCode(err) != "apps_confirmation_changed" {
		t.Fatalf("unbound name: %v", err)
	}
	if _, err = first.api.SetPublic(ctx, apps.PublicRequest{Packages: []string{"ExampleApp"}, Enabled: true, Approvals: map[string]apps.Approved{"ExampleApp": approved}}); err != nil {
		t.Fatal(err)
	}
	state, err := first.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	target := apps.Target{Package: "ExampleApp", Kind: apps.Public, Scope: *state.Apps["ExampleApp"].Public}
	if err = first.owner.Validate(ctx, target); err != nil {
		t.Fatalf("custom name failed runtime validation: %v", err)
	}
	// Seed a legacy name through durable state, then run the current default derivation.
	if err = first.store.Update(ctx, func(s *apps.State) error {
		v := s.Apps["ExampleApp"]
		v.Public.Authority = "exampleapp.example.com"
		v.Public.Origin = "https://exampleapp.example.com"
		s.Apps["ExampleApp"] = v
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(first.store.Dir, "public-name-id"), []byte("broken"), 0600); err != nil {
		t.Fatal(err)
	}
	host, _, err := first.owner.authority(ctx, "ExampleApp", apps.Public)
	if err != nil || host != "exampleapp.example.com" {
		t.Fatalf("migrated legacy name: %q %v", host, err)
	}
}

func TestCustomWorkerAppAndDSMNamesUseTheConfirmedAuthorities(t *testing.T) {
	f := newFixture(t, false)
	withWorkers(t, f)
	ctx := context.Background()
	in, err := f.api.Inspect(ctx, apps.InspectRequest{Package: "ExampleApp", Scope: "relay", Hostname: "my-app.example.workers.dev", DSMHostname: "my-nas.example.workers.dev"})
	if err != nil || in.DSM == nil {
		t.Fatalf("inspect: %+v %v", in, err)
	}
	approval := apps.Approved{Hostname: in.Hostname, Port: in.Port, Pin: *in.Pin, DSM: in.DSM, Token: in.Token}
	if _, err = f.api.SetPublic(ctx, apps.PublicRequest{Provider: apps.Workers, Packages: []string{"ExampleApp"}, Enabled: true, Approvals: map[string]apps.Approved{"ExampleApp": approval}}); err != nil {
		t.Fatal(err)
	}
	state, err := f.store.Load()
	if err != nil || state.DSM.Public.Authority != "my-nas.example.workers.dev" || state.Apps["ExampleApp"].Public.Authority != "my-app.example.workers.dev" {
		t.Fatalf("wrong authorities: %+v %v", state, err)
	}
	if err = f.owner.Check(ctx); err != nil {
		t.Fatalf("names did not survive reconciliation: %v", err)
	}
}
