package workers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"dsmapp/internal/accounttoken"
	"dsmapp/internal/apps"
	"dsmapp/internal/connector"
)

const fixtureZoneID = "fedcba9876543210fedcba9876543210"

// zoneCloud fakes the zone, route and script calls the Signal bridge uses.
type zoneCloud struct {
	mu                sync.Mutex
	account           string
	zoneHidden        bool
	routeDenied       bool
	tags              map[string][]string
	scripts           map[string]string
	routes            map[string][2]string // id -> pattern, script
	next              int
	uploads           int
	workersDev        []bool
	compatibilities   [][]string
	subdomain         string
	claimed           int
	routeReads        int
	broken            bool
	relayDeleteDenied bool
}

func zoneFixture(t *testing.T, script string) (*Manager, *zoneCloud) {
	t.Helper()
	c := &zoneCloud{subdomain: "example", account: fixtureAccount, tags: map[string][]string{}, scripts: map[string]string{}, routes: map[string][2]string{}}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.mu.Lock()
		defer c.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer "+fixtureToken) {
			t.Error("token absent")
		}
		refuse := func(status int) {
			w.WriteHeader(status)
			fmt.Fprintf(w, `{"success":false,"errors":[{"message":%q}]}`, fixtureToken)
		}
		path := strings.TrimPrefix(r.URL.Path, "/accounts/"+fixtureAccount)
		zoneRoutes := "/zones/" + fixtureZoneID + "/workers/routes"
		var out any = map[string]any{}
		switch {
		case r.URL.Path == "/accounts":
			out = []map[string]string{{"id": fixtureAccount}}
		case r.URL.Path == "/user/tokens/verify":
			out = map[string]string{"status": "active"}
		case path == "/workers/subdomain":
			if r.Method == "PUT" {
				c.claimed++
				var in struct{ Subdomain string }
				json.NewDecoder(r.Body).Decode(&in)
				c.subdomain = in.Subdomain
			}
			if c.subdomain == "" {
				refuse(404)
				return
			}
			out = map[string]string{"subdomain": c.subdomain}
		case r.URL.Path == "/zones":
			if r.URL.Query().Get("name") != "example.com" {
				t.Error("zone query", r.URL.RawQuery)
			}
			list := []any{}
			if !c.zoneHidden {
				list = append(list, map[string]any{"id": fixtureZoneID, "name": "example.com", "account": map[string]string{"id": c.account}})
			}
			out = list
		case r.URL.Path == zoneRoutes && r.Method == "GET":
			c.routeReads++
			if c.broken {
				refuse(502)
				return
			}
			if c.routeDenied {
				refuse(403)
				return
			}
			list := []map[string]string{}
			for id, p := range c.routes {
				list = append(list, map[string]string{"id": id, "pattern": p[0], "script": p[1]})
			}
			out = list
		case r.URL.Path == zoneRoutes && r.Method == "POST":
			if c.routeDenied {
				refuse(403)
				return
			}
			var in struct{ Pattern, Script string }
			json.NewDecoder(r.Body).Decode(&in)
			c.next++
			id := fmt.Sprintf("route%d", c.next)
			c.routes[id] = [2]string{in.Pattern, in.Script}
			out = map[string]string{"id": id}
		case strings.HasPrefix(r.URL.Path, zoneRoutes+"/") && r.Method == "DELETE":
			id := strings.TrimPrefix(r.URL.Path, zoneRoutes+"/")
			if _, ok := c.routes[id]; !ok {
				refuse(404)
				return
			}
			delete(c.routes, id)
		case strings.HasSuffix(path, "/settings"):
			name := strings.TrimSuffix(strings.TrimPrefix(path, "/workers/scripts/"), "/settings")
			tags, ok := c.tags[name]
			if !ok {
				refuse(404)
				return
			}
			out = map[string]any{"tags": tags}
		case strings.HasSuffix(path, "/subdomain"):
			var in map[string]bool
			json.NewDecoder(r.Body).Decode(&in)
			c.workersDev = append(c.workersDev, in["enabled"])
		case strings.HasPrefix(path, "/workers/scripts/"):
			name := strings.TrimPrefix(path, "/workers/scripts/")
			if r.Method == "DELETE" {
				if c.relayDeleteDenied && !strings.HasPrefix(name, "connect-nbsignal-") {
					refuse(403)
					return
				}
				// Deleting a Worker deletes its routes too.
				name = strings.TrimSuffix(name, "?force=true")
				delete(c.tags, name)
				delete(c.scripts, name)
				for id, rt := range c.routes {
					if rt[1] == name {
						delete(c.routes, id)
					}
				}
				break
			}
			if err := r.ParseMultipartForm(2 << 20); err != nil {
				t.Error(err)
				return
			}
			var meta struct {
				Tags     []string `json:"tags"`
				Main     string   `json:"main_module"`
				Flags    []string `json:"compatibility_flags"`
				Bindings []any    `json:"bindings"`
			}
			if json.Unmarshal([]byte(r.FormValue("metadata")), &meta) != nil || meta.Main != "bridge.js" || len(meta.Bindings) != 0 {
				t.Error("bad metadata", r.FormValue("metadata"))
			}
			f, _, err := r.FormFile("bridge.js")
			if err != nil {
				t.Error(err)
				return
			}
			var b strings.Builder
			buf := make([]byte, 4096)
			for {
				n, err := f.Read(buf)
				b.Write(buf[:n])
				if err != nil {
					break
				}
			}
			c.tags[name], c.scripts[name] = meta.Tags, b.String()
			c.compatibilities = append(c.compatibilities, meta.Flags)
			c.uploads++
		default:
			t.Error("unexpected API endpoint", r.Method, r.URL.Path)
		}
		json.NewEncoder(w).Encode(map[string]any{"success": true, "result": out})
	}))
	t.Cleanup(s.Close)
	m := &Manager{Store: Store{Dir: filepath.Join(t.TempDir(), "private")}, API: API{Base: s.URL, HTTP: s.Client(), Script: []byte("export default {}"), Bridge: []byte(script)}}
	t.Cleanup(m.Close)
	return m, c
}

