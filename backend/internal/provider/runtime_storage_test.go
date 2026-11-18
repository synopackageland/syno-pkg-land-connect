package provider

import (
	"dsmapp/internal/transient"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRuntimeEvictionRescuesAccessBeforeOverflowStop(t *testing.T) {
	for _, kind := range []string{"tailscale", "netbird"} {
		t.Run(kind, func(t *testing.T) {
			dir := storageFixture(t, kind)
			for i := 0; i < 8; i++ {
				storageFile(t, dir, "required/"+string(rune('a'+i)), (1<<20)-4096, time.Hour)
			}
			// These caches/backups fit the ordinary retention targets, but
			// must yield when required state needs the overall budget.
			if kind == "tailscale" {
				storageFile(t, dir, "certs/old.example.crt", 64<<10, time.Hour)
				storageFile(t, dir, "certs/old.example.key", 64<<10, time.Hour)
			} else {
				storageFile(t, dir, "state.json.corrupted.1", 64<<10, time.Hour)
				storageFile(t, dir, "state.json.corrupted.2", 64<<10, time.Minute)
			}
			if err := pruneRuntime(dir, kind, time.Now()); err != nil {
				t.Fatal("stopped instead of reclaiming safe data", err)
			}
			for _, name := range []string{"certs/old.example.crt", "certs/old.example.key", "state.json.corrupted.1", "state.json.corrupted.2"} {
				if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
					t.Fatal("retained pressure-eviction candidate", name, err)
				}
			}
			if st, err := os.Stat(filepath.Join(dir, "required/a")); err != nil || st.Size() != (1<<20)-4096 {
				t.Fatal("damaged required state", err)
			}
		})
	}
}

func TestRuntimeStopsOnlyAfterSafeEvictionStillLeavesOverflow(t *testing.T) {
	for _, kind := range []string{"tailscale", "netbird"} {
		t.Run(kind, func(t *testing.T) {
			dir := storageFixture(t, kind)
			for i := 0; i < 9; i++ {
				storageFile(t, dir, "required/"+string(rune('a'+i)), ProviderFileLimit, time.Hour)
			}
			var safe []string
			if kind == "tailscale" {
				safe = []string{"certs/old.example.crt", "certs/old.example.key", "dns-cache/dns-old.json", "derpmap.cached.json", "derpmap.cached.json.tmp1234"}
			} else {
				safe = []string{"state.json.corrupted.1", "state.json.corrupted.2", ".123state.json"}
			}
			for _, name := range safe {
				storageFile(t, dir, name, 4096, time.Hour)
			}
			if err := pruneRuntime(dir, kind, time.Now()); !errors.Is(err, errRuntimeBudget) {
				t.Fatal("required-state overflow accepted", err)
			}
			for _, name := range safe {
				if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
					t.Fatal("failed before safe eviction", name, err)
				}
			}
			if _, err := os.Stat(filepath.Join(dir, "required/a")); err != nil {
				t.Fatal("deleted required state", err)
			}
		})
	}
}

func TestRuntimeMonitorHasNoTickerAfterWriterExit(t *testing.T) {
	dir := storageFixture(t, "tailscale")
	exited := make(chan struct{})
	m := watchRuntime(dir, "tailscale", time.Hour, func() error { t.Error("stopped exited writer"); return nil }, exited)
	close(exited)
	select {
	case <-m.done:
	case <-time.After(time.Second):
		t.Fatal("monitor outlived writer")
	}
	m.Close()
	if m.failed.Load() {
		t.Fatal("normal exit became a budget failure")
	}
}

func TestRuntimeAtomicPartMatchingPreservesRealDomainNames(t *testing.T) {
	if atomicRuntimePart("host.tmp.example.crt") || !atomicRuntimePart("state.json.tmp123456") {
		t.Fatal("unsafe atomic-part classification")
	}
}

