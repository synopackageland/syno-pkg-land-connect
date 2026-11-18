package apps

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestIPv6AndAlternateBindTakeover(t *testing.T) {
	for _, addr := range []string{"127.0.0.1", "0.0.0.0", "127.0.0.2", "::1"} {
		t.Run(addr, func(t *testing.T) {
			l := newPinLab(t)
			attacker := &LabServer{Name: "takeover", Package: "DemoApp"}
			attacker.SetCert(l.leaf(NewKey(), approvedNames, true))
			port, err := attacker.Start(net.JoinHostPort(addr, "0"))
			if err != nil {
				if addr == "::1" || addr == "127.0.0.2" {
					t.Skipf("loopback alias unavailable: %v", err)
				}
				t.Fatal(err)
			}
			defer attacker.Close()
			b := l.binding()
			b.Port = port
			run := startPin(t, l, b, "tailscale", func(c *PinConfig) {
				o := l.opts
				o.Host = addr
				if addr == "0.0.0.0" {
					o.Host = "127.0.0.1"
				}
				// The reference server remains on IPv4: use S with the explicitly approved
				// original key, proving pin rejection on each dial/bind address independently.
				c.Binding.Mode = ModeS
				c.Binding.SPKI = SPKI(l.leaf(l.key, approvedNames, true).Leaf)
				c.Binding.Names = nil
				c.Verifier = NewVerifier(o)
			})
			res, err := run.get("/", "")
			if err != nil || res.code != http.StatusServiceUnavailable || attacker.AppBytes() != 0 {
				t.Fatalf("takeover: %+v %v bytes=%d", res, err, attacker.AppBytes())
			}
			l.clk.Add(120 * time.Second)
			run.p.Check(context.Background(), goodObservation(b))
			reason, _, n := run.s.snapshot()
			if reason != "cert_mismatch" || n != 1 {
				t.Fatalf("%s %d", reason, n)
			}
		})
	}
}

func TestUnknownDiscoveryWaitsAndRecovers(t *testing.T) {
	l := newPinLab(t)
	b := l.binding()
	r := startPin(t, l, b, "tailscale", nil)
	o := goodObservation(b)
	o.UnknownPorts = true
	if err := r.p.Check(context.Background(), o); err == nil {
		t.Fatal("unknown scan treated as success")
	}
	if res, err := r.get("/", ""); err != nil || res.code != http.StatusServiceUnavailable {
		t.Fatalf("%+v %v", res, err)
	}
	o.UnknownPorts = false
	r.p.Check(context.Background(), o)
	if res, err := r.get("/", ""); err != nil || res.code != 200 {
		t.Fatalf("did not recover %+v %v", res, err)
	}
}

func TestRegistryStopsExactScope(t *testing.T) {
	l := newPinLab(t)
	b := l.binding()
	private := startPin(t, l, b, "tailscale", nil)
	b.Authority = "public.example.com"
	public := startPin(t, l, b, "cloudflared", nil)
	reg := &PinRegistry{Observe: func(_ context.Context, b PinBinding) (PinObservation, error) { return goodObservation(b), nil }}
	reg.Set("private", private.p)
	reg.Set("public", public.p)
	if err := reg.Check(context.Background()); err != nil {
		t.Fatal(err)
	}
	reg.Stop("private")
	if private.p.active() {
		t.Fatal("private proxy still active")
	}
	if res, err := public.get("/", "public.example.com"); err != nil || res.code != 200 {
		t.Fatalf("public scope affected: %+v %v", res, err)
	}
	reg.Close()
	if public.p.active() {
		t.Fatal("close left proxy active")
	}
}

func TestDSMDoesNotPingAndPreservesOrigin(t *testing.T) {
	l := newPinLab(t)
	l.app.Package = "" // would give 404 if the DSM target pinged
	b := l.binding()
	b.Target = TargetDSM
	run := startPin(t, l, b, "tailscale", nil)
	if res, err := run.get("/authorize.cgi", ""); err != nil || res.code != 200 {
		t.Fatalf("DSM target: %+v %v", res, err)
	}
	if l.app.SNI.Load() {
		t.Fatal("DSM sent SNI")
	}
}