const fixturePattern = "netbird.example.com/signalexchange.SignalExchange/ConnectStream*"

func (c *zoneCloud) only() (name string, route [2]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for n := range c.scripts {
		name = n
	}
	for _, r := range c.routes {
		if strings.Contains(r[0], "ConnectStream") {
			route = r
		}
	}
	return
}

func TestSignalBridgeDeploysScriptAndRouteOnceAndUpdatesOnNewVersion(t *testing.T) {
	m, c := zoneFixture(t, "export default {} // v1")
	connectFixture(t, m)
	ctx := context.Background()
	if err := m.EnsureSignalBridge(ctx, "example.com", "netbird.example.com"); err != nil {
		t.Fatal(err)
	}
	name, route := c.only()
	if !strings.HasPrefix(name, "connect-nbsignal-") || len(c.scripts) != 1 || c.scripts[name] != "export default {} // v1" {
		t.Fatal(name, c.scripts)
	}
	if route != [2]string{fixturePattern, name} || len(c.routes) != 2 {
		t.Fatal(c.routes)
	}
	if len(c.workersDev) != 1 || c.workersDev[0] {
		t.Fatal("the bridge must not answer on workers.dev", c.workersDev)
	}
	if len(c.compatibilities[0]) != 1 || c.compatibilities[0][0] != "enable_request_signal" {
		t.Fatal(c.compatibilities)
	}
	// The same version is left alone.
	if err := m.EnsureSignalBridge(ctx, "example.com", "netbird.example.com"); err != nil || c.uploads != 1 || len(c.routes) != 2 {
		t.Fatal(err, c.uploads, c.routes)
	}
	// A Connect update brings a new script: it replaces the old one in place.
	m.API.Bridge = []byte("export default {} // v2")
	if err := m.EnsureSignalBridge(ctx, "example.com", "netbird.example.com"); err != nil || c.uploads != 2 || c.scripts[name] != "export default {} // v2" || len(c.routes) != 2 {
		t.Fatal(err, c.uploads, c.scripts, c.routes)
	}
	// A route removed in Cloudflare's dashboard comes back.
	c.mu.Lock()
	clear(c.routes)
	c.mu.Unlock()
	if err := m.EnsureSignalBridge(ctx, "example.com", "netbird.example.com"); err != nil || len(c.routes) != 2 || c.uploads != 2 {
		t.Fatal(err, c.routes)
	}
}

