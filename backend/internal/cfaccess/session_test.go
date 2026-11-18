package cfaccess

import (
	"context"
	"testing"
)

func TestSessionDefaultAndChoices(t *testing.T) {
	m, f := managerFixture(t)
	if got := m.Status().SessionDuration; got != "720h" {
		t.Fatal(got)
	}
	if _, e := m.Apply(context.Background(), Request{Emails: []string{"reader@example.test"}}); e != nil {
		t.Fatal(e)
	}
	for _, a := range f.apps {
		if a.Session != "720h" {
			t.Fatal(a.Session)
		}
	}
	for _, choice := range []string{"15m", "30m", "1h", "6h", "12h", "24h", "168h", "720h", "730h"} {
		if !sessionChoice(choice) || !validSessionDuration(choice) {
			t.Fatal(choice)
		}
	}
	for _, invalid := range []string{"31d", "744h", "-1h", "garbage"} {
		if _, e := m.Submit(Request{SessionDuration: invalid, Revision: m.Status().Revision}); Code(e) != "access_invalid_request" {
			t.Fatal(invalid, e)
		}
	}
}

func TestSessionUIChangeUpdatesAllWithoutRecreateOrRevoke(t *testing.T) {
	m, f := managerFixture(t)
	m.cfg.Endpoints = func() (Inventory, error) {
		return Inventory{Account: "account-id", DSMHost: "web.example.test", Hosts: []string{"app.example.test", "web.example.test"}}, nil
	}
	ctx := context.Background()
	if _, e := m.Apply(ctx, Request{Emails: []string{"reader@example.test"}}); e != nil {
		t.Fatal(e)
	}
	ids := map[string]string{}
	for id, a := range f.apps {
		ids[id] = a.AUD
	}
	if _, e := m.Apply(ctx, Request{Revision: m.Status().Revision, Emails: []string{"reader@example.test"}, SessionDuration: "168h"}); e != nil {
		t.Fatal(e)
	}
	if f.creates != 2 || f.deletes != 0 || f.revokes != 0 || f.updates != 2 {
		t.Fatal(f)
	}
	for id, a := range f.apps {
		if a.Session != "168h" || a.AUD != ids[id] {
			t.Fatal(a)
		}
	}
	if m.Status().SessionDuration != "168h" {
		t.Fatal(m.Status())
	}
	// Completed intent must not become a permanent enforcement policy.
	for id, a := range f.apps {
		a.Session = "2h45m"
		f.apps[id] = a
	}
	if e := m.Sync(ctx); e != nil {
		t.Fatal(e)
	}
	if f.updates != 2 || f.revokes != 0 || m.Status().SessionDuration != "2h45m" {
		t.Fatal(f, m.Status())
	}
}

func TestSessionDashboardDisagreementUsesDSMAndKeepsOthers(t *testing.T) {
	m, f := managerFixture(t)
	m.cfg.Endpoints = func() (Inventory, error) {
		return Inventory{Account: "account-id", DSMHost: "web.example.test", Hosts: []string{"app.example.test", "web.example.test"}}, nil
	}
	ctx := context.Background()
	if _, e := m.Apply(ctx, Request{Emails: []string{"reader@example.test"}}); e != nil {
		t.Fatal(e)
	}
	for id, a := range f.apps {
		if a.Domain == "web.example.test" {
			a.Session = "48h"
		} else {
			a.Session = "6h"
		}
		f.apps[id] = a
	}
	if e := m.Sync(ctx); e != nil {
		t.Fatal(e)
	}
	if m.Status().SessionDuration != "48h" || f.updates != 0 || f.revokes != 0 {
		t.Fatal(m.Status(), f)
	}
	restored := New(m.cfg)
	if restored.Status().SessionDuration != "48h" {
		t.Fatal(restored.Status())
	}
	// An email change repairs the include rules, preserving both custom lengths.
	if _, e := restored.Apply(ctx, Request{Revision: restored.Status().Revision, Emails: []string{"editor@example.test"}}); e != nil {
		t.Fatal(e)
	}
	for _, a := range f.apps {
		want := "6h"
		if a.Domain == "web.example.test" {
			want = "48h"
		}
		if a.Session != want || a.Policies[0].Include[0]["email"].(map[string]any)["email"] != "editor@example.test" {
			t.Fatal(a)
		}
	}
	if f.updates != 2 || f.revokes != 2 {
		t.Fatal(f)
	}
}

