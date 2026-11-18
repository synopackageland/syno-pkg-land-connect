package appaccess

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"dsmapp/internal/apps"
)

// Read the actual artifact emitted by the daemon, using the launcher's strict
// schema rather than deriving an expected map with the production projector.
func assertLaunchMap(t *testing.T, f *fixture, routes ...apps.LaunchRoute) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(filepath.Dir(f.owner.cfg.ProjectionPath), apps.LaunchFile))
	if err != nil {
		t.Fatal(err)
	}
	var got apps.LaunchMap
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&got); err != nil {
		t.Fatal(err)
	}
	want := apps.LaunchMap{Schema: 1, Routes: append([]apps.LaunchRoute{}, routes...)}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("launch map: %s; want %+v", raw, want)
	}
}

func workerLaunchRoute(state apps.State, status string) apps.LaunchRoute {
	scope := state.Apps["ExampleApp"].Public
	return apps.LaunchRoute{Provider: apps.Workers, DSM: "https://" + state.DSM.Public.Authority, Apps: map[string]apps.LaunchApp{
		"ExampleApp": {Origin: scope.Origin, Port: scope.Binding.Port, State: status},
	}}
}

func TestWorkersLaunchMapRecoveryAndWithdrawal(t *testing.T) {
	for _, action := range []string{"clear", "disable", "disconnect"} {
		t.Run(action, func(t *testing.T) {
			f := newFixture(t, false)
			withWorkers(t, f)
			ctx := context.Background()
			approval := inspect(t, f, "OtherApp", apps.Private)
			if _, err := f.api.SetPrivate(ctx, apps.PrivateRequest{Package: "OtherApp", Enabled: true, Approval: &approval}); err != nil {
				t.Fatal(err)
			}
			approveWorker(t, f, "ExampleApp")
			state, err := f.store.Load()
			if err != nil {
				t.Fatal(err)
			}
			private := state.Apps["OtherApp"].Private
			lan := apps.LaunchRoute{Provider: "tailscale", DSM: "https://" + state.DSM.Private.Authority, Apps: map[string]apps.LaunchApp{
				"OtherApp": {Origin: "https://" + private.Authority, Port: private.Binding.Port, State: apps.Active},
			}}
			public := workerLaunchRoute(state, apps.Active)
			assertLaunchMap(t, f, public, lan)
			// Startup must reconstruct a missing menu map from approvals alone.
			if err := os.Remove(filepath.Join(filepath.Dir(f.owner.cfg.ProjectionPath), apps.LaunchFile)); err != nil {
				t.Fatal(err)
			}
			f.owner.stopAll()
			if err := f.owner.Lifecycle().Recover(ctx); err != nil {
				t.Fatal(err)
			}
			assertLaunchMap(t, f, public, lan)
			switch action {
			case "clear":
				err = f.api.Clear(ctx, apps.ScopedChange{Package: "ExampleApp"})
			case "disable":
				_, err = f.api.SetPublic(ctx, apps.PublicRequest{Provider: apps.Workers, Packages: []string{"ExampleApp"}, Enabled: false})
			case "disconnect":
				err = f.api.(apps.WorkersAPI).WorkersDisconnect(ctx)
			}
			if err != nil {
				t.Fatal(err)
			}
			if action == "disconnect" {
				assertLaunchMap(t, f, lan)
			} else {
				public.Apps = map[string]apps.LaunchApp{}
				assertLaunchMap(t, f, public, lan)
				if err := f.api.(apps.DSMClearAPI).ClearDSM(ctx, apps.DSMInspectRequest{Scope: apps.Public, Provider: apps.Workers}); err != nil {
					t.Fatal(err)
				}
				assertLaunchMap(t, f, lan)
			}
		})
	}
}