func TestSignalBridgeRemovalDeletesOnlyItsRouteAndScript(t *testing.T) {
	m, c := zoneFixture(t, "export default {}")
	connectFixture(t, m)
	c.routes["other"] = [2]string{"www.example.com/*", "someone-else"}
	c.tags["someone-else"] = []string{"theirs"}
	ctx := context.Background()
	if err := m.EnsureSignalBridge(ctx, "example.com", "netbird.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := m.RemoveSignalBridge(ctx); err != nil {
		t.Fatal(err)
	}
	if len(c.routes) != 1 || c.routes["other"][1] != "someone-else" || len(c.scripts) != 0 || len(c.tags) != 1 {
		t.Fatal(c.routes, c.scripts, c.tags)
	}
	// Removing again, or with nothing deployed, is a no-op.
	if err := m.RemoveSignalBridge(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSignalBridgeMovesWithANewHostname(t *testing.T) {
	m, c := zoneFixture(t, "export default {}")
	connectFixture(t, m)
	ctx := context.Background()
	m.EnsureSignalBridge(ctx, "example.com", "netbird.example.com")
	if err := m.EnsureSignalBridge(ctx, "example.com", "nb.example.com"); err != nil {
		t.Fatal(err)
	}
	name, route := c.only()
	if len(c.routes) != 2 || route[0] != "nb.example.com/signalexchange.SignalExchange/ConnectStream*" || len(c.scripts) != 1 || route[1] != name {
		t.Fatal(c.routes, c.scripts)
	}
}

func TestSignalBridgeRefusalsNameTheMissingPiece(t *testing.T) {
	ctx := context.Background()
	m, _ := zoneFixture(t, "export default {}")
	if err := m.EnsureSignalBridge(ctx, "example.com", "netbird.example.com"); connector.ErrorCode(err) != "workers_not_connected" {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		set  func(*zoneCloud)
		code string
	}{
		{"zone not visible to the token", func(c *zoneCloud) { c.zoneHidden = true }, "workers_routes_permission"},
		{"no Workers Routes permission", func(c *zoneCloud) { c.routeDenied = true }, "workers_routes_permission"},
		{"zone in another account", func(c *zoneCloud) { c.account = "abcdef0123456789abcdef0123456789" }, "workers_zone_account"},
		{"route held by another Worker", func(c *zoneCloud) { c.routes["x"] = [2]string{fixturePattern, "someone-else"} }, "workers_route_conflict"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, c := zoneFixture(t, "export default {}")
			connectFixture(t, m)
			tc.set(c)
			err := m.EnsureSignalBridge(ctx, "example.com", "netbird.example.com")
			if connector.ErrorCode(err) != tc.code || strings.Contains(err.Error(), fixtureToken) {
				t.Fatal(err)
			}
			if c.routes["x"][1] == "someone-else" && len(c.routes) != 1 {
				t.Fatal("another Worker's route was touched")
			}
			if tc.code == "workers_routes_permission" && c.uploads != 0 {
				t.Fatal("script uploaded although no route could be added")
			}
		})
	}
}

func TestSignalBridgeNameHeldByAnotherOwnerIsNotOverwritten(t *testing.T) {
	m, c := zoneFixture(t, "export default {}")
	connectFixture(t, m)
	c.tags[bridgeScript("netbird.example.com")] = []string{"synopackageland-connect:someone"}
	if err := m.EnsureSignalBridge(context.Background(), "example.com", "netbird.example.com"); connector.ErrorCode(err) != "workers_name_conflict" || c.uploads != 0 {
		t.Fatal(err, c.uploads)
	}
}

func TestDisconnectAlsoRemovesTheSignalBridge(t *testing.T) {
	m, c := zoneFixture(t, "export default {}")
	connectFixture(t, m)
	ctx := context.Background()
	if err := m.EnsureSignalBridge(ctx, "example.com", "netbird.example.com"); err != nil {
		t.Fatal(err)
	}
	if err := m.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if len(c.routes) != 0 || len(c.scripts) != 0 {
		t.Fatal(c.routes, c.scripts)
	}
}

func TestTokenForTheNetBirdServerNeedsNoWorkersDevName(t *testing.T) {
	m, c := zoneFixture(t, "export default {}")
	c.subdomain = ""
	ctx := context.Background()
	out, err := m.Connect(ctx, apps.WorkersConnectRequest{Token: fixtureToken, AccountID: fixtureAccount, NoDSM: true})
	if err != nil || out.Connected || !out.Token || c.claimed != 0 {
		t.Fatalf("%+v %v claimed=%d", out, err, c.claimed)
	}
	if err = m.EnsureSignalBridge(ctx, "example.com", "netbird.example.com"); err != nil || len(c.routes) != 2 {
		t.Fatal(connector.ErrorCode(err), c.routes)
	}
	// Turning the Free link on later reuses the saved account and owner and
	// only asks for the name.
	out, err = m.Connect(ctx, apps.WorkersConnectRequest{Token: fixtureToken, Subdomain: "chosen"})
	if err != nil || !out.Connected || out.Subdomain != "chosen" || c.claimed != 1 {
		t.Fatalf("%+v %v", out, err)
	}
	if err = m.EnsureSignalBridge(ctx, "example.com", "netbird.example.com"); err != nil || c.uploads != 1 {
		t.Fatal("bridge redeployed after the Free link was turned on", err, c.uploads)
	}
}

func TestBridgeCheckNamesTheMissingPermissionWithoutDeploying(t *testing.T) {
	ctx := context.Background()
	m, c := zoneFixture(t, "export default {}")
	if err := m.CheckSignalBridge(ctx, "example.com"); connector.ErrorCode(err) != "workers_not_connected" {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		set  func(*zoneCloud)
		code string
	}{
		{"zone not visible to the token", func(c *zoneCloud) { c.zoneHidden = true }, "workers_routes_permission"},
		{"no Workers Routes permission", func(c *zoneCloud) { c.routeDenied = true }, "workers_routes_permission"},
		{"zone in another account", func(c *zoneCloud) { c.account = "abcdef0123456789abcdef0123456789" }, "workers_zone_account"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, c := zoneFixture(t, "export default {}")
			connectFixture(t, m)
			tc.set(c)
			err := m.CheckSignalBridge(ctx, "example.com")
			if connector.ErrorCode(err) != tc.code || strings.Contains(err.Error(), fixtureToken) {
				t.Fatal(err)
			}
			if c.uploads != 0 || len(c.routes) != 0 {
				t.Fatal("a check must not deploy", c.uploads, c.routes)
			}
		})
	}
	connectFixture(t, m)
	if err := m.CheckSignalBridge(ctx, "example.com"); err != nil || c.uploads != 0 || len(c.routes) != 0 {
		t.Fatal(err, c.uploads, c.routes)
	}
}

