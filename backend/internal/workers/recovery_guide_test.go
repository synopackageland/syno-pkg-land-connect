package workers

import (
	"context"
	"encoding/json"
	"testing"

	"dsmapp/internal/accounttoken"
	"dsmapp/internal/apps"
	"dsmapp/internal/connector"
	"path/filepath"
)

func TestForeignSignalWorkerConflictCarriesAnExactInlineGuide(t *testing.T) {
	m, c := zoneFixture(t, "bridge")
	connectFixture(t, m)
	name := bridgeScript("netbird.example.com")
	c.tags[name] = []string{"another-application"}
	c.scripts[name] = "foreign"
	e := m.EnsureSignalBridge(context.Background(), "example.com", "netbird.example.com")
	if connector.ErrorCode(e) != "workers_name_conflict" {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(connector.Conflict(e))
	var guide map[string]any
	_ = json.Unmarshal(raw, &guide)
	if guide["objectType"] != "worker" || guide["conflictName"] != name || guide["dashboardUrl"] == nil {
		t.Fatal("missing exact recovery guide", string(raw))
	}
}

func TestAbandoningAConflictingForeignWorkerNeverDeletesItsRoutes(t *testing.T) {
	m, c := zoneFixture(t, "bridge")
	connectFixture(t, m)
	name := bridgeScript("netbird.example.com")
	c.tags[name] = []string{"another-application"}
	c.scripts[name] = "foreign"
	c.routes["foreign-route"] = [2]string{fixturePattern, name}
	_ = m.EnsureSignalBridge(context.Background(), "example.com", "netbird.example.com")
	_ = m.RemoveSignalBridge(context.Background())
	if c.routes["foreign-route"] != [2]string{fixturePattern, name} || c.scripts[name] != "foreign" {
		t.Fatal("a failed setup made foreign objects eligible for removal")
	}
}

func TestAnotherWorkerNameLeavesTheForeignWorkerAndPersistsItsChoice(t *testing.T) {
	m, c := zoneFixture(t, "bridge")
	connectFixture(t, m)
	original := bridgeScript("netbird.example.com")
	c.tags[original] = []string{"another-application"}
	c.scripts[original] = "foreign"
	if e := m.EnsureSignalBridge(connector.WithAnotherName(context.Background()), "example.com", "netbird.example.com"); e != nil {
		t.Fatal(e)
	}
	saved, e := m.Store.load()
	if e != nil {
		t.Fatal(e)
	}
	if saved.SignalBridge.Script == original || c.scripts[original] != "foreign" {
		t.Fatal("foreign name was taken")
	}
	selected := saved.SignalBridge.Script
	if e := m.EnsureSignalBridge(context.Background(), "example.com", "netbird.example.com"); e != nil {
		t.Fatal(e)
	}
	saved, _ = m.Store.load()
	if saved.SignalBridge.Script != selected {
		t.Fatal("retry forgot the selected name")
	}
	if e := m.RemoveSignalBridge(context.Background()); e != nil {
		t.Fatal(e)
	}
	if c.scripts[original] != "foreign" {
		t.Fatal("removal touched foreign Worker")
	}
}

func TestRemovingOwnedWorkerDoesNotDeleteAnUnexpectedUserRoute(t *testing.T) {
	m, c := zoneFixture(t, "bridge")
	connectFixture(t, m)
	if e := m.EnsureSignalBridge(context.Background(), "example.com", "netbird.example.com"); e != nil {
		t.Fatal(e)
	}
	v, _ := m.Store.load()
	c.routes["user-route"] = [2]string{"other.example.com/*", v.SignalBridge.Script}
	e := m.RemoveSignalBridge(context.Background())
	if connector.ErrorCode(e) != "workers_route_conflict" {
		t.Fatal("unexpected reference must have recovery guide", e)
	}
	if _, ok := c.routes["user-route"]; !ok {
		t.Fatal("deleted a user-created route")
	}
	if _, ok := c.scripts[v.SignalBridge.Script]; !ok {
		t.Fatal("deleted script still used by another route")
	}
}

func TestAReplacedRouteIDIsNeverDeletedByPatternAlone(t *testing.T) {
	m, c := zoneFixture(t, "bridge")
	connectFixture(t, m)
	if e := m.EnsureSignalBridge(context.Background(), "example.com", "netbird.example.com"); e != nil {
		t.Fatal(e)
	}
	v, _ := m.Store.load()
	for id, r := range c.routes {
		if r[0] == fixturePattern {
			delete(c.routes, id)
			c.routes["replacement"] = r
			break
		}
	}
	e := m.RemoveSignalBridge(context.Background())
	if connector.ErrorCode(e) != "workers_route_conflict" {
		t.Fatal(e)
	}
	if _, ok := c.routes["replacement"]; !ok {
		t.Fatal("deleted unrecorded replacement")
	}
	if _, ok := c.scripts[v.SignalBridge.Script]; !ok {
		t.Fatal("deleted referenced script")
	}
}

func TestMissingSharedAccountCredentialOffersSignInWithoutLosingOwnedInventory(t *testing.T) {
	m, _ := zoneFixture(t, "bridge")
	m.Store.Credentials = &accounttoken.Store{Path: filepath.Join(t.TempDir(), "account", "credential.json")}
	connectFixture(t, m)
	if e := m.EnsureSignalBridge(context.Background(), "example.com", "netbird.example.com"); e != nil {
		t.Fatal(e)
	}
	before, _ := m.Store.load()
	if e := m.Store.Credentials.Remove(); e != nil {
		t.Fatal(e)
	}
	st, e := m.Status(context.Background())
	if e != nil || st.Token {
		t.Fatal("a missing credential is not corrupt object inventory", e, st.Token)
	}
	if e := m.CheckSignalBridge(context.Background(), "example.com"); connector.ErrorCode(e) != "workers_not_connected" {
		t.Fatal("missing sign-in must name the credential", e)
	}
	if _, e := m.Connect(context.Background(), apps.WorkersConnectRequest{Token: fixtureToken, NoDSM: true}); e != nil {
		t.Fatal("save token could not recover", e)
	}
	after, e := m.Store.load()
	if e != nil || after.Owner != before.Owner || after.SignalBridge == nil || after.SignalBridge.Script != before.SignalBridge.Script {
		t.Fatal("token recovery lost ownership", e)
	}
}

func TestAnUnrecordedRouteCannotBeAdoptedIntoAnExistingReceiptSet(t *testing.T) {
	m, c := zoneFixture(t, "bridge")
	connectFixture(t, m)
	if e := m.EnsureSignalBridge(context.Background(), "example.com", "netbird.example.com"); e != nil {
		t.Fatal(e)
	}
	v, _ := m.Store.load()
	delete(v.SignalBridge.RouteIDs, fixturePattern)
	if e := m.Store.save(v); e != nil {
		t.Fatal(e)
	}
	before := len(c.routes)
	e := m.EnsureSignalBridge(context.Background(), "example.com", "netbird.example.com")
	if connector.ErrorCode(e) != "workers_route_conflict" {
		t.Fatal("unrecorded route was silently adopted", e)
	}
	if len(c.routes) != before {
		t.Fatal("unknown route changed")
	}
}

func TestEmptyRouteReceiptsRemainNonLegacyAcrossRestart(t *testing.T) {
	m, _ := zoneFixture(t, "bridge")
	connectFixture(t, m)
	if e := m.EnsureSignalBridge(context.Background(), "example.com", "netbird.example.com"); e != nil {
		t.Fatal(e)
	}
	v, _ := m.Store.load()
	v.SignalBridge.RouteIDs = map[string]string{}
	if e := m.Store.save(v); e != nil {
		t.Fatal(e)
	}
	e := m.EnsureSignalBridge(context.Background(), "example.com", "netbird.example.com")
	if connector.ErrorCode(e) != "workers_route_conflict" {
		t.Fatal("empty new receipts became legacy after a state reload", e)
	}
}