// The local fixture includes a fixed response latency to represent an upstream
// service; this is a concurrency/pool sanity check, not a NAS throughput claim.
func TestFiftyConcurrentThroughput(t *testing.T) {
	l := newPinLab(t)
	l.app.Close()
	l.app = &LabServer{Name: "throughput", Package: "DemoApp"}
	l.app.SetCert(l.leaf(l.key, approvedNames, true))
	t.Cleanup(l.app.Close)
	payload := strings.Repeat("payload ", 16*1024) // 128 KiB
	var served atomic.Int64
	l.app.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_spl/ping" {
			io.WriteString(w, `{"package":"DemoApp"}`)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
		time.Sleep(5 * time.Millisecond)
		served.Add(1)
		io.WriteString(w, payload)
	})
	var err error
	l.ap, err = l.app.Start("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	run := startPin(t, l, l.binding(), "tailscale", nil)
	directTR := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, MaxIdleConnsPerHost: 64, MaxIdleConns: 128}
	defer directTR.CloseIdleConnections()
	direct := &http.Client{Transport: directTR, Timeout: 5 * time.Second}
	exercise := func(c *http.Client, url string) time.Duration {
		t.Helper()
		var wg sync.WaitGroup
		ready := make(chan struct{})
		fail := make(chan error, 50)
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-ready
				for j := 0; j < 10; j++ {
					res, err := c.Get(url)
					if err != nil {
						fail <- err
						return
					}
					n, err := io.Copy(io.Discard, res.Body)
					res.Body.Close()
					if err != nil || res.StatusCode != 200 || n != int64(len(payload)) {
						fail <- fmt.Errorf("response %d bytes=%d: %v", res.StatusCode, n, err)
						return
					}
				}
			}()
		}
		start := time.Now()
		close(ready)
		wg.Wait()
		elapsed := time.Since(start)
		close(fail)
		for err := range fail {
			t.Error(err)
		}
		return elapsed
	}
	directURL := fmt.Sprintf("https://127.0.0.1:%d/", l.ap)
	// Establish the verified reference and first upstream connection before
	// measuring pool concurrency. Otherwise 50 simultaneous cold W-mode
	// handshakes also probe DSM, testing cold-start TLS fan-out under -race
	// instead of this fixture's documented warm-pool throughput. Check every
	// response, including this warm-up; keep all 4,000 concurrent assertions.
	res, err := run.client.Get("http://localhost/")
	if err != nil {
		t.Fatal(err)
	}
	n, readErr := io.Copy(io.Discard, res.Body)
	res.Body.Close()
	if readErr != nil || res.StatusCode != 200 || n != int64(len(payload)) {
		t.Fatalf("verified warm-up: response %d bytes=%d: %v", res.StatusCode, n, readErr)
	}
	// Warm both pools, then alternate order to reduce scheduling bias.
	exercise(direct, directURL)
	exercise(run.client, "http://localhost/")
	var d, p time.Duration
	for i := 0; i < 3; i++ {
		if i%2 == 0 {
			d += exercise(direct, directURL)
			p += exercise(run.client, "http://localhost/")
		} else {
			p += exercise(run.client, "http://localhost/")
			d += exercise(direct, directURL)
		}
	}
	ratio := float64(d) / float64(p)
	t.Logf("50 workers x 10 fixed-length requests x 128 KiB, upstream latency 5ms: direct=%s proxy=%s throughput ratio=%.3f served=%d", d, p, ratio, served.Load())
	if run.p.tr.MaxIdleConnsPerHost < 50 {
		t.Fatal("pool smaller than concurrency")
	}
	// Timing ratios depend on the runner's CPU and load. The device/controlled
	// performance gate opts into the floor; the normal gate verifies all 4,000
	// responses and pool reuse without a flaky wall-clock assertion.
	if floor := os.Getenv("PINPROXY_MIN_THROUGHPUT"); floor != "" {
		want, err := strconv.ParseFloat(floor, 64)
		if err != nil {
			t.Fatal(err)
		}
		if ratio < want {
			t.Fatalf("proxy/direct throughput %.3f below %.3f", ratio, want)
		}
	}
}