func TestBridgeCheckIsRememberedUntilTheTokenOrTimeChanges(t *testing.T) {
	ctx := context.Background()
	m, c := zoneFixture(t, "export default {}")
	now := time.Unix(1_800_000_000, 0)
	m.clock = func() time.Time { return now }
	connectFixture(t, m)
	c.routeDenied = true
	if err := m.CheckSignalBridge(ctx, "example.com"); connector.ErrorCode(err) != "workers_routes_permission" || c.routeReads != 1 {
		t.Fatal(err, c.routeReads)
	}
	// The NetBird page asks on every status poll; Cloudflare is asked again
	// only after a short while.
	if err := m.CheckSignalBridge(ctx, "example.com"); connector.ErrorCode(err) != "workers_routes_permission" || c.routeReads != 1 {
		t.Fatal(err, c.routeReads)
	}
	// The permission added to the same token in Cloudflare's dashboard.
	c.routeDenied = false
	now = now.Add(checkRefusal + time.Second)
	if err := m.CheckSignalBridge(ctx, "example.com"); err != nil || c.routeReads != 2 {
		t.Fatal(err, c.routeReads)
	}
	if err := m.CheckSignalBridge(ctx, "example.com"); err != nil || c.routeReads != 2 {
		t.Fatal("a pass is remembered", err, c.routeReads)
	}
	now = now.Add(checkPass + time.Second)
	m.CheckSignalBridge(ctx, "example.com")
	if c.routeReads != 3 {
		t.Fatal("a pass expires", c.routeReads)
	}
	// A replaced token is checked at once, though a pass is remembered.
	c.routeDenied = true
	if _, err := m.Connect(ctx, apps.WorkersConnectRequest{Token: fixtureToken + "-new", AccountID: fixtureAccount}); err != nil {
		t.Fatal(err)
	}
	if err := m.CheckSignalBridge(ctx, "example.com"); connector.ErrorCode(err) != "workers_routes_permission" || c.routeReads != 4 {
		t.Fatal(err, c.routeReads)
	}
	// Cloudflare failing is not a refusal and is not remembered.
	c.routeDenied, c.broken = false, true
	now = now.Add(checkRefusal + time.Second)
	if err := m.CheckSignalBridge(ctx, "example.com"); err == nil || Refusal(err) {
		t.Fatal(err)
	}
	c.broken = false
	if err := m.CheckSignalBridge(ctx, "example.com"); err != nil || c.routeReads != 6 {
		t.Fatal(err, c.routeReads)
	}
}

