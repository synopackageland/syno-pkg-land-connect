package lanswitch

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"dsmapp/internal/ipctest"
	"dsmapp/internal/phonelink"
	"dsmapp/internal/ports"
)

// request builds a Binding request the way a browser does: USERNAME,
// PRIORITY, ICE-CONTROLLING, MESSAGE-INTEGRITY, FINGERPRINT.
func request(t *testing.T, username, pwd string, fingerprinted bool) []byte {
	t.Helper()
	msg := make([]byte, headerLen)
	binary.BigEndian.PutUint16(msg[0:2], bindingRequest)
	binary.BigEndian.PutUint32(msg[4:8], magicCookie)
	copy(msg[8:20], "txid-0123456")
	user := []byte(username)
	for len(user)%4 != 0 {
		user = append(user, 0)
	}
	msg = appendAttr(msg, attrUsername, user)
	// appendAttr pads nothing; fix the USERNAME length to its unpadded size.
	binary.BigEndian.PutUint16(msg[headerLen+2:headerLen+4], uint16(len(username)))
	msg = appendAttr(msg, 0x0024, []byte{0x6e, 0x7f, 0x1e, 0xff})
	msg = appendAttr(msg, 0x802a, []byte{1, 2, 3, 4, 5, 6, 7, 8})
	msg = appendAttr(msg, attrIntegrity, integrity(msg, []byte(pwd)))
	if fingerprinted {
		var fp [4]byte
		binary.BigEndian.PutUint32(fp[:], fingerprint(msg, len(msg)+8))
		msg = appendAttr(msg, attrFingerprint, fp[:])
	}
	return msg
}

// checkResponse verifies a success response the way a browser does.
func checkResponse(t *testing.T, out []byte, pwd string, peer netip.AddrPort) {
	t.Helper()
	if len(out) < headerLen || binary.BigEndian.Uint16(out[0:2]) != bindingSuccess || string(out[8:20]) != "txid-0123456" {
		t.Fatalf("not a success response for the request: %x", out)
	}
	if int(binary.BigEndian.Uint16(out[2:4])) != len(out)-headerLen {
		t.Fatal("length field does not match")
	}
	// XOR-MAPPED-ADDRESS, MESSAGE-INTEGRITY, FINGERPRINT in that order.
	xa := out[headerLen+4 : headerLen+4+8]
	if binary.BigEndian.Uint16(out[headerLen:]) != attrXorMapped || xa[1] != 1 {
		t.Fatal("no IPv4 XOR-MAPPED-ADDRESS first")
	}
	gotPort := binary.BigEndian.Uint16(xa[2:4]) ^ uint16(magicCookie>>16)
	var ip [4]byte
	binary.BigEndian.PutUint32(ip[:], binary.BigEndian.Uint32(xa[4:8])^magicCookie)
	if netip.AddrPortFrom(netip.AddrFrom4(ip), gotPort) != peer {
		t.Fatalf("mapped address %v, want %v", netip.AddrPortFrom(netip.AddrFrom4(ip), gotPort), peer)
	}
	mi := headerLen + 12
	if binary.BigEndian.Uint16(out[mi:]) != attrIntegrity {
		t.Fatal("no MESSAGE-INTEGRITY")
	}
	pre := append([]byte(nil), out[:mi]...)
	binary.BigEndian.PutUint16(pre[2:4], uint16(mi-headerLen+24))
	m := hmac.New(sha1.New, []byte(pwd))
	m.Write(pre)
	if !hmac.Equal(m.Sum(nil), out[mi+4:mi+24]) {
		t.Fatal("MESSAGE-INTEGRITY does not verify")
	}
	fp := mi + 24
	if binary.BigEndian.Uint16(out[fp:]) != attrFingerprint || fp+8 != len(out) {
		t.Fatal("FINGERPRINT is not last")
	}
	if binary.BigEndian.Uint32(out[fp+4:]) != crc32.ChecksumIEEE(out[:fp])^fingerprintXor {
		t.Fatal("FINGERPRINT does not verify")
	}
}

var peer = netip.MustParseAddrPort("192.168.1.20:53000")

