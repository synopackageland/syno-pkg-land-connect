package cfaccess

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"testing"
	"time"
)

// Explicit opt-in against the cf CLI's own authenticated account. The caller
// supplies only a test-prefixed, unpublished hostname from that account's zone.
// No DNS, tunnel, token, subscription or billing objects are created.
func TestLiveAccessCRUD(t *testing.T) {
	if os.Getenv("CF_ACCESS_LIVE") != "1" {
		t.Skip("explicit Cloudflare fixture only")
	}
	c := Credential{Account: os.Getenv("CF_ACCESS_ACCOUNT"), Token: os.Getenv("CF_ACCESS_TOKEN")}
	host := os.Getenv("CF_ACCESS_HOST")
	if c.Account == "" || c.Token == "" || len(host) < 5 || host[:5] != "test-" {
		t.Fatal("test fixture inputs required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	a := API{}
	snapshot := func() map[string][]string {
		t.Helper()
		out := map[string][]string{}
		for _, kind := range []string{"apps", "policies", "identity_providers"} {
			rows, e := list[json.RawMessage](ctx, a, c, accountPath(c)+"/"+kind)
			if e != nil {
				t.Fatalf("inventory %s: %s", kind, Code(e))
			}
			for _, raw := range rows {
				var v any
				if json.Unmarshal(raw, &v) != nil {
					t.Fatal("inventory JSON")
				}
				b, _ := json.Marshal(v)
				out[kind] = append(out[kind], string(b))
			}
			slices.Sort(out[kind])
		}
		var org json.RawMessage
		if _, e := a.call(ctx, c, "GET", accountPath(c)+"/organizations", nil, &org); e != nil {
			t.Fatal("organization: " + Code(e))
		}
		var v any
		json.Unmarshal(org, &v)
		raw, _ := json.Marshal(v)
		out["organization"] = []string{string(raw)}
		return out
	}
	before := snapshot()
	idps, e := a.IDPs(ctx, c)
	if e != nil {
		t.Fatal(Code(e))
	}
	otp := ""
	ownedOTP := false
	name := "Connect test-" + fmtInt(int(time.Now().UnixNano()))
	for _, p := range idps {
		if p.Type == "onetimepin" {
			otp = p.ID
			break
		}
	}
	appID := ""
	defer func() {
		cleanup, done := context.WithTimeout(context.Background(), 45*time.Second)
		defer done()
		if appID != "" {
			app, e := a.GetApp(cleanup, c, appID)
			if e != nil || app.Name != name || app.Domain != host {
				t.Error("test app ownership verification failed")
				return
			}
			if e = a.DeleteApp(cleanup, c, appID); e != nil {
				t.Error("test app cleanup: " + Code(e))
				return
			}
		}
		if ownedOTP {
			if e := a.DeleteOTP(cleanup, c, otp); e != nil {
				t.Error("test OTP cleanup: " + Code(e))
				return
			}
		}
		if !reflect.DeepEqual(before, snapshot()) {
			t.Error("Cloudflare inventory content differs after cleanup")
		} else {
			t.Log("Cloudflare inventory content unchanged after cleanup")
		}
	}()
	if otp == "" {
		p, e := a.CreateOTP(ctx, c, name+" email PIN")
		if e != nil {
			t.Fatal("OTP create: " + Code(e))
		}
		otp = p.ID
		ownedOTP = true
	}
	want := desiredApp(name, host, otp, []string{"reader@example.test"}, "")
	made, e := a.SaveApp(ctx, c, want)
	if e != nil {
		t.Fatal("create: " + Code(e))
	}
	appID = made.ID
	got, e := a.GetApp(ctx, c, appID)
	if e != nil {
		t.Fatal("readback: " + Code(e))
	}
	if canonical(got) != canonical(want) || got.AUD == "" {
		var gm, wm map[string]any
		json.Unmarshal([]byte(canonical(got)), &gm)
		json.Unmarshal([]byte(canonical(want)), &wm)
		keys := []string{}
		for k, v := range gm {
			if !reflect.DeepEqual(v, wm[k]) {
				keys = append(keys, k)
			}
		}
		slices.Sort(keys)
		for _, d := range got.Destinations {
			dk := []string{}
			for k := range d {
				dk = append(dk, k)
			}
			slices.Sort(dk)
			t.Logf("destination keys=%v public=%t uriMatches=%t hostnameMatches=%t", dk, d["type"] == "public", d["uri"] == host, d["hostname"] == host)
		}
		t.Fatalf("created app projection differs in fields %v", keys)
	}
	want = desiredApp(name, host, otp, []string{"editor@example.test"}, got.Policies[0].ID)
	want.ID = appID
	if _, e = a.SaveApp(ctx, c, want); e != nil {
		t.Fatal("update: " + Code(e))
	}
	got, e = a.GetApp(ctx, c, appID)
	if e != nil || canonical(got) != canonical(want) {
		t.Fatal("updated app readback differs")
	}
	if e = a.Revoke(ctx, c, appID); e != nil {
		t.Fatal("revoke: " + Code(e))
	}
	t.Log("self-hosted app-local CRUD and revoke accepted; no browser session or edge claim")
}
