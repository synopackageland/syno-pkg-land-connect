package workers

import (
	"context"
	"dsmapp/internal/accounttoken"
	"dsmapp/internal/connector"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLegacyTokenMigratesToOneStoreAndAccessOnlyDoesNotCreateWorkers(t *testing.T) {
	m, c := cloudFixture(t)
	connectFixture(t, m)
	secret := &accounttoken.Store{Path: filepath.Join(t.TempDir(), "private", "credential.json")}
	m.Store.Credentials = secret
	got, e := m.AccountCredential()
	if e != nil || got.Token != fixtureToken {
		t.Fatal(e)
	}
	raw, e := os.ReadFile(filepath.Join(m.Store.Dir, "state.json"))
	if e != nil || strings.Contains(string(raw), fixtureToken) {
		t.Fatal("legacy secret duplicated", e)
	}
	c.uploads = 0
	accessOnly := &Manager{Store: Store{Dir: filepath.Join(t.TempDir(), "workers"), Credentials: secret}, API: m.API}
	if e := accessOnly.SaveAccountToken(context.Background(), fixtureToken, nil); e != nil {
		t.Fatal(e)
	}
	if c.uploads != 0 {
		t.Fatal("Access token setup deployed a Worker")
	}
	if _, e := os.Stat(filepath.Join(accessOnly.Store.Dir, "state.json")); !os.IsNotExist(e) {
		t.Fatal("Access token setup wrote Worker state")
	}
	if e = m.SaveAccountToken(context.Background(), fixtureToken, func(context.Context, accounttoken.Credential) error {
		return connector.Coded("access_permission_denied", "refused")
	}); connector.ErrorCode(e) != "access_permission_denied" {
		t.Fatal(e)
	}
	after, e := secret.Load()
	if e != nil || after != got {
		t.Fatal("failed replacement changed credential")
	}
}

func TestDisconnectRetainsAccessCredentialWithoutReenablingFreeLink(t *testing.T) {
	m, _ := cloudFixture(t)
	m.Store.Credentials = &accounttoken.Store{Path: filepath.Join(t.TempDir(), "account", "credential.json")}
	connectFixture(t, m)
	m.KeepAccountToken = func() bool { return true }
	if err := m.Disconnect(context.Background()); err != nil {
		t.Fatal(err)
	}
	restarted := &Manager{Store: m.Store, API: m.API}
	if st, err := restarted.Status(context.Background()); err != nil || st.Connected || !st.Token || st.Code != "" {
		t.Fatalf("%+v %v", st, err)
	}
	if sub, err := restarted.Follow(context.Background()); err != nil || sub != "" {
		t.Fatal("off link followed subdomain", sub, err)
	}
}