func TestAnswerOnlySignedRequestsForLiveCredentials(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	r := &Responder{Now: func() time.Time { return now }}
	ufrag, pwd, err := r.Issue()
	if err != nil || len(ufrag) != ufragLen || len(pwd) != pwdLen {
		t.Fatal(ufrag, pwd, err)
	}
	for _, fingerprinted := range []bool{true, false} {
		out := r.Answer(request(t, ufrag+":abcd", pwd, fingerprinted), peer)
		checkResponse(t, out, pwd, peer)
	}
	cases := map[string][]byte{
		"wrong password":        request(t, ufrag+":abcd", pwd+"x", true),
		"unknown ufrag":         request(t, "ZZZZZZZZ:abcd", pwd, true),
		"username without peer": request(t, ufrag, pwd, true),
		"empty":                 {},
		"header only":           request(t, ufrag+":abcd", pwd, true)[:headerLen],
	}
	bad := request(t, ufrag+":abcd", pwd, true)
	bad[len(bad)-1] ^= 1
	cases["broken fingerprint"] = bad
	resp := request(t, ufrag+":abcd", pwd, true)
	binary.BigEndian.PutUint16(resp[0:2], bindingSuccess)
	cases["not a request"] = resp
	for name, pkt := range cases {
		if out := r.Answer(pkt, peer); out != nil {
			t.Errorf("%s: answered", name)
		}
	}
	now = now.Add(sessionTTL)
	if r.Answer(request(t, ufrag+":abcd", pwd, true), peer) != nil {
		t.Fatal("expired credentials still answer")
	}
}

func TestIssueKeepsABoundedSetOfCredentials(t *testing.T) {
	r := &Responder{}
	first, pwd, _ := r.Issue()
	for range maxSessions {
		if _, _, err := r.Issue(); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.sessions) != maxSessions || len(r.order) != maxSessions {
		t.Fatal(len(r.sessions), len(r.order))
	}
	if r.Answer(request(t, first+":abcd", pwd, true), peer) != nil {
		t.Fatal("the oldest credential was not dropped")
	}
}

func TestParseRejectsTruncatedAttributes(t *testing.T) {
	r := &Responder{}
	ufrag, pwd, _ := r.Issue()
	pkt := request(t, ufrag+":abcd", pwd, true)
	for n := headerLen + 1; n < len(pkt); n++ {
		if n == len(pkt)-8 {
			continue // without FINGERPRINT the request is still whole
		}
		cut := append([]byte(nil), pkt[:n]...)
		binary.BigEndian.PutUint16(cut[2:4], uint16(n-headerLen))
		if r.Answer(cut, peer) != nil {
			t.Fatalf("answered a request cut at %d", n)
		}
	}
}

func loopbackListen(t *testing.T) func(context.Context) (net.Listener, error) {
	return func(context.Context) (net.Listener, error) { return net.Listen("tcp4", "127.0.0.1:0") }
}

func frame(b []byte) []byte {
	out := make([]byte, 2+len(b))
	binary.BigEndian.PutUint16(out, uint16(len(b)))
	copy(out[2:], b)
	return out
}