func TestApprovalPingTimeoutAndRoutingBoundary(t *testing.T) {
	l := newPinLab(t)
	// Correct ping cannot promise ownership of every path on the listener.
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "other service") }))
	defer other.Close()
	l.app.Close()
	l.app = &LabServer{Name: "multiplexed", Package: "DemoApp", Other: other.URL}
	l.app.SetCert(l.leaf(l.key, approvedNames, true))
	t.Cleanup(l.app.Close)
	var err error
	l.ap, err = l.app.Start("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	run := startPin(t, l, l.binding(), "tailscale", nil)
	if res, err := run.get("/other/", ""); err != nil || res.body != "other service" {
		t.Fatalf("boundary: %+v %v", res, err)
	}
}

func TestStoppedTargetAndPingTimeoutRemainWaiting(t *testing.T) {
	t.Run("listener-not-ready", func(t *testing.T) {
		l := newPinLab(t)
		b := l.binding()
		run := startPin(t, l, b, "tailscale", nil)
		l.app.Close()
		if res, err := run.get("/", ""); err != nil || res.code != http.StatusServiceUnavailable {
			t.Fatalf("stopped target: %+v %v", res, err)
		}
		if _, _, n := run.s.snapshot(); n != 0 {
			t.Fatal("stopped target paused")
		}
		if _, err := l.app.Start(net.JoinHostPort("127.0.0.1", fmt.Sprint(b.Port))); err != nil {
			t.Fatal(err)
		}
		if res, err := run.get("/", ""); err != nil || res.code != 200 {
			t.Fatalf("restart: %+v %v", res, err)
		}
	})
	t.Run("ping-timeout", func(t *testing.T) {
		l := newPinLab(t)
		l.app.Close()
		l.app = &LabServer{Name: "ping-timeout", Package: "DemoApp"}
		l.app.SetCert(l.leaf(l.key, approvedNames, true))
		l.app.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() })
		var err error
		l.ap, err = l.app.Start("127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(l.app.Close)
		run := startPin(t, l, l.binding(), "tailscale", func(c *PinConfig) {
			o := l.opts
			o.HandshakeTimeout = 100 * time.Millisecond
			c.Verifier = NewVerifier(o)
		})
		start := time.Now()
		res, err := run.get("/", "")
		if err != nil || res.code != http.StatusServiceUnavailable || time.Since(start) > time.Second {
			t.Fatalf("ping timeout: %+v %v", res, err)
		}
		if _, _, n := run.s.snapshot(); n != 0 {
			t.Fatal("ping timeout paused")
		}
		if _, err := run.p.cfg.Verifier.Propose(context.Background(), TargetApp, l.ap, "DemoApp", run.p.cfg.Binding.Authority); ReasonOf(err) != ReasonPingMissing {
			t.Fatal(err)
		}
	})
}

func TestDSMRefusesOtherCertificateNameBeforeApplicationBytes(t *testing.T) {
	l := newPinLab(t)
	b := l.binding()
	b.Target = TargetDSM
	l.app.SetCert(l.leaf(NewKey(), []string{"attacker.example.com"}, true))
	run := startPin(t, l, b, "tailscale", nil)
	if res, err := run.get("/authorize.cgi", ""); err != nil || res.code != http.StatusServiceUnavailable {
		t.Fatalf("DSM name takeover: %+v %v", res, err)
	}
	if l.app.AppBytes() != 0 {
		t.Fatal("DSM takeover received application bytes")
	}
}

