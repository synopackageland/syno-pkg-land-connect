package appaccess

import (
	"context"
	"os"
	"testing"
	"time"

	"dsmapp/internal/apps"
	"dsmapp/internal/relay"
	"dsmapp/internal/workers"
)

// Each generation is a fresh owner over the same appdata, as stop/start,
// upgrade and boot do. TLS origins, sockets, projections and the real Workers
// manager are exercised; only Cloudflare's API and relay transport are fake.
func TestRecoveryRestoresEveryPublishedCombination(t *testing.T) {
	for _, way := range []string{apps.Workers, "cloudflare", "tailscale"} {
		for _, publication := range []string{"dsm", "apps", "both", "custom"} {
			if way == "tailscale" && publication == "custom" {
				continue
			} // Custom URLs are public-only.
			t.Run(way+"/"+publication, func(t *testing.T) {
				f := newFixture(t, false)
				ctx := context.Background()
				var manager *workers.Manager
				if way == apps.Workers {
					manager = withWorkers(t, f)
				}
				if publication == "custom" {
					backend := customBackend(t)
					added, err := f.api.(apps.CustomAPI).CustomAdd(ctx, apps.CustomRequest{Name: "Media", URL: backend.URL, AppSignIn: true, AcceptRisk: true})
					if err != nil {
						t.Fatal(err)
					}
					approveCustom(t, f, added.ID, way)
				} else if way == apps.Workers {
					approveWorker(t, f, "ExampleApp")
				} else {
					kind := apps.Public
					if way == "tailscale" {
						kind = apps.Private
					}
					approval := inspect(t, f, "ExampleApp", kind)
					if kind == apps.Private {
						if _, err := f.api.SetPrivate(ctx, apps.PrivateRequest{Package: "ExampleApp", Enabled: true, Approval: &approval}); err != nil {
							t.Fatal(err)
						}
					} else if _, err := f.api.SetPublic(ctx, apps.PublicRequest{Packages: []string{"ExampleApp"}, Enabled: true, Approvals: map[string]apps.Approved{"ExampleApp": approval}}); err != nil {
						t.Fatal(err)
					}
				}
				if err := f.store.Update(ctx, func(s *apps.State) error {
					if publication == "dsm" {
						s.Apps = map[string]apps.AppState{}
					}
					// Native App-only selections retain DSM as their SSO dependency.
					if publication == "custom" {
						s.DSM = nil
					}
					return nil
				}); err != nil {
					t.Fatal(err)
				}
				for _, generation := range []string{"stop-start", "upgrade-new-owner", "boot-new-owner"} {
					t.Run(generation, func(t *testing.T) {
						f.owner.Close()
						cfg := f.owner.cfg
						cfg.Registry = nil
						started := make(chan relay.Config, 8)
						if manager != nil {
							manager.Close()
							manager = &workers.Manager{Store: manager.Store, API: manager.API, Run: func(ctx context.Context, c relay.Config) {
								c.Connected(true)
								defer c.Connected(false)
								started <- c
								<-ctx.Done()
							}}
							cfg.Workers = manager
							t.Cleanup(manager.Close)
						}
						f.owner = New(ctx, cfg)
						t.Cleanup(f.owner.Close)
						f.private.engine = f.owner
						f.public.owner = f.owner
						if err := f.owner.Lifecycle().Recover(ctx); err != nil {
							t.Fatal(err)
						}
						state, err := f.store.Load()
						if err != nil {
							t.Fatal(err)
						}
						bindings := apps.PinBindings(state)
						if len(bindings) == 0 {
							t.Fatal("fixture has no approved routes")
						}
						for _, b := range bindings {
							if !f.owner.allowed[b.Socket] {
								t.Fatalf("%s %s never restored", b.Target, b.Scope)
							}
							code, _, err := request(t, f.store.Dir, b.Socket, b.Authority)
							if err != nil || code != 200 {
								t.Fatalf("restored route: %d %v", code, err)
							}
						}
						switch way {
						case apps.Workers:
							for range bindings {
								select {
								case <-started:
								case <-time.After(time.Second):
									t.Fatal("relay Run was not started")
								}
							}
							if status, err := manager.Status(ctx); err != nil || status.RelayState != "connected" {
								t.Fatal(status, err)
							}
						case "tailscale":
							if f.private.count() != len(bindings) {
								t.Fatal("private route refresh lost a binding")
							}
						case "cloudflare":
							targets, err := f.owner.ActivePublic(ctx)
							if err != nil || len(targets) != len(state.Apps) {
								t.Fatal("public routes lost", err)
							}
						}
						if err := f.owner.Check(ctx); err != nil {
							t.Fatal("safety recovery", err)
						}
						if _, err := os.Stat(cfg.ProjectionPath); err != nil {
							t.Fatal(err)
						}
					})
				}
			})
		}
	}
}
