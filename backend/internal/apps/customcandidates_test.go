package apps

import (
	"context"
	"net/netip"
	"reflect"
	"strconv"
	"testing"

	"github.com/synopackageland/syno-pkg-land-sdk/dsmservices"
)

// DSM's port definitions become pick-list entries: web-looking ones first,
// HTTPS ones with an https URL, one per single TCP port. DSM's own ports,
// excluded ports and ports that never serve a web page stay out.
func TestCustomCandidatesFromDSMDefinitions(t *testing.T) {
	d := Discovery{PackagesDir: t.TempDir(), DetectPorts: func(context.Context) (int, int, error) { return 5050, 5051, nil },
		Listening: func() []Listener {
			return []Listener{{Addr: netip.MustParseAddr("0.0.0.0"), Port: 445}, {Addr: netip.MustParseAddr("0.0.0.0"), Port: 22},
				{Addr: netip.MustParseAddr("::"), Port: 5432}, {Addr: netip.MustParseAddr("127.0.0.1"), Port: 9000},
				{Addr: netip.MustParseAddr("203.0.113.7"), Port: 9001}, {Addr: netip.MustParseAddr("::"), Port: 7001}}
		}}
	defs := []dsmservices.Definition{
		{ID: "dms", File: "synow3.sc", Title: "HTTP", TitleKey: "firewall:firewall_service_opt_dsm_http", Desc: "DSM", Ports: []int{5050}},
		{ID: "dms_https", File: "synow3.sc", Title: "HTTPS", Desc: "DSM", Ports: []int{5051}},
		{ID: "SYNO.SDS.App.FileStation3.Instance_https", File: "synow3.sc", Title: "HTTPS", Desc: "File Station", Ports: []int{7001}},
		{ID: "cloudstation", File: "SynologyDrive.sc", Title: "Synology Drive Server", Ports: []int{6690}},
		{ID: "contacts_radicale", File: "Contacts.sc", Title: "Radicale", Desc: "Contacts", Ports: []int{5555}},
		{ID: "media_tcp", File: "docker_media.sc", Title: "Docker(TCP)", Desc: "Docker media", Ports: []int{8920, 18096}},
		{ID: "own", File: "own.sc", Title: "Own", Ports: []int{4443}},
		{ID: "ntp", File: "syno_ntp.sc", Title: "NTP"},
		{ID: "cifs", File: "SMBService.sc", Title: "cifs", Ports: []int{139, 445}},
	}
	got := d.CustomCandidates(context.Background(), defs, map[int]bool{4443: true})
	label := func(def dsmservices.Definition) *ServiceLabel { return serviceLabel(def) }
	want := []Candidate{
		{Name: "DSM", URL: "https://127.0.0.1:5051", Source: "synology"},
		{Name: "Docker(TCP)", URL: "http://127.0.0.1:8920", Source: "service", Service: label(defs[5])},
		{Name: "Docker(TCP)", URL: "http://127.0.0.1:18096", Source: "service", Service: label(defs[5])},
		{Name: "HTTPS", URL: "https://127.0.0.1:7001", Source: "service", Service: label(defs[2])},
		{Name: "Radicale", URL: "http://127.0.0.1:5555", Source: "service", Service: label(defs[4])},
		// Listeners: unnamed and last; SSH, SMB and PostgreSQL are never offered.
		{URL: "http://127.0.0.1:9000", Source: "listening"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

// The cap of 64 cuts unnamed listeners, never a named service.
func TestCustomCandidatesCapKeepsNamedServices(t *testing.T) {
	defs := []dsmservices.Definition{}
	for i := 0; i < 10; i++ {
		defs = append(defs, dsmservices.Definition{ID: "web" + strconv.Itoa(i), Title: "Web " + strconv.Itoa(i), Ports: []int{8100 + i}})
	}
	d := Discovery{PackagesDir: t.TempDir(), Listening: func() []Listener {
		out := []Listener{}
		for port := 7000; port < 7100; port++ {
			out = append(out, Listener{Addr: netip.MustParseAddr("0.0.0.0"), Port: port})
		}
		return out
	}}
	got := d.CustomCandidates(context.Background(), defs, nil)
	if len(got) != 64 {
		t.Fatal(len(got))
	}
	for i, c := range got {
		if (i < 10) != (c.Source == "service") {
			t.Fatalf("%d: %+v", i, c)
		}
	}
	if serviceName(dsmservices.Definition{ID: "only_id"}) != "only_id" {
		t.Fatal("a definition without title or desc is named by its section")
	}
}