func TestMissingDiscoveryEvidenceDoesNotResume(t *testing.T) {
	for _, which := range []string{"dsm-port", "own-port", "scan"} {
		t.Run(which, func(t *testing.T) {
			l := newPinLab(t)
			b := l.binding()
			run := startPin(t, l, b, "tailscale", nil)
			o := goodObservation(b)
			switch which {
			case "dsm-port":
				o.DSMPort = 0
			case "own-port":
				o.Ports = nil
			case "scan":
				o.UnknownPorts = true
			}
			if err := run.p.Check(context.Background(), o); err == nil {
				t.Fatal("missing evidence passed")
			}
			if res, err := run.get("/", ""); err != nil || res.code != http.StatusServiceUnavailable {
				t.Fatalf("missing evidence: %+v %v", res, err)
			}
			if _, _, n := run.s.snapshot(); n != 0 {
				t.Fatal("unknown evidence persisted a pause")
			}
		})
	}
}

func TestConfirmedAuthorityMustBeCanonical(t *testing.T) {
	l := newPinLab(t)
	run := startPin(t, l, l.binding(), "tailscale", nil)
	for _, authority := range []string{"localhost:443", "nas.example.com", "NAS.example.com:443", "nas.example.com.:443"} {
		cfg := run.p.cfg
		cfg.Binding.Authority = authority
		if _, err := NewPinProxy(cfg); err == nil {
			t.Fatalf("noncanonical private authority %q accepted", authority)
		}
	}
}