func TestReplacingTheTokenKeepsOneCredential(t *testing.T) {
	ctx := context.Background()
	m, _ := zoneFixture(t, "export default {}")
	connectFixture(t, m)
	if err := m.EnsureSignalBridge(ctx, "example.com", "netbird.example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Connect(ctx, apps.WorkersConnectRequest{Token: fixtureToken + "-new", NoDSM: true}); err != nil {
		t.Fatal(err)
	}
	v, err := m.Store.load()
	if err != nil || v.Token != fixtureToken+"-new" || v.SignalBridge == nil || v.AccountID != fixtureAccount {
		t.Fatalf("%v %+v", err, v.SignalBridge)
	}
}

func TestTokenThatLostRouteReadPermissionKeepsInventoryUntilSafeRetry(t *testing.T) {
	ctx := context.Background()
	m, c := zoneFixture(t, "export default {}")
	connectFixture(t, m)
	if err := m.EnsureSignalBridge(ctx, "example.com", "netbird.example.com"); err != nil {
		t.Fatal(err)
	}
	c.routeDenied = true
	if err := m.Disconnect(ctx); connector.ErrorCode(err) != "workers_routes_permission" {
		t.Fatal("need fresh route evidence before deletion", err)
	}
	if len(c.scripts) == 0 || len(c.routes) == 0 {
		t.Fatal("removed without observing references")
	}
	c.routeDenied = false
	if err := m.Disconnect(ctx); err != nil {
		t.Fatal("retry after permission repair", err)
	}
	if len(c.scripts) != 0 || len(c.routes) != 0 {
		t.Fatal(c.scripts, c.routes)
	}
}

