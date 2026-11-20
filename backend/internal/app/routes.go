// Package routes register handlers. All access rules come from
// internal/api/permissions.json and default to DSM administrators.
package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"runtime"
	"time"

	"dsmapp/internal/apps"
	"dsmapp/internal/binaries"
	"dsmapp/internal/connector"
	"dsmapp/internal/dsmexternal"
	"dsmapp/internal/dsmsecurity"
	"dsmapp/internal/helper"
	"dsmapp/internal/ipv6"
	"dsmapp/internal/phonelink"
	"dsmapp/internal/ports"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

// Config is fixed at build time and startup, never taken from a request.
type Config struct {
	BinaryControl    binaries.Control  // Startup-only daemon transport; never browser supplied.
	ConnectorControl connector.Control // Startup-only daemon transport; never browser supplied.
	PackageID        string
	Version          string
	HelperInventory  helper.Inventory
	// DataDir is the package's persistent, package-owned directory
	// (/var/packages/<package>/var). It survives upgrades.
	DataDir     string
	ExternalGet func(context.Context, string, string) (dsmexternal.External, error)
	ExternalSet func(context.Context, string, string, dsmexternal.Update) error
	SecurityGet func(context.Context, string, string) dsmsecurity.Status
	// IPv6 is a trusted, read-only diagnostic dependency; no request supplies ports.
	IPv6 interface {
		Inspect(context.Context) ipv6.Status
	}
	PortDetector ports.Detector
	Apps         apps.API // Trusted startup dependency; never supplied by a browser.
	// NetBirdServer is a startup-only daemon transport, like BinaryControl.
	NetBirdServer NetBirdServerControl
	// GitHubHTTP checks a GitHub token before it is saved; nil uses a default client.
	GitHubHTTP *http.Client
	// PhoneLink replaces the live address sources in tests; nil reads the system.
	PhoneLink *phonelink.Source
	// LANSwitch reaches the LAN switch owner in connectd; nil uses the control socket.
	LANSwitch LANSwitchControl
}

func Routes(cfg Config) []api.Route {
	routes := []api.Route{
		api.Handle("ctl.cloudflare.access.status", cfg.accessControlRoute("get")),
		api.Handle("ctl.cloudflare.access.set", cfg.accessControlRoute("apply")),
		api.Handle("ctl.cloudflare.access.sync", cfg.accessControlRoute("sync")),
		api.Handle("cloudflare.access.get", cfg.accessRoute("get")),
		api.Handle("cloudflare.access.set", cfg.accessRoute("apply")),
		api.Handle("cloudflare.access.sync", cfg.accessRoute("sync")),
		api.Handle("cloudflare.access.token", cfg.accessRoute("token")),
		api.Handle("apps.list", cfg.appsList),
		api.Handle("apps.setrelay", cfg.appsSetRelay),
		api.Handle("apps.relayconnect", cfg.appsRelayConnect),
		api.Handle("apps.relaydisconnect", cfg.appsWorkersDisconnect),
		api.Handle("apps.cleardsm", cfg.appsClearDSM),
		api.Handle("apps.resetstate", cfg.appsResetState),
		api.Handle("apps.workersstatus", cfg.appsWorkersStatus),
		api.Handle("apps.workersconnect", cfg.appsWorkersConnect),
		api.Handle("apps.workersdisconnect", cfg.appsWorkersDisconnect),
		api.Handle("apps.workerspublishdsm", cfg.appsWorkersPublishDSM),
		api.Handle("apps.inspect", cfg.appsInspect),
		api.Handle("apps.cloudflareconnect", cfg.appsCloudflareConnect),
		api.Handle("apps.cloudflaredisconnect", cfg.appsCloudflareDisconnect),
		api.Handle("apps.cleanuporphans", cfg.appsCleanupOrphans),
		api.Handle("apps.setprivate", cfg.appsSetPrivate),
		api.Handle("apps.setpublic", cfg.appsSetPublic),
		api.Handle("apps.reenable", cfg.appsReenable),
		api.Handle("apps.clear", cfg.appsClear),
		api.Handle("apps.inspectdsm", cfg.appsInspectDSM),
		api.Handle("apps.setdsm", cfg.appsSetDSM),
		api.Handle("apps.customadd", cfg.appsCustomAdd),
		api.Handle("apps.customedit", cfg.appsCustomEdit),
		api.Handle("apps.customremove", cfg.appsCustomRemove),
		api.Handle("apps.custompause", cfg.appsCustomPause),
		api.Handle("apps.customcandidates", cfg.appsCustomCandidates),
		api.Handle("binaries.status", cfg.binaryRoute("status")),
		api.Handle("binaries.check", cfg.binaryRoute("check")),
		api.Handle("binaries.update", cfg.binaryRoute("update")),
		api.Handle("binaries.rollback", cfg.binaryRoute("rollback")),
		api.Handle("github.token.status", cfg.githubTokenStatus),
		api.Handle("github.token.set", cfg.githubTokenSet),
		api.Handle("github.token.remove", cfg.githubTokenRemove),
		api.Handle("netbirdserver.status", cfg.netbirdServerRoute("status")),
		api.Handle("netbirdserver.setup", cfg.netbirdServerRoute("setup")),
		api.Handle("netbirdserver.disable", cfg.netbirdServerRoute("disable")),
		api.Handle("netbirdserver.credentials", cfg.netbirdServerRoute("credentials")),
		api.Handle("integration.status", cfg.integrationStatus),
		api.HandleRequest("app.status", cfg.status),
		api.Handle("phone.addnas", cfg.phoneAddNAS),
		api.Handle("phone.lanprobe", cfg.phoneLANProbe),
		api.Handle("lanswitch.get", cfg.lanSwitchGet),
		api.Handle("lanswitch.set", cfg.lanSwitchSet),
		api.Handle("connection.status", cfg.connectionStatus),
		api.Handle("ctl.connection.status", cfg.controlConnectionStatus),
		api.Handle("ctl.connection.connect", cfg.controlConnectionConnect),
		api.Handle("ctl.connection.forget", cfg.controlConnectionForget),
		api.Handle("ctl.connection.disconnect", cfg.controlConnectionDisconnect),
		api.Handle("ctl.ports.list", cfg.controlPortsList),
		api.Handle("ctl.ports.upsert", cfg.controlPortMutation(cfg.upsertPort)),
		api.Handle("ctl.ports.delete", cfg.controlPortMutation(cfg.deletePort)),
		api.Handle("ctl.ports.dsmset", cfg.controlPortMutation(cfg.setDSMPort)),
		api.Handle("ctl.ports.followset", cfg.controlPortMutation(cfg.setFollowPorts)),
		api.Handle("ctl.ipv6.status", cfg.controlIPv6Status),
		api.HandleRequest("ctl.diagnose", cfg.controlDiagnose),
		api.Handle("advisor.recommend", cfg.recommend),
		api.Handle("connection.connect", cfg.lockedConnectionConnect),
		api.Handle("connection.disconnect", cfg.lockedConnectionDisconnect),
		api.Handle("connection.check", cfg.connectionCheck),
		api.Handle("connection.forget", cfg.lockedConnectionForget),
		api.HandleRequest("external.get", cfg.externalGet),
		api.HandleRequest("external.status", cfg.externalStatus),
		api.HandleRequest("external.set", cfg.externalSet),
		api.Handle("ipv6.status", cfg.ipv6Status),
		api.HandleRequest("security.status", cfg.securityStatus),
		api.Handle("ports.list", cfg.listPorts),
		api.Handle("ports.upsert", cfg.upsertPort),
		api.Handle("ports.delete", cfg.deletePort),
		api.Handle("ports.dsmset", cfg.setDSMPort),
		api.Handle("ports.followset", cfg.setFollowPorts),
	}
	return append(append(routes, previewRoutes()...), cfg.rootRoutes()...)
}

