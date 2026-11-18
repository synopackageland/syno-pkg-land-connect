package ports

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// dsm74 is DSM 7.2+'s datastore (test NAS 7.4); dsm71 is DSM 7.1.1's (legacy NAS),
// whose keys are only fqdn, https, port, ssl and version, as libsynow3's
// DSM6-2 writer (TransVarToJson) also produces.
const (
	dsm74 = `{"mainApp":"DSM","version":1,"port":5000,"ssl":{"port":5001},"directAccess":false}`
	dsm71 = `{"fqdn":null,"https":{"compression":false,"hsts":false},"port":5000,"ssl":{"port":5001,"redirect":false},"version":1}`
)

func TestDSMDatastoreReadsDSM71AndDSM74Formats(t *testing.T) {
	for _, raw := range []string{dsm74, dsm71} {
		p := filepath.Join(t.TempDir(), "DSM.json")
		os.WriteFile(p, []byte(raw), 0600)
		if v, e := (DatastoreDetector{Path: p}).Detect(context.Background()); e != nil || v != (DSMPorts{5000, 5001}) {
			t.Fatalf("%s: %v %v", raw, v, e)
		}
	}
}

func TestDSMDatastoreRejectsMissingInvalidAndUnreadable(t *testing.T) {
	for _, raw := range []string{
		`{}`, `{"mainApp":"FileStation","version":1,"port":5000,"ssl":{"port":5001}}`,
		`{"mainApp":"DSM","version":2,"port":5000,"ssl":{"port":5001}}`,
		`{"mainApp":"DSM","version":1,"port":5000}`,
		`{"mainApp":"DSM","version":1,"port":0,"ssl":{"port":65536}}`,
		`{"mainApp":"DSM","version":1,"port":5000,"ssl":{"port":5000}}`,
		`{"mainApp":"DSM","version":1,"port":"5000","ssl":{"port":5001}}`,
		`{"mainApp":"DSM","version":1,"port":5000,"ssl":{"port":5001}} {}`,
		`{"mainApp":"","version":1,"port":5000,"ssl":{"port":5001}}`,
		`{"port":5000,"ssl":{"port":5001}}`,
		`{"version":1,"port":5000}`,
		`{"version":1,"port":5000,"ssl":{"port":5000}}`,
	} {
		p := filepath.Join(t.TempDir(), "DSM.json")
		os.WriteFile(p, []byte(raw), 0600)
		if v, e := (DatastoreDetector{Path: p}).Detect(context.Background()); !errors.Is(e, ErrDetection) || v != (DSMPorts{}) {
			t.Fatalf("accepted %s: %v %v", raw, v, e)
		}
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "absent")
	if _, e := (DatastoreDetector{Path: path}).Detect(context.Background()); !errors.Is(e, ErrDetection) {
		t.Fatal(e)
	}
	target := filepath.Join(dir, "target")
	os.WriteFile(target, []byte(`{"mainApp":"DSM","version":1,"port":5000,"ssl":{"port":5001}}`), 0600)
	os.Symlink(target, path)
	if _, e := (DatastoreDetector{Path: path}).Detect(context.Background()); !errors.Is(e, ErrDetection) {
		t.Fatal("link", e)
	}
	os.Remove(path)
	os.WriteFile(path, make([]byte, 65537), 0600)
	if _, e := (DatastoreDetector{Path: path}).Detect(context.Background()); !errors.Is(e, ErrDetection) {
		t.Fatal("size", e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e := (DatastoreDetector{Path: target}).Detect(ctx); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}

func TestDSMDatastorePollingReadsCurrentPairAndWithdraws(t *testing.T) {
	path := filepath.Join(t.TempDir(), "DSM.json")
	write := func(raw string) {
		t.Helper()
		if e := os.WriteFile(path, []byte(raw), 0600); e != nil {
			t.Fatal(e)
		}
	}
	write(dsm74)
	d := DatastoreDetector{Path: path}
	ctx := context.Background()
	s := Store{Dir: t.TempDir()}
	if v, e := d.Detect(ctx); e != nil || v != (DSMPorts{5000, 5001}) {
		t.Fatal(v, e)
	}
	if _, e := s.SetDSM(ctx, d, DSMHTTPS, true, false); !errors.Is(e, ErrConfirmation) {
		t.Fatal(e)
	}
	if _, e := s.SetDSM(ctx, d, DSMHTTPS, true, true); e != nil {
		t.Fatal(e)
	}
	f := &Fake{}
	r := Reconciler{Store: s, Detector: d, Applier: f}
	if _, e := r.Step(ctx, time.Now()); e != nil || f.Current().Mappings[0].Port != 5001 {
		t.Fatal(e)
	}
	write(`{"mainApp":"DSM","version":1,"port":6100,"ssl":{"port":6101}}`)
	if _, e := r.Step(ctx, time.Now()); e != nil || f.Current().Mappings[0].Port != 6101 {
		t.Fatal(e)
	}
	write(`{}`)
	if v, e := r.Step(ctx, time.Now()); e != nil || v.Detection != "needs-attention" || len(f.Current().Mappings) != 0 {
		t.Fatal(v, e)
	}
}

func TestDSMHTTPSPortDoesNotFollowRedirectOrAdvertisedAddress(t *testing.T) {
	for _, redirect := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "DSM.json")
		raw := fmt.Sprintf(`{"version":1,"port":6100,"ssl":{"port":6443,"redirect":%t},"fqdn":"advertised.example","https":{"hsts":true}}`, redirect)
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		got, err := (DatastoreDetector{Path: path}).Detect(context.Background())
		if err != nil || got != (DSMPorts{6100, 6443}) {
			t.Fatal("redirect or advertised address changed the local HTTPS port", got, err)
		}
	}
}
