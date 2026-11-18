package ports

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

type detectorFunc func(context.Context) (DSMPorts, error)

func (f detectorFunc) Detect(ctx context.Context) (DSMPorts, error) { return f(ctx) }
func goodDetector() Detector {
	return detectorFunc(func(context.Context) (DSMPorts, error) { return DSMPorts{5000, 5001}, nil })
}
func mustList(t *testing.T, s Store, d Detector) Snapshot {
	t.Helper()
	v, e := s.List(context.Background(), d)
	if e != nil {
		t.Fatal(e)
	}
	return v
}

func TestFileDetectorRejectsIncompleteInvalidAndDuplicatePorts(t *testing.T) {
	for _, raw := range []string{"", `admin_port="5000"`, "admin_port=0\nsecure_admin_port=5001", "admin_port=5000\nsecure_admin_port=65536", "admin_port=5000\nsecure_admin_port=5000", "admin_port=5000\nadmin_port=5002\nsecure_admin_port=5001", "admin_port=+5000\nsecure_admin_port=5001", "admin_port=\"5000\"garbage\nsecure_admin_port=5001"} {
		t.Run(raw, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "synoinfo.conf")
			os.WriteFile(p, []byte(raw), 0600)
			if _, e := (FileDetector{Path: p}).Detect(context.Background()); !errors.Is(e, ErrDetection) {
				t.Fatalf("accepted %q: %v", raw, e)
			}
		})
	}
	p := filepath.Join(t.TempDir(), "synoinfo.conf")
	os.WriteFile(p, []byte("other=\"ignore\"\nadmin_port=\"15000\"\nsecure_admin_port=\"15001\"\n"), 0600)
	d := FileDetector{Path: p}
	v, e := d.Detect(context.Background())
	if e != nil || v != (DSMPorts{15000, 15001}) {
		t.Fatalf("%v %v", v, e)
	}
	os.Remove(p)
	os.Symlink("/dev/null", p)
	if _, e = d.Detect(context.Background()); !errors.Is(e, ErrDetection) {
		t.Fatal("symlink accepted")
	}
}
func TestDefaultsConsentPersistenceAndNoStaleDetection(t *testing.T) {
	ctx := context.Background()
	s := Store{Dir: t.TempDir()}
	d := goodDetector()
	v := mustList(t, s, d)
	if len(v.Entries) != 2 || v.Entries[0].Enabled || len(v.Plan.Mappings) != 0 || v.MappingState != "pending-provider" {
		t.Fatalf("unsafe default: %+v", v)
	}
	if _, e := s.SetDSM(ctx, d, DSMHTTP, true, false); !errors.Is(e, ErrConfirmation) {
		t.Fatal(e)
	}
	if _, e := s.SetDSM(ctx, d, DSMHTTP, true, true); e != nil {
		t.Fatal(e)
	}
	in := Input{Name: "Web", Protocol: HTTPS, Port: 443, Enabled: true, ConfirmExposure: true}
	v, e := s.Upsert(ctx, d, in)
	if e != nil {
		t.Fatal(e)
	}
	id := v.Entries[2].ID
	reopened := Store{Dir: s.Dir}
	v = mustList(t, reopened, d)
	if len(v.Plan.Mappings) != 2 {
		t.Fatalf("not persisted: %+v", v)
	}
	data, e := os.Stat(filepath.Join(s.Dir, "ports.json"))
	if e != nil || data.Mode().Perm() != 0600 {
		t.Fatal("nonprivate store", e)
	}
	unavailable := detectorFunc(func(context.Context) (DSMPorts, error) { return DSMPorts{}, errors.New("private detail") })
	v = mustList(t, reopened, unavailable)
	if v.Detection != "needs-attention" || len(v.Plan.Mappings) != 0 || v.Entries[0].Port != nil || v.Entries[2].State != "needs-attention" {
		t.Fatalf("stale mapping retained: %+v", v)
	}
	if _, e = s.Delete(ctx, id); e != nil {
		t.Fatal(e)
	}
	if len(mustList(t, s, d).Entries) != 2 {
		t.Fatal("delete not persisted")
	}
}
func TestValidationAndDynamicConflicts(t *testing.T) {
	ctx := context.Background()
	s := Store{Dir: t.TempDir()}
	d := goodDetector()
	for _, in := range []Input{{Name: "", Protocol: HTTP, Port: 80}, {Name: "web", Protocol: "udp", Port: 80}, {Name: "web", Protocol: HTTP, Port: 0}, {Name: "web", Protocol: TCP, Port: 65536}, {Name: "web\ncmd", Protocol: HTTP, Port: 80}, {ID: DSMHTTP, Name: "spoof", Protocol: HTTP, Port: 80}, {Name: "web", Protocol: HTTP, Port: 5001}} {
		if _, e := s.Upsert(ctx, d, in); !errors.Is(e, ErrValidation) && !errors.Is(e, ErrConflict) {
			t.Fatalf("accepted %+v: %v", in, e)
		}
	}
	if _, e := s.Upsert(ctx, d, Input{Name: "web", Protocol: HTTP, Port: 80, Enabled: true}); !errors.Is(e, ErrConfirmation) {
		t.Fatal(e)
	}
	v, e := s.Upsert(ctx, d, Input{Name: "web", Protocol: HTTP, Port: 80, Enabled: true, ConfirmExposure: true})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = s.Upsert(ctx, d, Input{Name: "other", Protocol: TCP, Port: 80}); !errors.Is(e, ErrConflict) {
		t.Fatal(e)
	}
	if _, e = s.Delete(ctx, DSMHTTP); !errors.Is(e, ErrValidation) {
		t.Fatal("deleted auto entry")
	}
	moved := detectorFunc(func(context.Context) (DSMPorts, error) { return DSMPorts{80, 5001}, nil })
	v = mustList(t, s, moved)
	if v.Entries[2].State != "conflict" || len(v.Plan.Mappings) != 0 {
		t.Fatalf("dynamic conflict exposed: %+v", v)
	}
	// Editing an enabled service to a different port requires new consent.
	if _, e = s.Upsert(ctx, d, Input{ID: v.Entries[2].ID, Name: "web", Protocol: HTTP, Port: 81, Enabled: true}); !errors.Is(e, ErrConfirmation) {
		t.Fatal("inherited consent for changed target", e)
	}
}
func TestConcurrentCGIStoresDoNotLoseUpdates(t *testing.T) {
	dir := t.TempDir()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, e := (Store{Dir: dir}).Upsert(context.Background(), goodDetector(), Input{Name: "service", Protocol: TCP, Port: 10000 + i})
			if e != nil {
				t.Error(e)
			}
		}(i)
	}
	wg.Wait()
	if len(mustList(t, Store{Dir: dir}, goodDetector()).Entries) != 22 {
		t.Fatal("lost concurrent update")
	}
}
func TestCorruptAndSymlinkStoreFailClosed(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	path := filepath.Join(s.Dir, "ports.json")
	for _, raw := range []string{`{}`, `{"schema":1,"dsmHTTP":true,"dsmHTTPS":false,"revision":1,"custom":[]} trailing`, `{"schema":1,"dsmHTTP":false,"dsmHTTPS":false,"revision":0,"custom":[],"injected":true}`} {
		os.WriteFile(path, []byte(raw), 0600)
		if _, e := s.List(context.Background(), goodDetector()); !errors.Is(e, ErrSettings) {
			t.Fatal("corrupt store accepted", e)
		}
	}
	os.Remove(path)
	os.Symlink("/dev/null", path)
	if _, e := s.List(context.Background(), goodDetector()); !errors.Is(e, ErrSettings) {
		t.Fatal("symlink accepted", e)
	}
}
func TestReconcilerDebouncesAndImmediatelyWithdrawsUnsafeMappings(t *testing.T) {
	ctx := context.Background()
	s := Store{Dir: t.TempDir()}
	s.SetDSM(ctx, goodDetector(), DSMHTTP, true, true)
	fake := &Fake{}
	r := Reconciler{Store: s, Detector: goodDetector(), Applier: fake, Debounce: time.Second}
	now := time.Unix(0, 0)
	if _, e := r.Step(ctx, now); e != nil {
		t.Fatal(e)
	}
	if len(fake.Plans()) != 1 || len(fake.Plans()[0].Mappings) != 0 {
		t.Fatal("startup must clear prior routes")
	}
	r.Step(ctx, now.Add(time.Second))
	if fake.Current().Mappings[0].Port != 5000 {
		t.Fatal("initial mapping missing")
	}
	r.Detector = detectorFunc(func(context.Context) (DSMPorts, error) { return DSMPorts{6000, 6001}, nil })
	r.Step(ctx, now.Add(2*time.Second))
	if len(fake.Current().Mappings) != 0 {
		t.Fatal("old port retained during debounce")
	}
	r.Step(ctx, now.Add(2500*time.Millisecond))
	if len(fake.Current().Mappings) != 0 {
		t.Fatal("applied too early")
	}
	r.Step(ctx, now.Add(3*time.Second))
	if fake.Current().Mappings[0].Port != 6000 {
		t.Fatal("new port not applied")
	}
	r.Detector = detectorFunc(func(context.Context) (DSMPorts, error) { return DSMPorts{}, ErrDetection })
	v, e := r.Step(ctx, now.Add(4*time.Second))
	if e != nil || v.Detection != "needs-attention" || len(fake.Current().Mappings) != 0 {
		t.Fatal("failure kept stale routes", v, e)
	}
}
func TestFailedApplyRevokesAndReportsCleanupFailure(t *testing.T) {
	ctx := context.Background()
	fake := &Fake{ApplyError: errors.New("fail"), RevokeError: errors.New("fail")}
	r := Reconciler{Store: Store{Dir: t.TempDir()}, Detector: goodDetector(), Applier: fake}
	v, e := r.Step(ctx, time.Now())
	if e == nil || v.MappingState != "cleanup-required" {
		t.Fatal("hidden cleanup failure", v, e)
	}
	fake.RevokeError = nil
	v, e = r.Step(ctx, time.Now())
	if e == nil || v.MappingState != "needs-attention" {
		t.Fatal("incorrect failure status", v, e)
	}
	fake.ApplyError = nil
	v, e = r.Step(ctx, time.Now())
	if e != nil || v.MappingState != "applied" {
		t.Fatal("no retry", v, e)
	}
}
func TestRunObservesExternalSettingsAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := Store{Dir: t.TempDir()}
	fake := &Fake{}
	r := Reconciler{Store: s, Detector: goodDetector(), Applier: fake, Debounce: time.Millisecond}
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx, 5*time.Millisecond, nil) }()
	s.SetDSM(context.Background(), goodDetector(), DSMHTTPS, true, true)
	deadline := time.Now().Add(time.Second)
	for len(fake.Current().Mappings) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if len(fake.Current().Mappings) != 1 {
		t.Fatal("CGI write not observed")
	}
	cancel()
	if e := <-done; e != nil {
		t.Fatal(e)
	}
	if len(fake.Current().Mappings) != 0 {
		t.Fatal("shutdown left mappings")
	}
}