func (cfg Config) integrationStatus(ctx context.Context, _ api.Caller, params json.RawMessage) (any, error) {
	if err := decodeParams(params, &struct{}{}); err != nil {
		return nil, err
	}
	return helper.NewClient(cfg.HelperInventory).Status(ctx), nil
}

// status is the SDK's api.AppStatus (package, version, caller, and the
// lang/locale of the caller's DSM display language: the entry page's
// dsmLanguage hint, else the user's last hint, else the browser's language)
// with Connect's NAS name and runtime metadata.
func (cfg Config) status(ctx context.Context, caller api.Caller, r *http.Request, params json.RawMessage) (any, error) {
	base, err := api.AppStatusHandler(cfg.PackageID, cfg.Version, nil)(ctx, caller, r, params)
	if err != nil {
		return nil, err
	}
	nasName, _ := os.Hostname()
	return struct {
		api.AppStatus
		NASName   string         `json:"nasName"`
		Process   map[string]int `json:"process"`
		GoVersion string         `json:"go_version"`
		Platform  string         `json:"platform"`
		Time      string         `json:"time"`
	}{
		AppStatus: base.(api.AppStatus),
		NASName:   nasName,
		Process:   map[string]int{"uid": os.Getuid(), "gid": os.Getgid()},
		GoVersion: runtime.Version(),
		Platform:  runtime.GOOS + "/" + runtime.GOARCH,
		Time:      time.Now().UTC().Format(time.RFC3339),
	}, nil
}

// decodeParams decodes params strictly: unknown fields are an error.
func decodeParams(params json.RawMessage, into any) error {
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return api.BadRequest("invalid params: " + err.Error())
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return api.BadRequest("invalid params: trailing data")
	}
	return nil
}

func (cfg Config) externalGet(ctx context.Context, _ api.Caller, request *http.Request, params json.RawMessage) (any, error) {
	if e := decodeParams(params, &struct{}{}); e != nil {
		return nil, e
	}
	result, e := cfg.readExternal(ctx, request)
	if e != nil {
		return nil, &api.Error{Code: "external_unavailable", Message: "DSM external settings could not be read"}
	}
	return result, nil
}
