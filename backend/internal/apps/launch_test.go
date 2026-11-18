package apps

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func launchFixture(t *testing.T, dsmPublic string) State {
	t.Helper()
	s := EmptyState()
	if err := approve(&s, Public); err != nil {
		t.Fatal(err)
	}
	if err := approve(&s, Private); err != nil {
		t.Fatal(err)
	}
	app := s.Apps["ExampleApp"]
	app.Public.State = Paused
	app.Public.Reason = "paused by an administrator"
	provider := DSMPublicProvider(dsmPublic)
	if provider == Workers {
		// One installation has one public DSM mode. Its private scopes remain
		// independent, but a domain App cannot coexist with workers Apps.
		app.Public = nil
		rev, _ := s.Revision()
		workers := fixtureScope(Public)
		workers.Provider, workers.Socket, workers.Authority, workers.Origin = Workers, "w1", "spl-other-3c4d.example.workers.dev", "https://spl-other-3c4d.example.workers.dev"
		s.Apps["OtherApp"] = AppState{Rev: rev, Public: workers}
	}
	s.Apps["ExampleApp"] = app
	s.DSM = &DSMState{Port: 5001, Pin: Pin{Mode: "S", SPKI: strings.Repeat("c", 64)},
		Public:  &Endpoint{Provider: provider, Socket: "d1", Authority: dsmPublic},
		Private: &Endpoint{Socket: "d2", Authority: "nas.example.ts.net:5001"},
		Origin:  "https://" + dsmPublic}
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	return s
}

// strictLaunch decodes the way the SDK's launcher does: unknown fields make
// the whole map absent.
func strictLaunch(t *testing.T, raw []byte) LaunchMap {
	t.Helper()
	var m LaunchMap
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&m); err != nil {
		t.Fatalf("launch map %s: %v", raw, err)
	}
	return m
}

func TestLaunchMapRoutesEachDSMEndpointToItsProvider(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spl", "external.json")
	if err := WriteProjection(path, launchFixture(t, "dsm.example.com")); err != nil {
		t.Fatal(err)
	}
	launch := filepath.Join(filepath.Dir(path), LaunchFile)
	raw, err := os.ReadFile(launch)
	if err != nil {
		t.Fatal(err)
	}
	want := LaunchMap{Schema: 1, Routes: []LaunchRoute{
		{Provider: "cloudflare", DSM: "https://dsm.example.com", Apps: map[string]LaunchApp{
			"ExampleApp": {Origin: "https://app.example.com", Port: 28443, State: Paused},
		}},
		{Provider: "tailscale", DSM: "https://nas.example.ts.net:5001", Apps: map[string]LaunchApp{
			"ExampleApp": {Origin: "https://nas.example.ts.net:28443", Port: 28443, State: Active},
		}},
	}}
	if got := strictLaunch(t, raw); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v\nwant %+v", got, want)
	}
	for _, secret := range []string{"spki", "SPKI", "pin", "socket", "summary", "reason", "paused by", "cleanup", "rev", strings.Repeat("b", 64), strings.Repeat("c", 64)} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("launch map leaked %s: %s", secret, raw)
		}
	}
	if st, _ := os.Stat(launch); st.Mode().Perm() != 0644 {
		t.Fatal(st.Mode())
	}
	// The sign-in projection still holds no private authority.
	projection, _ := os.ReadFile(path)
	if strings.Contains(string(projection), "ts.net") {
		t.Fatalf("projection leaked a private authority: %s", projection)
	}
}

func TestLaunchMapWorkersDSMListsWorkersApps(t *testing.T) {
	got := Launch(launchFixture(t, "spl-dsm-1a2b.example.workers.dev"))
	if len(got.Routes) != 2 || got.Routes[0].Provider != Workers || got.Routes[0].DSM != "https://spl-dsm-1a2b.example.workers.dev" ||
		!reflect.DeepEqual(got.Routes[0].Apps, map[string]LaunchApp{"OtherApp": {Origin: "https://spl-other-3c4d.example.workers.dev", Port: 28443, State: Active}}) {
		t.Fatalf("%+v", got)
	}
}

func TestLaunchMapWithoutDSMOrValidStateHasNoRoutes(t *testing.T) {
	s := launchFixture(t, "dsm.example.com")
	s.DSM = nil
	broken := launchFixture(t, "dsm.example.com")
	broken.NextRev = 0
	for name, state := range map[string]State{"no DSM": s, "invalid": broken, "empty": EmptyState()} {
		raw, _ := json.Marshal(Launch(state))
		if string(raw) != `{"schema":1,"routes":[]}` {
			t.Fatal(name, string(raw))
		}
	}
	// A DSM endpoint with no App on that provider is still listed, so the
	// launcher can say the App is not enabled there.
	s = EmptyState()
	s.DSM = &DSMState{Port: 5001, Pin: Pin{Mode: "S", SPKI: strings.Repeat("c", 64)}, Private: &Endpoint{Socket: "d2", Authority: "nas.example.ts.net:5001"}}
	raw, _ := json.Marshal(Launch(s))
	if string(raw) != `{"schema":1,"routes":[{"provider":"tailscale","dsm":"https://nas.example.ts.net:5001","apps":{}}]}` {
		t.Fatal(string(raw))
	}
}

func TestLaunchMapFollowsProjectionDirectoryChecks(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "spl")
	if err := os.MkdirAll(dir, 0775); err != nil {
		t.Fatal(err)
	}
	os.Chmod(dir, 0775)
	if err := WriteProjection(filepath.Join(dir, "external.json"), launchFixture(t, "dsm.example.com")); err != ErrCorrupt {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, LaunchFile)); !os.IsNotExist(err) {
		t.Fatal("wrote the launch map into an unsafe directory", err)
	}
}