func TestPollingReopensReplacedDSMConfigWithoutHints(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "synoinfo.conf")
	ctx := context.Background()
	write := func(raw string) {
		t.Helper()
		tmp := path + ".new"
		if e := os.WriteFile(tmp, []byte(raw), 0600); e != nil {
			t.Fatal(e)
		}
		if e := os.Rename(tmp, path); e != nil {
			t.Fatal(e)
		}
	}
	write("admin_port=5000\nsecure_admin_port=5001\n")
	d := FileDetector{Path: path}
	s := Store{Dir: dir}
	if _, e := s.SetDSM(ctx, d, DSMHTTPS, true, true); e != nil {
		t.Fatal(e)
	}
	fake := &Fake{}
	r := Reconciler{Store: s, Detector: d, Applier: fake}
	r.Step(ctx, time.Now())
	if fake.Current().Mappings[0].Port != 5001 {
		t.Fatal("initial config not applied")
	}
	write("admin_port=6000\nsecure_admin_port=6001\n")
	r.Step(ctx, time.Now())
	if fake.Current().Mappings[0].Port != 6001 {
		t.Fatal("replacement missed")
	}
	write("admin_port=6000\n")
	r.Step(ctx, time.Now())
	if len(fake.Current().Mappings) != 0 {
		t.Fatal("partial config did not withdraw mapping")
	}
}
func TestCanceledAndBoundedSettings(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	s := Store{Dir: t.TempDir()}
	if _, e := s.List(ctx, goodDetector()); !errors.Is(e, context.Canceled) {
		t.Fatal("ignored cancellation", e)
	}
	for i := 0; i < MaxCustom; i++ {
		if _, e := s.Upsert(context.Background(), goodDetector(), Input{Name: "service", Protocol: TCP, Port: 10000 + i}); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := s.Upsert(context.Background(), goodDetector(), Input{Name: "overflow", Protocol: TCP, Port: 20000}); !errors.Is(e, ErrValidation) {
		t.Fatal("unbounded settings", e)
	}
}

// holdLock holds ports.lock as a writer does during a slow fsync on a loaded NAS.
func holdLock(t *testing.T, dir string) func() {
	t.Helper()
	f, e := os.OpenFile(filepath.Join(dir, "ports.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		t.Fatal(e)
	}
	if e = syscall.Flock(int(f.Fd()), syscall.LOCK_EX); e != nil {
		t.Fatal(e)
	}
	return func() { syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }
}
func TestListAndObservationDoNotWaitBehindAWriter(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	if _, e := s.Upsert(context.Background(), goodDetector(), Input{Name: "service", Protocol: HTTP, Port: 8080, Enabled: true, ConfirmExposure: true}); e != nil {
		t.Fatal(e)
	}
	release := holdLock(t, s.Dir)
	defer release()
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	v, e := s.List(ctx, goodDetector())
	if e != nil || len(v.Plan.Mappings) != 1 {
		t.Fatal("ports.list must read the atomically replaced file without the writer lock", e)
	}
	if e = s.RecordObservation(ctx, v); e != nil {
		t.Fatal("the daemon's observation must not contend with CGI writers", e)
	}
}
func TestWritersDetectOutsideTheLock(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	entered := make(chan struct{})
	release := make(chan struct{})
	slow := detectorFunc(func(context.Context) (DSMPorts, error) {
		close(entered)
		<-release
		return DSMPorts{5000, 5001}, nil
	})
	done := make(chan error, 1)
	go func() { _, e := s.SetDSM(context.Background(), slow, DSMHTTPS, false, false); done <- e }()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, e := s.Upsert(ctx, goodDetector(), Input{Name: "service", Protocol: TCP, Port: 9000}); e != nil {
		t.Fatal("a slow DSM port detection must not hold the settings lock", e)
	}
	close(release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}
func TestKnownPortsListsEveryExistingServiceOnlyWhenDetected(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	for _, in := range []Input{{Name: "on", Protocol: HTTP, Port: 8080, Enabled: true, ConfirmExposure: true}, {Name: "off", Protocol: TCP, Port: 9000}} {
		if _, e := s.Upsert(context.Background(), goodDetector(), in); e != nil {
			t.Fatal(e)
		}
	}
	known, e := (ResolvedSource{Store: s, Detector: goodDetector()}).KnownPorts(context.Background())
	if e != nil || len(known) != 4 {
		t.Fatal("DSM, enabled and disabled services all exist", known, e)
	}
	failing := detectorFunc(func(context.Context) (DSMPorts, error) { return DSMPorts{}, ErrDetection })
	if _, e = (ResolvedSource{Store: s, Detector: failing}).KnownPorts(context.Background()); e == nil {
		t.Fatal("an undetected DSM must not read as deleted DSM services")
	}
}