func TestRuntimeUnsafeStorageDoesNotMisreportOverflow(t *testing.T) {
	dir := storageFixture(t, "tailscale")
	if err := os.Symlink(t.TempDir(), filepath.Join(dir, "dns-cache")); err != nil {
		t.Fatal(err)
	}
	stopped := make(chan struct{})
	m := watchRuntime(dir, "tailscale", time.Millisecond, func() error { close(stopped); return nil }, nil)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("unsafe storage did not stop writer")
	}
	m.Close()
	if m.failureReason() != "tmpfs_storage_unavailable" {
		t.Fatal("unsafe path misreported as full storage", m.failureReason())
	}
}

func storageFixture(t *testing.T, kind string) string {
	t.Helper()
	dir, err := transient.For(t.TempDir(), kind, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func storageFile(t *testing.T, dir, name string, size int, age time.Duration) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, make([]byte, size), 0600); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-age)
	if err := os.Chtimes(p, at, at); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestNetBirdRetentionKeepsNewestBackupsAndFreshWrites(t *testing.T) {
	dir := storageFixture(t, "netbird")
	for i, name := range []string{"state.json.corrupted.1", "state.json.corrupted.2", "state.json.corrupted.3", "state.json.corrupted.4"} {
		storageFile(t, dir, name, 8192, time.Duration(10-i)*time.Minute)
	}
	old := storageFile(t, dir, ".123state.json", 8192, 10*time.Minute)
	fresh := storageFile(t, dir, "config/netbird/.456default.state.json", 8192, time.Second)
	state := storageFile(t, dir, "state.json", 64, time.Hour)
	if err := pruneRuntime(dir, "netbird", time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{old, filepath.Join(dir, "state.json.corrupted.1"), filepath.Join(dir, "state.json.corrupted.2")} {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Fatal("retained stale backup/part", p, err)
		}
	}
	for _, p := range []string{state, fresh, filepath.Join(dir, "state.json.corrupted.3"), filepath.Join(dir, "state.json.corrupted.4")} {
		if _, err := os.Stat(p); err != nil {
			t.Fatal("removed current state or new write", p, err)
		}
	}
}