func TestResponderAnswersFramedChecksOverTCP(t *testing.T) {
	r := &Responder{Listen: loopbackListen(t)}
	port, err := r.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Stop()
	if again, _ := r.Start(); again != port {
		t.Fatal("Start reopened the listener")
	}
	ufrag, pwd, _ := r.Issue()
	c, err := net.Dial("tcp4", net.JoinHostPort("127.0.0.1", itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	// A wrong check gets no reply; the next good one on the same connection does.
	if _, err := c.Write(append(frame(request(t, ufrag+":abcd", "wrong", true)), frame(request(t, ufrag+":abcd", pwd, true))...)); err != nil {
		t.Fatal(err)
	}
	var head [2]byte
	if _, err := io.ReadFull(c, head[:]); err != nil {
		t.Fatal(err)
	}
	out := make([]byte, binary.BigEndian.Uint16(head[:]))
	if _, err := io.ReadFull(c, out); err != nil {
		t.Fatal(err)
	}
	local := c.LocalAddr().(*net.TCPAddr).AddrPort()
	checkResponse(t, out, pwd, netip.AddrPortFrom(local.Addr().Unmap(), local.Port()))
	// An oversized frame closes the connection.
	if _, err := c.Write([]byte{0xff, 0xff}); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Read(head[:]); err == nil {
		t.Fatal("connection stayed open after an oversized frame")
	}
}

func TestStopClosesOpenChecksAndForgetsCredentials(t *testing.T) {
	r := &Responder{Listen: loopbackListen(t)}
	port, err := r.Start()
	if err != nil {
		t.Fatal(err)
	}
	ufrag, pwd, _ := r.Issue()
	c, err := net.Dial("tcp4", net.JoinHostPort("127.0.0.1", itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	deadline := time.Now().Add(2 * time.Second)
	for {
		r.mu.Lock()
		n := len(r.open)
		r.mu.Unlock()
		if n == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	done := make(chan struct{})
	go func() { r.Stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Stop waited for an open check")
	}
	if r.Port() != 0 || r.Answer(request(t, ufrag+":abcd", pwd, true), peer) != nil {
		t.Fatal("still listening or answering after Stop")
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

type fakeDSM struct {
	p        ports.DSMPorts
	redirect bool
	err      error
}

func (f fakeDSM) read(context.Context) (ports.DSMPorts, bool, error) { return f.p, f.redirect, f.err }

func service(t *testing.T, dsm fakeDSM, down map[string]bool) *Service {
	t.Helper()
	return &Service{
		Path: filepath.Join(t.TempDir(), "lanswitch.json"),
		DSM:  dsm.read,
		Interfaces: func() ([]phonelink.Interface, error) {
			return []phonelink.Interface{
				{Name: "eth0", Addrs: []netip.Addr{netip.MustParseAddr("192.168.1.10"), netip.MustParseAddr("fd00::10"), netip.MustParseAddr("fe80::1")}},
				{Name: "eth1", Addrs: []netip.Addr{netip.MustParseAddr("10.0.0.5"), netip.MustParseAddr("203.0.113.9")}},
				{Name: "docker0", Addrs: []netip.Addr{netip.MustParseAddr("172.17.0.1")}},
				{Name: "tun0", Addrs: []netip.Addr{netip.MustParseAddr("100.64.0.2")}},
			}, nil
		},
		Dial: func(_ context.Context, a netip.AddrPort) error {
			if down[a.Addr().String()] {
				return io.EOF
			}
			return nil
		},
		Responder: &Responder{Listen: loopbackListen(t)},
	}
}

func TestProbeIsEmptyWhileTheOptionIsOff(t *testing.T) {
	s := service(t, fakeDSM{p: ports.DSMPorts{HTTP: 5000, HTTPS: 5001}}, nil)
	s.Restore()
	defer s.Close()
	p, err := s.Probe(context.Background())
	if err != nil || p.Enabled || p.Ufrag != "" || len(p.Candidates) != 0 {
		t.Fatal(p, err)
	}
	if s.Responder.Port() != 0 {
		t.Fatal("the responder listens while the option is off")
	}
}

func TestProbeFollowsDSMProtocolAndOffersReachableLANAddresses(t *testing.T) {
	for _, tc := range []struct {
		redirect bool
		want     []string
	}{
		{false, []string{"http://192.168.1.10:5000", "http://10.0.0.5:5000"}},
		{true, []string{"https://192.168.1.10:5001", "https://10.0.0.5:5001"}},
	} {
		s := service(t, fakeDSM{p: ports.DSMPorts{HTTP: 5000, HTTPS: 5001}, redirect: tc.redirect}, nil)
		if _, err := s.Set(true); err != nil {
			t.Fatal(err)
		}
		p, err := s.Probe(context.Background())
		s.Close()
		if err != nil || !p.Enabled || p.HTTPS != tc.redirect || p.Ufrag == "" || p.Pwd == "" || len(p.Candidates) != len(tc.want) {
			t.Fatalf("%+v %v", p, err)
		}
		for i, c := range p.Candidates {
			if c.URL != tc.want[i] || c.Port == 0 || c.Address != netip.MustParseAddrPort(c.URL[len("https://"):]).Addr().String() && c.Address != netip.MustParseAddrPort(c.URL[len("http://"):]).Addr().String() {
				t.Fatalf("candidate %d: %+v", i, c)
			}
		}
	}
}

func TestProbeLeavesOutAddressesWhereDSMDoesNotAnswer(t *testing.T) {
	s := service(t, fakeDSM{p: ports.DSMPorts{HTTP: 5000, HTTPS: 5001}}, map[string]bool{"192.168.1.10": true})
	defer s.Close()
	if _, err := s.Set(true); err != nil {
		t.Fatal(err)
	}
	p, _ := s.Probe(context.Background())
	if len(p.Candidates) != 1 || p.Candidates[0].Address != "10.0.0.5" {
		t.Fatalf("%+v", p)
	}
	s = service(t, fakeDSM{p: ports.DSMPorts{HTTP: 5000, HTTPS: 5001}}, map[string]bool{"192.168.1.10": true, "10.0.0.5": true})
	defer s.Close()
	_, _ = s.Set(true)
	p, _ = s.Probe(context.Background())
	if !p.Enabled || len(p.Candidates) != 0 || p.Ufrag != "" {
		t.Fatalf("credentials issued with no candidate: %+v", p)
	}
}

func TestProbeOffersNothingWithoutDSMPorts(t *testing.T) {
	s := service(t, fakeDSM{err: ports.ErrDetection}, nil)
	defer s.Close()
	_, _ = s.Set(true)
	p, err := s.Probe(context.Background())
	if err != nil || !p.Enabled || len(p.Candidates) != 0 || p.Ufrag != "" {
		t.Fatal(p, err)
	}
}

func TestSetPersistsPrivatelyAndRestoreStartsTheResponder(t *testing.T) {
	s := service(t, fakeDSM{p: ports.DSMPorts{HTTP: 5000, HTTPS: 5001}}, nil)
	st, err := s.Set(true)
	if err != nil || !st.Enabled || st.Port == 0 || len(st.Addresses) != 2 {
		t.Fatal(st, err)
	}
	info, err := os.Stat(s.Path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("settings file is not private", err)
	}
	s.Close()
	again := &Service{Path: s.Path, Interfaces: s.Interfaces, Responder: &Responder{Listen: loopbackListen(t)}}
	again.Restore()
	if again.Responder.Port() == 0 || !again.Status().Enabled {
		t.Fatal("restore did not start the responder")
	}
	if st, err := again.Set(false); err != nil || st.Enabled || st.Port != 0 || again.Responder.Port() != 0 {
		t.Fatal("turning the option off left the responder running", st, err)
	}
	again.Close()
	third := &Service{Path: s.Path, Responder: &Responder{Listen: loopbackListen(t)}}
	third.Restore()
	defer third.Close()
	if third.Responder.Port() != 0 || third.Status().Enabled {
		t.Fatal("an off option restored as on")
	}
}

func TestHandlerServesStatusSetAndProbe(t *testing.T) {
	s := service(t, fakeDSM{p: ports.DSMPorts{HTTP: 5000, HTTPS: 5001}}, nil)
	defer s.Close()
	call := func(method, action, body string) reply {
		t.Helper()
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest(method, "/lanswitch/"+action, strings.NewReader(body)))
		var out reply
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if out := call("POST", "status", "{}"); out.Status == nil || out.Status.Enabled {
		t.Fatalf("%+v", out)
	}
	if out := call("POST", "set", `{"enabled":true}`); out.Status == nil || !out.Status.Enabled || out.Status.Port == 0 {
		t.Fatalf("%+v", out)
	}
	if out := call("POST", "probe", "{}"); out.Probe == nil || len(out.Probe.Candidates) != 2 || out.Probe.Pwd == "" {
		t.Fatalf("%+v", out)
	}
	for _, bad := range [][3]string{{"GET", "status", "{}"}, {"POST", "status", `{"x":1}`}, {"POST", "nope", "{}"}, {"POST", "set", "{} {}"}} {
		if out := call(bad[0], bad[1], bad[2]); out.Error == "" || out.Status != nil || out.Probe != nil {
			t.Fatalf("%v accepted: %+v", bad, out)
		}
	}
}

func TestControlRoundTrip(t *testing.T) {
	s := service(t, fakeDSM{p: ports.DSMPorts{HTTP: 5000, HTTPS: 5001}}, nil)
	defer s.Close()
	sock := ipctest.Path(t, filepath.Join(t.TempDir(), "c.sock"))
	ln, err := ipctest.Listen(t, sock)
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(sock)
	go func() { _ = http.Serve(ln, s.Handler()) }()
	defer ln.Close()
	c := Client{Socket: sock}
	ctx := context.Background()
	if st, err := c.Status(ctx); err != nil || st.Enabled {
		t.Fatal(st, err)
	}
	if st, err := c.Set(ctx, true); err != nil || !st.Enabled || st.Port == 0 {
		t.Fatal(st, err)
	}
	p, err := c.Probe(ctx)
	if err != nil || !p.Enabled || len(p.Candidates) != 2 || p.Pwd == "" {
		t.Fatal(p, err)
	}
	if st, err := c.Set(ctx, false); err != nil || st.Enabled {
		t.Fatal(st, err)
	}
}