func TestSessionReadFallbackDoesNotTurnOptionalFieldIntoPolicyDrift(t *testing.T) {
	for _, known := range []bool{false, true} {
		t.Run(map[bool]string{false: "default", true: "last-known"}[known], func(t *testing.T) {
			m, f := managerFixture(t)
			ctx := context.Background()
			if _, e := m.Apply(ctx, Request{Emails: []string{"reader@example.test"}}); e != nil {
				t.Fatal(e)
			}
			if known {
				for id, a := range f.apps {
					a.Session = "48h"
					f.apps[id] = a
				}
				if e := m.Sync(ctx); e != nil {
					t.Fatal(e)
				}
			} else {
				// A legacy ledger never stored session lengths.
				m.state.Session = ""
				for host, r := range m.state.Records {
					r.Session = ""
					m.state.Records[host] = r
				}
				if e := m.persist(); e != nil {
					t.Fatal(e)
				}
			}
			want := "720h"
			if known {
				want = "48h"
			}
			f.badSession = true
			if e := m.Sync(ctx); e != nil {
				t.Fatal("optional malformed duration blocked sync", e)
			}
			if m.Status().SessionDuration != want || f.updates != 0 {
				t.Fatal(m.Status(), f)
			}
			f.badSession = false
			f.readFailure = true
			// Mandatory ownership/policy evidence still fails closed; the UI retains its duration.
			if e := m.Sync(ctx); e == nil {
				t.Fatal("missing mandatory app evidence accepted")
			}
			if m.Status().SessionDuration != want {
				t.Fatal(m.Status())
			}
			f.readFailure = false
			for id, a := range f.apps {
				a.Session = "unparsable"
				f.apps[id] = a
			}
			if e := m.Sync(ctx); e != nil {
				t.Fatal(e)
			}
			if m.Status().SessionDuration != want || f.updates != 0 {
				t.Fatal(m.Status(), f)
			}
		})
	}
}

func TestSessionPendingChangeRecoversAcrossRestart(t *testing.T) {
	m, f := managerFixture(t)
	ctx := context.Background()
	if _, e := m.Apply(ctx, Request{Emails: []string{"reader@example.test"}}); e != nil {
		t.Fatal(e)
	}
	f.updateFailure = true
	if _, e := m.Apply(ctx, Request{Revision: m.Status().Revision, Emails: []string{"reader@example.test"}, SessionDuration: "24h"}); e == nil {
		t.Fatal("missing failure")
	}
	f.updateFailure = false
	restored := New(m.cfg)
	if e := restored.Sync(ctx); e != nil {
		t.Fatal(e)
	}
	for _, a := range f.apps {
		if a.Session != "24h" {
			t.Fatal(a)
		}
	}
	if f.creates != 1 || f.revokes != 0 {
		t.Fatal(f)
	}
}

func TestSessionCustomLengthSurvivesIncludeDriftRepair(t *testing.T) {
	m, f := managerFixture(t)
	ctx := context.Background()
	if _, e := m.Apply(ctx, Request{Emails: []string{"reader@example.test"}}); e != nil {
		t.Fatal(Code(e), e)
	}
	for id, a := range f.apps {
		a.Session = "2h45m"
		a.Policies[0].Include = []map[string]any{{"everyone": map[string]any{}}}
		f.apps[id] = a
	}
	if e := m.Sync(ctx); e != nil {
		t.Fatal(Code(e), e)
	}
	for _, a := range f.apps {
		if a.Session != "2h45m" || a.Policies[0].Include[0]["email"].(map[string]any)["email"] != "reader@example.test" {
			t.Fatal(a)
		}
	}
	if f.updates != 1 || f.revokes != 1 {
		t.Fatal(f)
	}
}

func TestSessionDefaultCanBeSavedWhileOffWithoutCloudRead(t *testing.T) {
	m, _ := managerFixture(t)
	m.cfg.Credential = func() (Credential, error) { t.Fatal("unexpected credential read"); return Credential{}, nil }
	st, e := m.Apply(context.Background(), Request{SessionDuration: "24h"})
	if e != nil || st.State != "off" || st.SessionDuration != "24h" || m.InUse() {
		t.Fatal(st, e)
	}
	restored := New(m.cfg)
	if restored.Status().SessionDuration != "24h" {
		t.Fatal(restored.Status())
	}
}
