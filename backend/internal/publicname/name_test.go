package publicname

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestReadableLabels(t *testing.T) {
	for input, want := range map[string]string{"My-NAS": "my-nas", "  Home_NAS.example  ": "home-nas-example", "---": "dsm", "儲存伺服器": "dsm", "": "dsm", "Rack 2": "rack-2"} {
		if got := NASLabel(input); got != want {
			t.Errorf("%q: %q, want %q", input, got, want)
		}
	}
	for input, want := range map[string]string{"SynoPkgJellyfin": "jellyfin", "Gopeed": "gopeed", "SynoPkg": "synopkg", "": "dsm", "custom.plex": "plex", "custom.home-assistant-2": "home-assistant-2", "custom.app": "app"} {
		if got := ShortName(input); got != want {
			t.Errorf("%q: %q", input, got)
		}
	}
}

func TestCollisionLadderHasNoInstallCode(t *testing.T) {
	var seen []string
	got, err := Choose(context.Background(), "jellyfin", "Home-NAS", func(name string) (bool, error) {
		seen = append(seen, name)
		return len(seen) == 4, nil
	})
	want := []string{"jellyfin", "jellyfin-home-nas", "jellyfin-home-nas-2", "jellyfin-home-nas-3"}
	if err != nil || got != want[3] || !reflect.DeepEqual(seen, want) {
		t.Fatalf("%q %v %v", got, seen, err)
	}
	for _, attempt := range []int{0, 1, 2, 999} {
		name := Candidate(strings.Repeat("a", 64), strings.Repeat("b", 64), attempt)
		if !LabelPattern.MatchString(name) || len(name) > 63 {
			t.Fatalf("invalid label %q", name)
		}
	}
}

func TestChooseStopsOnErrorsAndCancellation(t *testing.T) {
	failure := errors.New("offline")
	calls := 0
	_, err := Choose(context.Background(), "app", "nas", func(string) (bool, error) { calls++; return false, failure })
	if !errors.Is(err, failure) || calls != 1 {
		t.Fatal(err, calls)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = Choose(ctx, "app", "nas", func(string) (bool, error) { t.Fatal("lookup after cancel"); return true, nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
