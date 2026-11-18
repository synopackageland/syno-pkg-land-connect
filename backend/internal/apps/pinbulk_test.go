package apps

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type shortPinRead struct{ io.Reader }

func (r shortPinRead) Read(b []byte) (int, error) { return r.Reader.Read(b[:min(len(b), 16<<10)]) }

func TestFinitePinBodyBatchesShortReadsAndPreservesErrors(t *testing.T) {
	const size = 2*pinCopyBufferSize + 7
	original := io.NopCloser(shortPinRead{strings.NewReader(strings.Repeat("x", size))})
	res := &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: size, Body: original}
	if err := batchPinBody(res); err != nil {
		t.Fatal(err)
	}
	buf := pinBuffers{}.Get()
	defer pinBuffers{}.Put(buf)
	for _, want := range []int{pinCopyBufferSize, pinCopyBufferSize, 7} {
		n, err := res.Body.Read(buf)
		if err != nil || n != want {
			t.Fatalf("read=%d, want %d: %v", n, want, err)
		}
	}
	if n, err := res.Body.Read(buf); n != 0 || err != io.EOF {
		t.Fatalf("end=%d %v", n, err)
	}
	truncated := &http.Response{StatusCode: 200, Header: make(http.Header), ContentLength: 10, Body: io.NopCloser(strings.NewReader("short"))}
	batchPinBody(truncated)
	if n, err := truncated.Body.Read(buf); n != 5 || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("truncation hidden: %d %v", n, err)
	}
}

func TestStreamingPinBodiesKeepOriginalReader(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		length     int64
		status     int
	}{
		{"unknown-length", "application/octet-stream", -1, 200},
		{"fixed-SSE", "text/event-stream; charset=utf-8", 1024, 200},
		{"upgrade", "application/octet-stream", 1024, 101},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := &http.Response{StatusCode: tc.status, Header: http.Header{"Content-Type": {tc.kind}}, ContentLength: tc.length, Body: io.NopCloser(strings.NewReader("ready"))}
			if err := batchPinBody(res); err != nil {
				t.Fatal(err)
			}
			if _, ok := res.Body.(*pinBulkBody); ok {
				t.Fatal("stream was buffered")
			}
		})
	}
}

func TestStreamingFirstChunkFlushesBeforeProducerFinishes(t *testing.T) {
	for _, kind := range []string{"unknown-length", "fixed-SSE"} {
		t.Run(kind, func(t *testing.T) {
			l := newPinLab(t)
			l.app.Close()
			l.app = &LabServer{Name: "stream", Package: "DemoApp"}
			l.app.SetCert(l.leaf(l.key, approvedNames, true))
			t.Cleanup(l.app.Close)
			release := make(chan struct{})
			defer close(release)
			l.app.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/_spl/ping" {
					io.WriteString(w, `{"package":"DemoApp"}`)
					return
				}
				if kind == "fixed-SSE" {
					w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
					w.Header().Set("Content-Length", "1024")
				} else {
					w.Header().Set("Content-Type", "application/octet-stream")
				}
				io.WriteString(w, "ready\n")
				w.(http.Flusher).Flush()
				select {
				case <-release:
				case <-r.Context().Done():
				}
			})
			var err error
			l.ap, err = l.app.Start("127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			run := startPin(t, l, l.binding(), "tailscale", nil)
			c, err := net.Dial("unix", run.sock)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			c.SetDeadline(time.Now().Add(2 * time.Second))
			fmt.Fprint(c, "GET /stream HTTP/1.1\r\nHost: localhost\r\n\r\n")
			req, _ := http.NewRequest("GET", "http://localhost/stream", nil)
			res, err := http.ReadResponse(bufio.NewReader(c), req)
			if err != nil {
				t.Fatal(err)
			}
			defer res.Body.Close()
			got := make([]byte, 6)
			if _, err = io.ReadFull(res.Body, got); err != nil || string(got) != "ready\n" {
				t.Fatalf("first chunk delayed: %q %v", got, err)
			}
		})
	}
}

func TestPauseClosesPartiallyBufferedBulk(t *testing.T) {
	l := newPinLab(t)
	l.app.Close()
	l.app = &LabServer{Name: "bulk", Package: "DemoApp"}
	l.app.SetCert(l.leaf(l.key, approvedNames, true))
	t.Cleanup(l.app.Close)
	started, stopped := make(chan struct{}), make(chan struct{})
	l.app.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/_spl/ping" {
			io.WriteString(w, `{"package":"DemoApp"}`)
			return
		}
		defer close(stopped)
		w.Header().Set("Content-Length", "262144")
		io.WriteString(w, strings.Repeat("x", 16<<10))
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	})
	var err error
	l.ap, err = l.app.Start("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	run := startPin(t, l, l.binding(), "tailscale", nil)
	done := make(chan struct{})
	go func() { defer close(done); run.get("/bulk", "") }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream did not start")
	}
	if err = run.p.Pause(context.Background(), "protection_changed", false); err != nil {
		t.Fatal(err)
	}
	for _, ch := range []chan struct{}{stopped, done} {
		select {
		case <-ch:
		case <-time.After(2 * time.Second):
			t.Fatal("pause left a bulk connection open")
		}
	}
}

// This test-only unpinned relay isolates the cost of the additional Unix HTTP
// hop from the security/metadata checks. It forwards only the generated fixture
// and shares the production body-copy and socket settings for a fair comparison.
func newPinRelayControl(t *testing.T, l *pinLab) (*http.Client, string, *pinWireReads) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "pin-relay-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	ln, err := net.Listen("unix", filepath.Join(dir, "control.sock"))
	if err != nil {
		t.Fatal(err)
	}
	up := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12, NextProtos: []string{"http/1.1"}}, ForceAttemptHTTP2: false, TLSHandshakeTimeout: 3 * time.Second, MaxIdleConns: 128, MaxIdleConnsPerHost: 64, IdleConnTimeout: 30 * time.Second}
	target := &url.URL{Scheme: "https", Host: net.JoinHostPort("127.0.0.1", fmt.Sprint(l.ap))}
	rp := &httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) { r.SetURL(target); r.Out.Host = l.binding().Authority }, Transport: up, FlushInterval: 0, BufferPool: pinBuffers{}, ModifyResponse: batchPinBody}
	srv := &http.Server{Handler: rp, ReadHeaderTimeout: 10 * time.Second}
	done := make(chan struct{})
	go func() { srv.Serve(pinRelayListener{ln}); close(done) }()
	reads := &pinWireReads{}
	down := &http.Transport{MaxIdleConns: 128, MaxIdleConnsPerHost: 64, DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		c, err := (&net.Dialer{}).DialContext(ctx, "unix", ln.Addr().String())
		if err != nil {
			return nil, err
		}
		if os.Getenv("PINPROXY_BULK_TRACE_READS") == "1" {
			return &pinMeasuredConn{Conn: c, reads: reads}, nil
		}
		return c, nil
	}}
	t.Cleanup(func() { down.CloseIdleConnections(); srv.Close(); up.CloseIdleConnections(); <-done })
	return &http.Client{Transport: down, Timeout: 2 * time.Minute}, "http://localhost", reads
}

type pinRelayListener struct{ net.Listener }

func (l pinRelayListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err == nil {
		tunePinSocket(c)
	}
	return c, err
}

func tunePinSocket(c net.Conn) {
	if unix, ok := c.(*net.UnixConn); ok {
		_ = unix.SetReadBuffer(256 << 10)
		_ = unix.SetWriteBuffer(256 << 10)
	}
}
