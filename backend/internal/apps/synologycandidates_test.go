package apps

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestSynologyCandidatesNoExtraApps(t *testing.T) {
	d := Discovery{PackagesDir: t.TempDir(), DetectPorts: fixturePorts, Listening: func() []Listener { return nil }}
	base := d.CustomCandidates(context.Background(), nil, nil)
	if len(base) != 1 || base[0].Name != "DSM" || base[0].URL != "https://127.0.0.1:5001" {
		t.Fatalf("DSM missing: %+v", base)
	}
	portals := []PortalEntry{
		{ID: "SYNO.SDS.App.FileStation3.Instance", Name: "File Station", Additional: portalDefaults("file")},
		{ID: "SYNO.SDS.SynologyPhotos.Application", Name: "Synology Photos", Additional: portalDefaults("photo")},
	}
	got := WithSynologyPortals(base, portals)
	if len(got) != 3 {
		t.Fatalf("shared DSM port lost apps: %+v", got)
	}
	for i, path := range []string{"/file/", "/photo/"} {
		if got[i+1].URL != "https://127.0.0.1:5001"+path {
			t.Fatal(got)
		}
	}
}

func TestSynologyCandidatesCustomPortsAliasAndDedupe(t *testing.T) {
	base := []Candidate{{Name: "DSM", URL: "https://127.0.0.1:5051", Source: "synology"}, {Name: "Duplicate", URL: "https://127.0.0.1:7443", Source: "service"}, {URL: "http://127.0.0.1:9000", Source: "listening"}}
	portals := []PortalEntry{
		{ID: "photos", Name: "Photos", Alias: "pictures", Additional: portalDefaults("photo")},
		{ID: "drive", Name: "Drive", HTTPSPort: 7443, HTTPPort: 7080, Additional: portalDefaults("drive")},
		{ID: "audio", Name: "Audio Station", HTTPPort: 7081},
		{ID: "disabled", Name: ""},
	}
	got := WithSynologyPortals(base, portals)
	urls := []string{}
	for _, c := range got {
		urls = append(urls, c.URL)
	}
	want := []string{"https://127.0.0.1:5051", "http://127.0.0.1:7081", "https://127.0.0.1:7443", "https://127.0.0.1:5051/pictures/", "http://127.0.0.1:9000"}
	if !reflect.DeepEqual(urls, want) {
		t.Fatalf("got %v want %v", urls, want)
	}
	if got[2].Name != "Drive" {
		t.Fatal("portal must beat a generic port definition", got)
	}
}

func portalDefaults(alias string) struct {
	DefaultSetting PortalDefaults `json:"default_setting"`
} {
	return struct {
		DefaultSetting PortalDefaults `json:"default_setting"`
	}{PortalDefaults{Alias: alias}}
}

func TestSynologyPortalWireDefaultsAndSafeIcons(t *testing.T) {
	var entries []PortalEntry
	if err := json.Unmarshal([]byte(`[{"id":"photo","display_name":"Photos","icon":"https://outside.example/track.png","additional":{"default_setting":{"alias":"photo","https_port":1234}}},{"id":"file","display_name":"File Station","alias":"../webman"}]`), &entries); err != nil {
		t.Fatal(err)
	}
	base := []Candidate{{Name: "DSM", URL: "https://127.0.0.1:5001", Source: "synology"}}
	got := WithSynologyPortals(base, entries)
	if len(got) != 2 || got[1].URL != "https://127.0.0.1:5001/photo/" || got[1].Icon != "" {
		t.Fatal(got)
	}
	// A native alias shared with an SDK App must retain the SDK's choose flow.
	base = append(base, Candidate{Package: "Example", Name: "Example", URL: got[1].URL, Source: "package"})
	got = WithSynologyPortals(base, entries)
	if len(got) != 2 || got[1].Package != "Example" {
		t.Fatal(got)
	}
}
