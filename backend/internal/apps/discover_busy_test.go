package apps

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBusyDSMPortReadKeepsTheListWithLastKnownPorts(t *testing.T) {
	root := t.TempDir()
	writeDiscoveryGateway(t, root, "SynoPkgOne", gatewayFixture("SynoPkgOne", "", 28001))
	writeDiscoveryGateway(t, root, "SynoPkgDSM", gatewayFixture("SynoPkgDSM", "", 5001))
	busy := false
	d := Discovery{PackagesDir: root, Known: &KnownPorts{}, Ping: func(context.Context, string, int) string { return Ready },
		DetectPorts: func(ctx context.Context) (int, int, error) {
			if busy {
				return 0, 0, errors.New("datastore timed out")
			}
			return fixturePorts(ctx)
		}}
	if _, e := d.Scan(context.Background()); e != nil {
		t.Fatal(e)
	}
	busy = true
	if _, e := d.Scan(context.Background()); !errors.Is(e, ErrUnavailable) {
		t.Fatal("a change must still refuse without fresh DSM ports", e)
	}
	got, e := d.ScanList(context.Background())
	if e != nil || len(got) != 1 || got[0].Package != "SynoPkgOne" || got[0].PingStatus != Ready {
		t.Fatalf("the list must reuse the last DSM ports: %+v %v", got, e)
	}
}

func TestListWithoutAnyDSMPortsMarksEveryAppWaiting(t *testing.T) {
	root := t.TempDir()
	writeDiscoveryGateway(t, root, "SynoPkgOne", gatewayFixture("SynoPkgOne", "", 28001))
	writeDiscoveryGateway(t, root, "SynoPkgTwo", gatewayFixture("SynoPkgTwo", "", 28002))
	d := Discovery{PackagesDir: root, Known: &KnownPorts{},
		Ping:        func(context.Context, string, int) string { panic("no ping without DSM ports") },
		DetectPorts: func(context.Context) (int, int, error) { return 0, 0, errors.New("busy") }}
	got, e := d.ScanList(context.Background())
	if e != nil || len(got) != 2 {
		t.Fatalf("the list must still show the Apps: %+v %v", got, e)
	}
	for _, app := range got {
		if app.PingStatus != Waiting || !app.Waiting {
			t.Fatalf("an unchecked App waits: %+v", app)
		}
	}
}

func TestSlowPingsWaitInsteadOfFailingTheList(t *testing.T) {
	root := t.TempDir()
	writeDiscoveryGateway(t, root, "SynoPkgFast", gatewayFixture("SynoPkgFast", "", 28001))
	writeDiscoveryGateway(t, root, "SynoPkgSlow", gatewayFixture("SynoPkgSlow", "", 28002))
	d := Discovery{PackagesDir: root, DetectPorts: fixturePorts, Ping: func(ctx context.Context, pkg string, _ int) string {
		if pkg == "SynoPkgSlow" {
			<-ctx.Done()
			return Waiting
		}
		return Ready
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	got, e := d.ScanList(ctx)
	if e != nil || len(got) != 2 {
		t.Fatalf("a slow App must not fail the list: %+v %v", got, e)
	}
	for _, app := range got {
		if want := map[string]string{"SynoPkgFast": Ready, "SynoPkgSlow": Waiting}[app.Package]; app.PingStatus != want {
			t.Fatalf("%s: %s, want %s", app.Package, app.PingStatus, want)
		}
	}
}