func TestSignalBridgeAddsExactLandingRouteAndRemovesBothOnDisable(t *testing.T) {
	m, c := zoneFixture(t, "export default {}")
	connectFixture(t, m)
	ctx := context.Background()
	if e := m.EnsureSignalBridge(ctx, "example.com", "netbird.example.com"); e != nil {
		t.Fatal(e)
	}
	var landing, stream bool
	for _, r := range c.routes {
		landing = landing || r[0] == "netbird.example.com/"
		stream = stream || r[0] == fixturePattern
	}
	if !landing || !stream || len(c.routes) != 2 {
		t.Fatalf("missing exact landing route: %v", c.routes)
	}
	if e := m.RemoveSignalBridge(ctx); e != nil {
		t.Fatal(e)
	}
	if len(c.routes) != 0 || len(c.scripts) != 0 {
		t.Fatalf("left bridge resources: %v %v", c.routes, c.scripts)
	}
}

func TestLandingRouteConflictLeavesForeignWorkerUntouched(t *testing.T) {
	m, c := zoneFixture(t, "export default {}")
	connectFixture(t, m)
	c.routes["foreign"] = [2]string{"netbird.example.com/", "other-worker"}
	e := m.EnsureSignalBridge(context.Background(), "example.com", "netbird.example.com")
	if connector.ErrorCode(e) != "workers_route_conflict" || len(c.routes) != 1 || c.uploads != 0 {
		t.Fatalf("foreign route changed: %v %v uploads=%d", e, c.routes, c.uploads)
	}
}

// Free link shutdown owns only relay scripts, even when no relay is live.
func TestDisconnectKeepsNetBirdBridgeAndCanBeRepeatedAfterRestart(t *testing.T) {
	m, c := zoneFixture(t, "export default {}")
	m.Store.Credentials = &accounttoken.Store{Path: filepath.Join(t.TempDir(), "account", "credential.json")}
	connectFixture(t, m)
	ctx := context.Background()
	if err := m.EnsureSignalBridge(ctx, "example.com", "netbird.example.com"); err != nil {
		t.Fatal(err)
	}
	v, _ := m.Store.load()
	v.Records[""] = Record{Name: "relay-example", Secret: strings.Repeat("a", 64), Deployed: true}
	if err := m.Store.save(v); err != nil {
		t.Fatal(err)
	}
	c.tags["relay-example"] = []string{"synopackageland-connect:" + v.Owner}
	c.scripts["relay-example"] = "relay"
	m.KeepSignalBridge = func() bool { return true }
	if st, _ := m.Status(ctx); st.RelayState != "reconnecting" {
		t.Fatal(st)
	}
	c.relayDeleteDenied = true
	if err := m.Disconnect(ctx); connector.ErrorCode(err) != "workers_permission_denied" {
		t.Fatal("cleanup error hidden", err)
	}
	if st, _ := m.Status(ctx); st.Code != "workers_cleanup_required" {
		t.Fatal(st)
	}
	if len(c.routes) != 2 || len(c.scripts) != 2 {
		t.Fatal("failed cleanup touched bridge")
	}
	c.relayDeleteDenied = false
	if err := m.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if len(c.routes) != 2 || len(c.scripts) != 1 || c.scripts[v.SignalBridge.Script] == "" {
		t.Fatal("bridge changed", c.routes, c.scripts)
	}
	// A fresh owner must still see the Free link off and the bridge token usable.
	restarted := &Manager{Store: m.Store, API: m.API, KeepSignalBridge: func() bool { return true }}
	defer restarted.Close()
	if st, err := restarted.Status(ctx); err != nil || st.Connected || !st.Token || st.Code != "" || st.RelayState != "disconnected" {
		t.Fatalf("status: %+v %v", st, err)
	}
	if err := restarted.Ensure(ctx, []string{""}); connector.ErrorCode(err) != "workers_not_connected" {
		t.Fatal("off link published again", err)
	}
	if err := restarted.EnsureSignalBridge(ctx, "example.com", "netbird.example.com"); err != nil {
		t.Fatal("bridge token lost", err)
	}
	if err := restarted.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if len(c.routes) != 2 || len(c.scripts) != 1 {
		t.Fatal("repeat deleted bridge")
	}
	if _, err := restarted.Connect(ctx, apps.WorkersConnectRequest{Token: fixtureToken, AccountID: fixtureAccount}); err != nil {
		t.Fatal(err)
	}
	if st, _ := restarted.Status(ctx); !st.Connected {
		t.Fatal("explicit connect did not enable link", st)
	}
	restarted.KeepSignalBridge = func() bool { return false }
	if err := restarted.Disconnect(ctx); err != nil || len(c.routes) != 0 || len(c.scripts) != 0 {
		t.Fatal(err, c.routes, c.scripts)
	}
}