func TestTailscaleCachesEvictOldestAndKeepCertificatePairs(t *testing.T) {
	dir := storageFixture(t, "tailscale")
	for _, domain := range []string{"old.example.com", "new.example.com"} {
		age := time.Minute
		if domain == "old.example.com" {
			age = time.Hour
		}
		storageFile(t, dir, "certs/"+domain+".crt", 700<<10, age)
		storageFile(t, dir, "certs/"+domain+".key", 700<<10, age)
	}
	account := storageFile(t, dir, "certs/acme-account.key.pem", 2048, time.Hour)
	part := storageFile(t, dir, "certs/.active-write.tmp", 16, time.Second)
	for i := 0; i < 40; i++ {
		storageFile(t, dir, "dns-cache/dns-"+string(rune('a'+i))+".json", 8192, time.Duration(40-i)*time.Minute)
	}
	if err := pruneRuntime(dir, "tailscale", time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{".crt", ".key"} {
		if _, err := os.Stat(filepath.Join(dir, "certs/old.example.com"+suffix)); !os.IsNotExist(err) {
			t.Fatal("old certificate pair retained", err)
		}
		if _, err := os.Stat(filepath.Join(dir, "certs/new.example.com"+suffix)); err != nil {
			t.Fatal("new certificate pair lost", err)
		}
	}
	if _, err := os.Stat(account); err != nil {
		t.Fatal("ACME account lost", err)
	}
	if _, err := os.Stat(part); err != nil {
		t.Fatal("active atomic write removed", err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "dns-cache"))
	if len(entries) > 32 {
		t.Fatal("DNS cache count exceeded", len(entries))
	}
}

func TestRuntimeDNSCacheByteBudgetAndPermanentExit(t *testing.T) {
	dir := storageFixture(t, "tailscale")
	old := storageFile(t, dir, "dns-cache/old", 400<<10, time.Hour)
	newest := storageFile(t, dir, "dns-cache/new", 400<<10, time.Minute)
	if err := pruneRuntime(dir, "tailscale", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatal("old DNS bytes retained", err)
	}
	if _, err := os.Stat(newest); err != nil {
		t.Fatal("new DNS cache lost", err)
	}
	m := &runtimeMonitor{}
	m.failed.Store(true)
	ts := &tailscaleExecSession{storage: m}
	nb := &netbirdExecSession{storage: m}
	if ts.Alive() || nb.Alive() {
		t.Fatal("storage failure left provider alive")
	}
	for _, exit := range []struct {
		reason    string
		permanent bool
	}{
		{ts.LastExit().Reason, ts.LastExit().Permanent},
		{nb.LastExit().Reason, nb.LastExit().Permanent},
	} {
		if exit.reason != "tmpfs_budget_exceeded" || !exit.permanent {
			t.Fatal("storage failure would silently restart", exit)
		}
	}
}

func TestRuntimeBudgetRefusesToDeleteTailnetLockOrIdentity(t *testing.T) {
	dir := storageFixture(t, "tailscale")
	// Every file fits the existing kernel per-file ceiling; their sum does not.
	p := storageFile(t, dir, "tka-profiles/default/important-aum", ProviderFileLimit, time.Hour)
	for i := 0; i < 8; i++ {
		storageFile(t, dir, "tka-profiles/default/aum-"+string(rune('a'+i)), ProviderFileLimit, time.Hour)
	}
	if err := pruneRuntime(dir, "tailscale", time.Now()); !errors.Is(err, errRuntimeBudget) {
		t.Fatal("unbounded state accepted", err)
	}
	if st, err := os.Stat(p); err != nil || st.Size() != ProviderFileLimit {
		t.Fatal("required security history damaged", err)
	}
	dir = storageFixture(t, "netbird")
	for i := 0; i <= runtimeMaxEntries; i++ {
		storageFile(t, dir, "unknown/"+string(rune(0x100+i)), 0, time.Minute)
	}
	if err := pruneRuntime(dir, "netbird", time.Now()); !errors.Is(err, errRuntimeBudget) {
		t.Fatal("unbounded inode count accepted", err)
	}
}

func TestRuntimePruningNeverFollowsLinksOrReadsVolume(t *testing.T) {
	dir := storageFixture(t, "tailscale")
	outside := t.TempDir()
	p := storageFile(t, outside, "keep", 16, time.Hour)
	if err := os.Symlink(outside, filepath.Join(dir, "dns-cache")); err != nil {
		t.Fatal(err)
	}
	if err := pruneRuntime(dir, "tailscale", time.Now()); err == nil {
		t.Fatal("accepted redirected cache")
	}
	if st, err := os.Stat(p); err != nil || st.Size() != 16 {
		t.Fatal("followed cache link", err)
	}
	if err := pruneRuntime(outside, "netbird", time.Now()); err == nil {
		t.Fatal("accepted persistent directory")
	}
}

func TestRuntimeMonitorStopsWriterOnOverflowAndJoinsOnClose(t *testing.T) {
	dir := storageFixture(t, "tailscale")
	stopped := make(chan struct{})
	monitor := watchRuntime(dir, "tailscale", time.Millisecond, func() error { close(stopped); return nil }, nil)
	storageFile(t, dir, "tka-profiles/large", runtimeMaxBytes+1, 0)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("overflow did not stop writer")
	}
	monitor.Close()
	if !monitor.failed.Load() {
		t.Fatal("failure not retained")
	}
	dir = storageFixture(t, "netbird")
	monitor = watchRuntime(dir, "netbird", time.Millisecond, func() error { t.Error("stopped after close"); return nil }, nil)
	monitor.Close()
	storageFile(t, dir, "large", runtimeMaxBytes+1, 0)
}
