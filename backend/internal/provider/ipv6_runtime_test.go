package provider

import (
	"context"
	"dsmapp/internal/connector"
	"dsmapp/internal/ipv6"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type ipv6SourceFunc func(context.Context) ([]ipv6.Address, error)

func (f ipv6SourceFunc) Addresses(ctx context.Context) ([]ipv6.Address, error) { return f(ctx) }
func runtimeSource() ipv6.Source {
	return ipv6SourceFunc(func(context.Context) ([]ipv6.Address, error) {
		return []ipv6.Address{{IP: netip.MustParseAddr("2606:4700:1234::1"), Interface: "eth0", PrefixBits: 64}}, nil
	})
}
func runtimeSettings() Settings {
	return Settings{ConfirmExposure: true, Mappings: []PortMapping{{LocalPort: 7443, PublicPort: 7443, Protocol: "https"}}}
}

func TestIPv6RuntimeImplementsContractAndNeverRequiresCredentialsOrBinaries(t *testing.T) {
	var r Runtime = NewIPv6Runtime(t.TempDir(), runtimeSource())
	if r.ID() != "ipv6" || len(r.Binaries()) != 0 || len(r.Schema()) != 2 {
		t.Fatal("metadata")
	}
	if len(r.Endpoints()) != 0 || r.Status().State != "disconnected" {
		t.Fatal("default exposure")
	}
	s, err := r.Start(context.Background(), runtimeSettings())
	if err != nil || s.State != "connected" || s.ExternalVerified || s.ShareURL != "" {
		t.Fatal(s, err)
	}
	e := r.Endpoints()
	if len(e) != 1 || e[0].URL != "https://[2606:4700:1234::1]:7443/" || e[0].LocalPort != 7443 || !e[0].Public {
		t.Fatal(e)
	}
}

func TestIPv6RuntimeRefusesMissingConfirmationCredentialsAndNAT(t *testing.T) {
	dir := t.TempDir()
	r := NewIPv6Runtime(dir, runtimeSource())
	for _, change := range []func(*Settings){func(s *Settings) { s.ConfirmExposure = false }, func(s *Settings) { s.Token = "synthetic" }, func(s *Settings) { s.Hostname = "fixture.invalid" }, func(s *Settings) { s.Mappings = nil }, func(s *Settings) { s.Mappings[0].PublicPort = 443 }, func(s *Settings) { s.Mappings[0].Protocol = "udp" }, func(s *Settings) { s.Mappings[0].LocalPort = 0 }} {
		s := runtimeSettings()
		change(&s)
		if _, err := r.Start(context.Background(), s); err == nil {
			t.Fatal("invalid start accepted", s)
		}
		files, _ := os.ReadDir(dir)
		if len(files) != 0 {
			t.Fatal("invalid start changed saved state")
		}
	}
}

func TestIPv6RuntimeStopPreservesIntentAndDisableRevokesRestore(t *testing.T) {
	dir := t.TempDir()
	r := NewIPv6Runtime(dir, runtimeSource())
	if _, err := r.Start(context.Background(), runtimeSettings()); err != nil {
		t.Fatal(err)
	}
	if err := r.Stop(); err != nil {
		t.Fatal(err)
	}
	if r.Status().State != "disconnected" || len(r.Endpoints()) != 0 {
		t.Fatal("stop")
	}
	fresh := NewIPv6Runtime(dir, runtimeSource())
	if err := fresh.Restore(context.Background()); err != nil || fresh.Status().State != "connected" {
		t.Fatal("restore", err)
	}
	if err := fresh.Disable(); err != nil {
		t.Fatal(err)
	}
	final := NewIPv6Runtime(dir, runtimeSource())
	if err := final.Restore(context.Background()); err != nil || final.Status().State != "disconnected" || len(final.Endpoints()) != 0 {
		t.Fatal("disabled intent restored", err)
	}
	raw, err := connector.ReadPrivate(filepath.Join(dir, "ipv6.json"))
	if err != nil || !strings.Contains(string(raw), `"enabled":false`) {
		t.Fatal("no private durable tombstone", err)
	}
}

// A process that starts with IPv6 disabled never restores it; disabling it
// there must keep the services an earlier process saved.
func TestIPv6RuntimeDisableKeepsServicesSavedByAnEarlierProcess(t *testing.T) {
	dir := t.TempDir()
	first := NewIPv6Runtime(dir, runtimeSource())
	if _, err := first.Start(context.Background(), runtimeSettings()); err != nil {
		t.Fatal(err)
	}
	if err := first.Disable(); err != nil {
		t.Fatal(err)
	}
	if err := NewIPv6Runtime(dir, runtimeSource()).Disable(); err != nil {
		t.Fatal(err)
	}
	restored := NewIPv6Runtime(dir, runtimeSource())
	if err := restored.Restore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := restored.Status().Mappings; len(got) != 1 || got[0] != runtimeSettings().Mappings[0] {
		t.Fatal("disable lost the saved services", got)
	}
}

func TestIPv6RuntimeTracksPrefixAndPortChangesWithoutStaleEndpoints(t *testing.T) {
	ip := "2606:4700:1234::1"
	source := ipv6SourceFunc(func(context.Context) ([]ipv6.Address, error) {
		if ip == "" {
			return nil, nil
		}
		return []ipv6.Address{{IP: netip.MustParseAddr(ip), Interface: "eth0", PrefixBits: 64}}, nil
	})
	r := NewIPv6Runtime(t.TempDir(), source)
	if _, err := r.Start(context.Background(), runtimeSettings()); err != nil {
		t.Fatal(err)
	}
	ip = "2606:4700:5678::1"
	if strings.Contains(r.Endpoints()[0].URL, "1234") {
		t.Fatal("old prefix")
	}
	if err := r.AcceptMappings([]PortMapping{{LocalPort: 8443, PublicPort: 8443, Protocol: "https"}}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Endpoints()[0].URL, "]:8443/") {
		t.Fatal("old port")
	}
	if err := r.AcceptMappings([]PortMapping{{LocalPort: 80, PublicPort: 443, Protocol: "https"}}); err == nil {
		t.Fatal("NAT accepted")
	}
	if !strings.Contains(r.Endpoints()[0].URL, "]:8443/") {
		t.Fatal("invalid mapping lost valid settings")
	}
	ip = ""
	if len(r.Endpoints()) != 0 || r.Status().State != "failed" {
		t.Fatal("stale endpoints after prefix loss")
	}
}

func TestIPv6RuntimeRestorationFailsClosedOnUnapprovedOrMalformedStore(t *testing.T) {
	for _, raw := range []string{`{"schema":1,"enabled":true,"confirmExposure":false,"mappings":[{"localPort":7443,"publicPort":7443,"protocol":"https"}]}`, `{"schema":1,"enabled":true,"confirmExposure":true,"mappings":[{"localPort":7443,"publicPort":443,"protocol":"https"}]}`, `{"schema":99}`, `{"schema":1,"unknown":true}`, `{} {}`} {
		dir := t.TempDir()
		if err := connector.WritePrivate(filepath.Join(dir, "ipv6.json"), []byte(raw)); err != nil {
			t.Fatal(err)
		}
		r := NewIPv6Runtime(dir, runtimeSource())
		if err := r.Restore(context.Background()); err == nil || len(r.Endpoints()) != 0 || r.Status().State != "failed" {
			t.Fatal("restored invalid store", raw, err)
		}
	}
}

func TestIPv6RuntimePersistenceFailureNeverReportsDisableSuccess(t *testing.T) {
	dir := t.TempDir()
	r := NewIPv6Runtime(dir, runtimeSource())
	if _, err := r.Start(context.Background(), runtimeSettings()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dir, "ipv6.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "ipv6.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if r.Disable() == nil {
		t.Fatal("failed write reported disabled")
	}
	if r.Status().State != "connected" {
		t.Fatal("in-memory disable without durable revocation")
	}
}

func TestIPv6RuntimePortRemovalPreservesApprovedIntentAcrossRestore(t *testing.T) {
	dir := t.TempDir()
	r := NewIPv6Runtime(dir, runtimeSource())
	if _, err := r.Start(context.Background(), runtimeSettings()); err != nil {
		t.Fatal(err)
	}
	if err := r.AcceptMappings(nil); err != nil {
		t.Fatal(err)
	}
	if r.Status().State != "failed" || len(r.Endpoints()) != 0 {
		t.Fatal("port removal left endpoints")
	}
	fresh := NewIPv6Runtime(dir, runtimeSource())
	if err := fresh.Restore(context.Background()); err != nil {
		t.Fatal("empty live mapping invalidated durable intent", err)
	}
	if fresh.Status().State != "failed" || len(fresh.Endpoints()) != 0 {
		t.Fatal("restored endpoint without ports")
	}
	if err := fresh.AcceptMappings(runtimeSettings().Mappings); err != nil {
		t.Fatal(err)
	}
	if fresh.Status().State != "connected" {
		t.Fatal("port recovery lost approved intent")
	}
}

func TestIPv6RuntimeForgetClearsSavedServicesOnlyWhenDisabled(t *testing.T) {
	dir := t.TempDir()
	r := NewIPv6Runtime(dir, runtimeSource())
	if _, err := r.Start(context.Background(), runtimeSettings()); err != nil {
		t.Fatal(err)
	}
	if err := r.Forget(); err == nil || len(r.Status().Mappings) != 1 {
		t.Fatal("forget while enabled", err)
	}
	if err := r.Disable(); err != nil {
		t.Fatal(err)
	}
	if err := r.Forget(); err != nil || len(r.Status().Mappings) != 0 {
		t.Fatal("forget", err, r.Status().Mappings)
	}
	fresh := NewIPv6Runtime(dir, runtimeSource())
	if err := fresh.Restore(context.Background()); err != nil || len(fresh.Status().Mappings) != 0 || fresh.Status().State != "disconnected" {
		t.Fatal("forgotten services restored", err, fresh.Status())
	}
}