func TestSignalBridgeObservationNeverRepairsMissingResources(t *testing.T) {
	m, c := zoneFixture(t, "export default {}")
	connectFixture(t, m)
	ctx := context.Background()
	if e := m.ObserveSignalBridge(ctx, "example.com", "netbird.example.com"); connector.ErrorCode(e) != "netbird_server_bridge_missing" {
		t.Fatal(e)
	}
	if c.uploads != 0 || len(c.routes) != 0 {
		t.Fatal("observation changed Cloudflare")
	}
	if e := m.EnsureSignalBridge(ctx, "example.com", "netbird.example.com"); e != nil {
		t.Fatal(e)
	}
	if e := m.ObserveSignalBridge(ctx, "example.com", "netbird.example.com"); e != nil {
		t.Fatal(e)
	}
	delete(c.routes, "route1")
	if e := m.ObserveSignalBridge(ctx, "example.com", "netbird.example.com"); connector.ErrorCode(e) != "netbird_server_bridge_missing" {
		t.Fatal(e)
	}
	if len(c.routes) != 1 || c.uploads != 1 {
		t.Fatal("read restored a missing route")
	}
}

func TestSignalTokenWithAnExistingAccountNameDoesNotEnableFreeLink(t *testing.T) {
	m, _ := zoneFixture(t, "export default {}")
	ctx := context.Background()
	out, e := m.Connect(ctx, apps.WorkersConnectRequest{Token: fixtureToken, AccountID: fixtureAccount, NoDSM: true})
	if e != nil || out.Connected || !out.Token || out.Code != "" {
		t.Fatalf("token activated a Free link: %+v %v", out, e)
	}
	if e = m.EnsureSignalBridge(ctx, "example.com", "netbird.example.com"); e != nil {
		t.Fatal(e)
	}
	if st, e := m.Status(ctx); e != nil || st.Connected || !st.Token {
		t.Fatal("bridge activated Free link", st, e)
	}
}

func TestDeletedSignalBridgeScriptAndRoutesAreRecreated(t *testing.T) {
	m, c := zoneFixture(t, "export default {}")
	connectFixture(t, m)
	ctx := context.Background()
	if err := m.EnsureSignalBridge(ctx, "example.com", "netbird.example.com"); err != nil {
		t.Fatal(err)
	}
	name, _ := c.only()
	c.mu.Lock()
	delete(c.scripts, name)
	delete(c.tags, name)
	clear(c.routes)
	c.mu.Unlock()
	if err := m.EnsureSignalBridge(ctx, "example.com", "netbird.example.com"); err != nil {
		t.Fatal(err)
	}
	if c.uploads != 2 || c.scripts[name] != "export default {}" || len(c.routes) != 2 {
		t.Fatal("deleted bridge did not recover")
	}
}
