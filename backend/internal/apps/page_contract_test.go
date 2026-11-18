package apps

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
)

// The App URLs page (frontend/src/pages/AppUrlsPage.vue) builds every request
// from the JSON it was shown. These requests are written the way the page
// writes them and decoded as strictly as the browser handlers decode them.
func pageJSON(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err = json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func pageRequest[T any](t *testing.T, params map[string]any) T {
	t.Helper()
	raw, _ := json.Marshal(params)
	var into T
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&into); err != nil {
		t.Fatalf("page request refused: %v: %s", err, raw)
	}
	return into
}

// approval mirrors approve() in AppUrlsPage.vue: port, pin, token and dsm as inspected.
func pageApproval(inspection map[string]any) map[string]any {
	out := map[string]any{"port": inspection["port"], "pin": inspection["pin"]}
	for _, key := range []string{"token", "dsm"} {
		if v, ok := inspection[key]; ok {
			out[key] = v
		}
	}
	return out
}

func pageRow(t *testing.T, s *Service, pkg string) map[string]any {
	t.Helper()
	list, err := s.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range pageJSON(t, list)["apps"].([]any) {
		if r := row.(map[string]any); r["package"] == pkg {
			return r
		}
	}
	t.Fatalf("%s not listed", pkg)
	return nil
}

func TestAppURLsPageRequestsRoundTrip(t *testing.T) {
	s := fixtureService(t)
	ctx := context.Background()
	const pkg = "ExampleApp"
	inspect := func(scope string) map[string]any {
		in := pageRequest[InspectRequest](t, map[string]any{"package": pkg, "scope": scope})
		out, err := s.Inspect(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		view := pageJSON(t, out)
		if view["ready"] != true || view["pin"] == nil || view["token"] == nil {
			t.Fatalf("inspection gives the page nothing to approve: %v", view)
		}
		return view
	}

	row := pageRow(t, s, pkg)
	for _, key := range []string{"package", "name", "port", "protection", "publicCapable", "needsUpdate"} {
		if _, ok := row[key]; !ok {
			t.Fatalf("list row lacks %q: %v", key, row)
		}
	}

	// Private switch on: apps.inspect, then apps.setprivate with what was shown.
	approval := pageApproval(inspect(Private))
	if _, err := s.SetPrivate(ctx, pageRequest[PrivateRequest](t, map[string]any{"package": pkg, "enabled": true, "approval": approval})); err != nil {
		t.Fatal(err)
	}
	private, _ := pageRow(t, s, pkg)["private"].(map[string]any)
	if private["state"] != Active || private["url"] == "" || private["reachable"] != true {
		t.Fatalf("active private scope reads as %v", private)
	}

	// A paused row shows the stored reason and "Turn on again" sends apps.reenable.
	if err := s.Lifecycle.Store.Update(ctx, func(state *State) error {
		a := state.Apps[pkg]
		a.Private.State, a.Private.Reason = Paused, "cert_mismatch"
		state.Apps[pkg] = a
		_, err := state.Revision()
		return err
	}); err != nil {
		t.Fatal(err)
	}
	private, _ = pageRow(t, s, pkg)["private"].(map[string]any)
	if private["state"] != Paused || private["reason"] != "cert_mismatch" {
		t.Fatalf("paused private scope reads as %v", private)
	}
	reenable := pageRequest[ReenableRequest](t, map[string]any{"package": pkg, "scope": Private, "approval": pageApproval(inspect(Private))})
	if _, err := s.Reenable(ctx, reenable); err != nil {
		t.Fatal(err)
	}

	// Public: the selection is created with one apps.setpublic.
	public := pageRequest[PublicRequest](t, map[string]any{"packages": []string{pkg}, "enabled": true, "approvals": map[string]any{pkg: pageApproval(inspect(Public))}})
	out, err := s.SetPublic(ctx, public)
	if err != nil {
		t.Fatal(err)
	}
	result := pageJSON(t, out)["results"].([]any)[0].(map[string]any)
	if result["package"] != pkg || result["ok"] != true {
		t.Fatalf("public result reads as %v", result)
	}

	// Turn off public, then Clear (the page sends only the package).
	if _, err = s.SetPublic(ctx, pageRequest[PublicRequest](t, map[string]any{"packages": []string{pkg}, "enabled": false})); err != nil {
		t.Fatal(err)
	}
	if err = s.Clear(ctx, pageRequest[ScopedChange](t, map[string]any{"package": pkg})); err != nil {
		t.Fatal(err)
	}
	row = pageRow(t, s, pkg)
	if row["private"] != nil || row["public"] != nil {
		t.Fatalf("cleared row still has scopes: %v", row)
	}
}