func TestOldListenerCannotUnlinkReplacement(t *testing.T) {
	lab := newPinLab(t)
	run := startPin(t, lab, lab.binding(), "tailscale", nil)
	if err := os.Remove(run.sock); err != nil {
		t.Fatal(err)
	}
	second, err := StartPinProxy(context.Background(), run.p.cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Stop()
	run.p.Stop()
	c, err := net.Dial("unix", run.sock)
	if err != nil {
		t.Fatalf("old server removed replacement: %v", err)
	}
	c.Close()
}

// The device baseline measures 64 MiB responses at 50 workers. Keep this
// controlled bulk run separate from the small-response fast-gate sanity check.
func TestBulkFiftyConcurrentThroughput(t *testing.T) {
	if mode := os.Getenv("PINPROXY_BULK_CLIENT"); mode != "" && mode != "provider" {
		t.Fatalf("unknown bulk client %q", mode)
	}
	if os.Getenv("PINPROXY_BULK_PERFORMANCE") != "1" {
		t.Skip("controlled bulk performance run")
	}
	l := newPinLab(t)
	l.app.Close()
	l.app = &LabServer{Name: "bulk", Package: "DemoApp"}
	l.app.SetCert(l.leaf(l.key, approvedNames, true))
	t.Cleanup(l.app.Close)
	const size = 64 << 20
	chunk := make([]byte, 64<<10)
	l.app.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_spl/ping" {
			io.WriteString(w, `{"package":"DemoApp"}`)
			return
		}
		n := size
		if r.URL.Path == "/warm" {
			n = 1
		}
		w.Header().Set("Content-Length", strconv.Itoa(n))
		for left := n; left > 0; {
			k := min(left, len(chunk))
			if _, err := w.Write(chunk[:k]); err != nil {
				return
			}
			left -= k
		}
	})
	var err error
	l.ap, err = l.app.Start("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	run := startPin(t, l, l.binding(), "tailscale", nil)
	var directReads, proxyReads pinWireReads
	directTR := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, MaxIdleConnsPerHost: 64, MaxIdleConns: 128}
	defer directTR.CloseIdleConnections()
	traceReads := os.Getenv("PINPROXY_BULK_TRACE_READS") == "1"
	if traceReads {
		directTR.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			c, err := (&net.Dialer{}).DialContext(ctx, network, address)
			if err != nil {
				return nil, err
			}
			return &pinMeasuredConn{Conn: c, reads: &directReads}, nil
		}
		proxyTR := run.client.Transport.(*http.Transport)
		proxyDial := proxyTR.DialContext
		proxyTR.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			c, err := proxyDial(ctx, network, address)
			if err != nil {
				return nil, err
			}
			return &pinMeasuredConn{Conn: c, reads: &proxyReads}, nil
		}
	}
	direct := &http.Client{Transport: directTR, Timeout: 2 * time.Minute}
	run.client.Timeout = 2 * time.Minute
	transfer := func(c *http.Client, url string, want int64) time.Duration {
		var wg sync.WaitGroup
		ready := make(chan struct{})
		fail := make(chan error, 50)
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-ready
				res, err := c.Get(url)
				if err != nil {
					fail <- err
					return
				}
				var n int64
				if os.Getenv("PINPROXY_BULK_CLIENT") == "provider" {
					// Match httputil.ReverseProxy's default 32 KiB body copy. Wrapping the
					// writer suppresses io.Discard.ReadFrom, which ignores CopyBuffer's buffer
					// and otherwise drains plaintext Unix sockets in 8 KiB reads.
					n, err = io.CopyBuffer(pinWriteOnly{io.Discard}, res.Body, make([]byte, 32<<10))
				} else {
					n, err = io.Copy(io.Discard, res.Body)
				}
				res.Body.Close()
				if err != nil || res.StatusCode != 200 || n != want {
					fail <- fmt.Errorf("response %d bytes=%d: %v", res.StatusCode, n, err)
				}
			}()
		}
		start := time.Now()
		close(ready)
		wg.Wait()
		elapsed := time.Since(start)
		close(fail)
		for err := range fail {
			t.Error(err)
		}
		return elapsed
	}
	origin := fmt.Sprintf("https://127.0.0.1:%d", l.ap)
	transfer(direct, origin+"/warm", 1)
	transfer(run.client, "http://localhost/warm", 1)

	var relayClient *http.Client
	var relayURL string
	var relayReads *pinWireReads
	if os.Getenv("PINPROXY_BULK_COMPARE") == "relay" {
		relayClient, relayURL, relayReads = newPinRelayControl(t, l)
		transfer(relayClient, relayURL+"/warm", 1)
	}
	directReads.reset()
	proxyReads.reset()
	if relayReads != nil {
		relayReads.reset()
	}
	d := transfer(direct, origin+"/blob", size)
	var p, relay time.Duration
	if relayClient != nil && pinBulkRunSequence.Add(1)%2 == 0 {
		relay = transfer(relayClient, relayURL+"/blob", size)
		p = transfer(run.client, "http://localhost/blob", size)
	} else {
		p = transfer(run.client, "http://localhost/blob", size)
		if relayClient != nil {
			relay = transfer(relayClient, relayURL+"/blob", size)
		}
	}
	if relayClient != nil {
		t.Logf("same-topology relay=%s pin=%s pin/relay throughput ratio=%.3f", relay, p, float64(relay)/float64(p))
	}

	ratio := float64(d) / float64(p)
	if traceReads {
		t.Logf("client=%q direct TLS net.Conn.Read calls=%d bytes=%d; proxy Unix net.Conn.Read calls=%d bytes=%d", os.Getenv("PINPROXY_BULK_CLIENT"), directReads.calls.Load(), directReads.bytes.Load(), proxyReads.calls.Load(), proxyReads.bytes.Load())
		if relayReads != nil {
			t.Logf("relay Unix net.Conn.Read calls=%d bytes=%d", relayReads.calls.Load(), relayReads.bytes.Load())
		}
	}
	t.Logf("50 workers x 64 MiB fixed-length responses: direct=%s proxy=%s throughput ratio=%.3f", d, p, ratio)
	if ratio < 0.84 {
		t.Fatalf("bulk proxy/direct throughput %.3f below 0.840", ratio)
	}
}

// Instrument the controlled client without modifying the production transport.
type pinWireReads struct{ calls, bytes atomic.Int64 }

func (r *pinWireReads) reset() { r.calls.Store(0); r.bytes.Store(0) }

type pinMeasuredConn struct {
	net.Conn
	reads *pinWireReads
}

func (c *pinMeasuredConn) Read(p []byte) (int, error) {
	c.reads.calls.Add(1)
	n, err := c.Conn.Read(p)
	c.reads.bytes.Add(int64(n))
	return n, err
}

type pinWriteOnly struct{ io.Writer }

var pinBulkRunSequence atomic.Uint64
