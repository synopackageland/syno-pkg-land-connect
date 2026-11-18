// Package control composes the worker's private HTTP control plane.
package control

import (
	"context"
	"net/http"

	"dsmapp/internal/apps"
	"dsmapp/internal/binaries"
	"dsmapp/internal/connector"
	"dsmapp/internal/localipc"
	"dsmapp/internal/nbserver"
	"github.com/synopackageland/syno-pkg-land-sdk/ipc"
)

const Socket = localipc.RunDir + "/control.sock"

type Config struct {
	Apps       apps.API
	Binaries   *binaries.Service
	NetBird    *nbserver.Server
	Controller connector.Controller
	Shutdown   func()
	Restored   <-chan struct{}
	AfterApps  func()
}

// Start is called under the daemon lock, before recovery starts. Read-only
// requests remain available while mutations wait for the restore barrier.
func Start(ctx context.Context, cfg Config) (*ipc.Server, error) {
	if err := localipc.Prepare(Socket); err != nil {
		return nil, err
	}
	return ipc.StartUnix(ctx, Socket, handler(cfg))
}

func handler(cfg Config) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/apps/", AfterChange(apps.Handler(cfg.Apps), cfg.AfterApps))
	mux.Handle("/binaries/", cfg.Binaries)
	mux.Handle("/nbserver/", cfg.NetBird.Handler())
	mux.Handle("/", connector.Handler(cfg.Controller, cfg.Shutdown))
	return UntilRestored(cfg.Restored, mux)
}

var restoreReads = map[string]bool{"/apps/accessget": true, "/ping": true, "/status": true, "/shutdown": true, "/apps/list": true, "/apps/inspect": true, "/apps/inspectdsm": true, "/apps/workersstatus": true, "/binaries/status": true, "/nbserver/status": true}

func AfterChange(next http.Handler, changed func()) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		if !restoreReads[r.URL.Path] {
			go changed()
		}
	})
}

func UntilRestored(restored <-chan struct{}, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !restoreReads[r.URL.Path] {
			select {
			case <-restored:
			case <-r.Context().Done():
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
