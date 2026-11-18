package provider

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func inspectTailscaleStatus(t *testing.T, status string) TailscaleSnapshot {
	t.Helper()
	f := &commandFixture{status: []byte(status)}
	s, e := (TailscaleExec{Dir: t.TempDir(), Binaries: fakeBinaries{}, Commands: f}).Open(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Stop()
	o, e := s.Inspect(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	return o
}

func TestTailscaleExecCountsOnlinePeersFromDaemonStatus(t *testing.T) {
	o := inspectTailscaleStatus(t, `{"BackendState":"Running","TailscaleIPs":["100.64.0.1"],"Peer":{"nodekey:a":{"Online":true},"nodekey:b":{"Online":false},"nodekey:c":{"Online":true}}}`)
	if o.PeersOnline == nil || *o.PeersOnline != 2 {
		t.Fatal("online peers not counted", o.PeersOnline)
	}
	o = inspectTailscaleStatus(t, `{"BackendState":"Running","TailscaleIPs":["100.64.0.1"],"Peer":null}`)
	if o.PeersOnline == nil || *o.PeersOnline != 0 {
		t.Fatal("a reported empty peer list is zero", o.PeersOnline)
	}
	o = inspectTailscaleStatus(t, `{"BackendState":"Running","TailscaleIPs":["100.64.0.1"]}`)
	if o.PeersOnline != nil {
		t.Fatal("missing peer data must not be guessed", *o.PeersOnline)
	}
}

func inspectNetBirdStatus(t *testing.T, status string) NetBirdSnapshot {
	t.Helper()
	c := &nbCommands{commandFixture: commandFixture{status: []byte(status)}}
	s, e := (NetBirdExec{Dir: t.TempDir(), Binaries: fakeBinaries{}, Commands: c}).Open(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	defer s.Stop()
	o, e := s.Inspect(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	return o
}

func TestNetBirdExecReadsConnectedPeersFromDaemonStatus(t *testing.T) {
	ready := `"daemonStatus":"Connected","management":{"connected":true},"signal":{"connected":true},"netbirdIp":"100.88.0.2/16"`
	o := inspectNetBirdStatus(t, `{`+ready+`,"peers":{"total":3,"connected":2}}`)
	if o.PeersOnline == nil || *o.PeersOnline != 2 {
		t.Fatal("connected peers not read", o.PeersOnline)
	}
	o = inspectNetBirdStatus(t, `{`+ready+`}`)
	if o.PeersOnline != nil {
		t.Fatal("missing peer data must not be guessed", *o.PeersOnline)
	}
	o = inspectNetBirdStatus(t, `{`+ready+`,"peers":{"total":3}}`)
	if o.PeersOnline != nil {
		t.Fatal("missing connected count must not be guessed", *o.PeersOnline)
	}
}

func peers(n int) *int { return &n }

func TestConnectedStatusReportsPeersOnlineOnlyWhenObserved(t *testing.T) {
	ts := &tsSession{snapshot: TailscaleSnapshot{BackendState: "Running", IPs: []string{"100.64.0.1"}, DNSName: "nas.fixture.ts.net.", Serving: true, PeersOnline: peers(3)}}
	tp := NewTailscale(t.TempDir(), &tsDriver{session: ts})
	if s, e := tp.Start(context.Background(), tsSettings()); e != nil || s.State != "connected" || s.PeersOnline == nil || *s.PeersOnline != 3 {
		t.Fatal("tailscale peers missing", s, e)
	}
	ts.snapshot.PeersOnline = nil
	raw, _ := json.Marshal(tp.Status())
	if strings.Contains(string(raw), "peersOnline") {
		t.Fatal("unobserved peers must be omitted", string(raw))
	}
	ts.snapshot = TailscaleSnapshot{BackendState: "NeedsLogin", AuthURL: "https://login.tailscale.com/a/fixture", PeersOnline: peers(1)}
	if s := tp.Status(); s.PeersOnline != nil {
		t.Fatal("peers reported before connection", s)
	}

	nb := &nbSession{snapshot: NetBirdSnapshot{DaemonStatus: "Connected", ManagementConnected: true, SignalConnected: true, IP: "100.88.0.2/16", PeersOnline: peers(0)}}
	np := NewNetBird(t.TempDir(), &nbDriver{session: nb})
	s, e := np.Start(context.Background(), nbSettings())
	if e != nil || s.State != "connected" || s.PeersOnline == nil || *s.PeersOnline != 0 {
		t.Fatal("netbird peers missing", s, e)
	}
	raw, _ = json.Marshal(s)
	if !strings.Contains(string(raw), `"peersOnline":0`) {
		t.Fatal("an observed zero must be reported", string(raw))
	}
	nb.snapshot.PeersOnline = nil
	raw, _ = json.Marshal(np.Status())
	if strings.Contains(string(raw), "peersOnline") {
		t.Fatal("unobserved peers must be omitted", string(raw))
	}
}
