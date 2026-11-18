package ports

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestServicesFollowInstalledDeclarationsWithoutStalePorts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Drive.sc")
	write := func(raw string) {
		t.Helper()
		if e := os.WriteFile(path, []byte(raw), 0600); e != nil {
			t.Fatal(e)
		}
	}
	write("[drive]\ntitle=\"Drive\"\ndst.ports=\"6690/tcp\"\n[discovery]\ndst.ports=\"123/udp\"\n")
	reads := 0
	d := ServiceDetector{Dir: dir, read: func(path string) ([]byte, error) { reads++; return os.ReadFile(path) }}
	got, e := d.DetectServices(context.Background())
	if e != nil || len(got.Services) != 1 || got.Services[0].Port != 6690 || !got.UDPSkipped {
		t.Fatal(got, e)
	}
	for i := 0; i < 10; i++ {
		if _, e = d.DetectServices(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
	if reads != 1 {
		t.Fatal("unchanged files reread", reads)
	}
	write("[drive]\ndst.ports=\"6691/tcp\"\n")
	got, e = d.DetectServices(context.Background())
	if e != nil || got.Services[0].Port != 6691 {
		t.Fatal(got, e)
	}
	os.Remove(path)
	got, e = d.DetectServices(context.Background())
	if e != nil || len(got.Services) != 0 {
		t.Fatal("removed service reused", got, e)
	}
	write("[drive]\ndst.ports=\"bad/tcp\"\n")
	if _, e = d.DetectServices(context.Background()); e == nil {
		t.Fatal("invalid declaration accepted")
	}
	os.Remove(path)
	os.Symlink("/dev/null", path)
	if _, e = d.DetectServices(context.Background()); e == nil {
		t.Fatal("symlink accepted")
	}
}

func TestServicesRangesDeduplicateAndBoundAllOrNothing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SMB.sc")
	os.WriteFile(path, []byte("[smb]\ndst.ports=\"137:139,445/udp,tcp\"\n[duplicate]\ndst.ports=\"445/tcp\"\n"), 0600)
	d := ServiceDetector{Dir: dir}
	got, e := d.DetectServices(context.Background())
	if e != nil || len(got.Services) != 4 || got.Overflow || !got.UDPSkipped {
		t.Fatal(got, e)
	}
	os.WriteFile(path, []byte("[huge]\ndst.ports=\"1:65535/tcp\"\n"), 0600)
	got, e = d.DetectServices(context.Background())
	if e != nil || !got.Overflow || len(got.Services) > 17 {
		t.Fatal(got, e)
	}
}

func TestFollowIsOptInAndDetectionFailureWithdrawsWholeDynamicSet(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "Drive.sc")
	os.WriteFile(path, []byte("[drive]\ndst.ports=\"6690/tcp\"\n"), 0600)
	s := Store{Dir: t.TempDir(), Services: &ServiceDetector{Dir: dir}}
	if v := mustList(t, s, goodDetector()); v.FollowServices || len(v.Plan.Mappings) != 0 {
		t.Fatal(v)
	}
	if _, e := s.SetFollow(ctx, goodDetector(), true, false); e != ErrConfirmation {
		t.Fatal(e)
	}
	v, e := s.SetFollow(ctx, goodDetector(), true, true)
	if e != nil || !v.FollowServices || len(v.Plan.Mappings) != 1 {
		t.Fatal(v, e)
	}
	os.WriteFile(path, []byte("[drive]\ndst.ports=\"broken\"\n"), 0600)
	v = mustList(t, s, goodDetector())
	if v.FollowState != "unavailable" || len(v.Plan.Mappings) != 0 {
		t.Fatal(v)
	}
	os.WriteFile(path, []byte("[drive]\ndst.ports=\"1:65535/tcp\"\n"), 0600)
	v = mustList(t, s, goodDetector())
	if v.FollowState != "limit" || len(v.Plan.Mappings) != 0 {
		t.Fatal(v)
	}
	if _, e = s.SetFollow(ctx, goodDetector(), false, false); e != nil {
		t.Fatal(e)
	}
}

func TestManualMutationsKeepFollowStateInTheirResponses(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "Drive.sc"), []byte("[drive]\ndst.ports=\"6690/tcp\"\n"), 0600)
	s := Store{Dir: t.TempDir(), Services: &ServiceDetector{Dir: dir}}
	if _, err := s.SetFollow(ctx, goodDetector(), true, true); err != nil {
		t.Fatal(err)
	}
	v, err := s.SetDSM(ctx, goodDetector(), DSMHTTP, true, true)
	if err != nil || !v.FollowServices || v.FollowState != "ready" || len(v.Plan.Mappings) != 2 {
		t.Fatal(v, err)
	}
	v, err = s.Upsert(ctx, goodDetector(), Input{Name: "Web", Protocol: TCP, Port: 8080, Enabled: true, ConfirmExposure: true})
	if err != nil || !v.FollowServices || v.FollowState != "ready" || len(v.Plan.Mappings) != 3 {
		t.Fatal(v, err)
	}
}

// MailPlus Server writes bare values; one such file must not withdraw every
// followed service. A half-quoted value is still refused.
func TestServicesAcceptBareValuesBesideQuotedFiles(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "MailPlus-Server.sc"), []byte("[mail_haproxy_smtp]\ntitle=MailPlus Server (SMTP)\ndst.ports=25/tcp\nport_forward=yes\ndesc=MailPlus Server\n"), 0600)
	os.WriteFile(filepath.Join(dir, "SynologyDrive.sc"), []byte("[cloudstation]\ntitle=\"Synology Drive Server\"\ndst.ports=\"6690/tcp\"\n"), 0600)
	os.WriteFile(filepath.Join(dir, "DNSServer.sc"), []byte("[dns]\ntitle=\"DNS\"\ndst.ports=\"53/all\"\n"), 0600)
	d := ServiceDetector{Dir: dir}
	got, e := d.DetectServices(context.Background())
	if e != nil || len(got.Services) != 3 || got.Services[0].Port != 25 || got.Services[1].Port != 53 || got.Services[2].Port != 6690 || !got.UDPSkipped {
		t.Fatal(got, e)
	}
	if got.Services[0].Name != "MailPlus-Server / MailPlus Server (SMTP)" {
		t.Fatal(got.Services[0].Name)
	}
	for _, half := range []string{"dst.ports=\"25/tcp\n", "dst.ports=25/tcp\"\n", "title=\"\n"} {
		os.WriteFile(filepath.Join(dir, "MailPlus-Server.sc"), []byte("[mail]\n"+half), 0600)
		if _, e := (&ServiceDetector{Dir: dir}).DetectServices(context.Background()); e == nil {
			t.Fatalf("%q accepted", half)
		}
	}
}
