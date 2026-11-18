package nbserver

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestConfigServesOnePortWithExternalSTUNAndVerifiedTLS(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "netbird-server")
	p := paths{dir}
	k, e := newSecrets("netbird.example.com")
	if e != nil {
		t.Fatal(e)
	}
	raw, e := renderConfig(p, state{Hostname: "netbird.example.com", Metrics: 40001, Health: 40002}, k)
	if e != nil {
		t.Fatal(e)
	}
	config := string(raw)
	for _, want := range []string{
		"  listenAddress: \":33073\"\n",
		"  exposedAddress: \"https://netbird.example.com:443\"\n",
		"  metricsPort: 40001\n",
		"  healthcheckAddress: \"127.0.0.1:40002\"\n",
		"  dataDir: \"" + dir + "/data\"\n",
		"  stuns:\n    - uri: \"stun:stun.cloudflare.com:3478\"\n",
		"    certFile: \"" + dir + "/tls/cert.pem\"\n    keyFile: \"" + dir + "/tls/key.pem\"\n",
		"    trustedHTTPProxies:\n      - \"127.0.0.1/32\"\n",
		"    issuer: \"https://netbird.example.com/oauth2\"\n",
		"  store:\n    engine: \"sqlite\"\n    encryptionKey: \"" + k.StoreKey + "\"\n",
		"  disableGeoliteUpdate: true\n",
		"  disableAnonymousMetrics: true\n",
	} {
		if !strings.Contains(config, want) {
			t.Fatalf("missing %q in\n%s", want, config)
		}
	}
	// No local STUN port: Cloudflare cannot carry its UDP.
	if strings.Contains(config, "stunPorts") || strings.Contains(config, k.AdminPassword) || strings.Contains(config, "owner") {
		t.Fatal(config)
	}
	other, _ := newSecrets("netbird.example.com")
	if other.StoreKey == k.StoreKey || other.AdminPassword == k.AdminPassword || len(k.AdminPassword) < 24 || k.AdminEmail != "admin@netbird.example.com" {
		t.Fatal("secrets are not fresh")
	}
	for _, bad := range []state{
		{Hostname: "netbird.example.com\"\n  x: 1", Metrics: 40001, Health: 40002},
		{Hostname: "Netbird.example.com", Metrics: 40001, Health: 40002},
		{Hostname: "netbird.example.com", Metrics: 80, Health: 40002},
		{Hostname: "netbird.example.com", Metrics: 40001},
	} {
		if _, e = renderConfig(p, bad, k); e == nil {
			t.Fatal("accepted", bad)
		}
	}
	k.StoreKey = "short"
	if _, e = renderConfig(p, state{Hostname: "netbird.example.com", Metrics: 40001, Health: 40002}, k); e == nil {
		t.Fatal("accepted a short key")
	}
	if _, e = renderConfig(paths{"relative"}, state{Hostname: "netbird.example.com", Metrics: 40001, Health: 40002}, other); e == nil {
		t.Fatal("accepted a relative directory")
	}
}

func TestOriginCertificateIsPrivateAndOnlyForTheName(t *testing.T) {
	p := paths{t.TempDir()}
	if e := ensureCertificate(p, "netbird.example.com"); e != nil {
		t.Fatal(e)
	}
	raw, _ := os.ReadFile(p.cert())
	block, _ := pem.Decode(raw)
	c, e := x509.ParseCertificate(block.Bytes)
	if e != nil || c.VerifyHostname("netbird.example.com") != nil || c.VerifyHostname("nas.example.com") == nil || len(c.IPAddresses) != 0 {
		t.Fatal("certificate names", e)
	}
	pool := x509.NewCertPool()
	pool.AddCert(c)
	if _, e = c.Verify(x509.VerifyOptions{Roots: pool, DNSName: "netbird.example.com"}); e != nil {
		t.Fatal("cloudflared cannot pin it", e)
	}
	if info, _ := os.Stat(p.key()); info.Mode().Perm() != 0600 {
		t.Fatal("key not private")
	}
	// The same name keeps the certificate; another name replaces it.
	if e = ensureCertificate(p, "netbird.example.com"); e != nil {
		t.Fatal(e)
	}
	if again, _ := os.ReadFile(p.cert()); string(again) != string(raw) {
		t.Fatal("certificate replaced without cause")
	}
	ensureCertificate(p, "vpn.example.com")
	if again, _ := os.ReadFile(p.cert()); string(again) == string(raw) {
		t.Fatal("certificate kept for another name")
	}
}

func grpcServer(t *testing.T, h http.HandlerFunc, http2 bool) *httptest.Server {
	s := httptest.NewUnstartedServer(h)
	s.EnableHTTP2 = http2
	s.StartTLS()
	t.Cleanup(s.Close)
	return s
}

// answerServerKey answers the first client call the way NetBird does.
func answerServerKey(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/grpc")
	w.Header().Set("Trailer", "Grpc-Status")
	w.Write([]byte{0, 0, 0, 0, 0})
	w.Header().Set("Grpc-Status", "0")
}

// signalRegisters answers the Signal stream at once, while the request is
// still open, as NetBird's Signal does.
func signalRegisters(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-Wiretrustee-Peer-Id") == "" {
		w.WriteHeader(400)
		return
	}
	w.Header().Set("Content-Type", "application/grpc")
	w.Header().Set("X-Wiretrustee-Peer-Registered", "1")
	w.WriteHeader(200)
	w.(http.Flusher).Flush()
	io.Copy(io.Discard, r.Body)
}

// netbirdLike serves the two calls; signal stands in for what the path does
// to the stream.
func netbirdLike(signal http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.Header.Get("Content-Type") != "application/grpc" {
			w.WriteHeader(404)
			return
		}
		switch r.URL.Path {
		case "/management.ManagementService/GetServerKey":
			answerServerKey(w)
		case "/signalexchange.SignalExchange/ConnectStream":
			signal(w, r)
		default:
			w.WriteHeader(404)
		}
	}
}

func TestGRPCCheckNeedsAGRPCAnswerOverHTTP2AndAnOpenSignalStream(t *testing.T) {
	grpcOK := netbirdLike(signalRegisters)
	cases := []struct {
		name  string
		h     http.HandlerFunc
		http2 bool
		want  string
	}{
		{"grpc", grpcOK, true, ""},
		{"http1", grpcOK, false, "netbird_server_grpc_blocked"},
		{"forbidden", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(403) }, true, "netbird_server_grpc_blocked"},
		{"page", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("<html>")) }, true, "netbird_server_grpc_blocked"},
		{"grpc error", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/grpc")
			w.Header().Set("Grpc-Status", "14")
		}, true, "netbird_server_grpc_blocked"},
		{"no tunnel", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(530) }, true, "netbird_server_unreachable"},
		// What a Cloudflare Tunnel public hostname does: single calls pass,
		// the stream's answer waits until the request ends.
		{"signal held", netbirdLike(func(w http.ResponseWriter, r *http.Request) {
			io.Copy(io.Discard, r.Body)
			signalRegisters(w, r)
		}), true, "netbird_server_signal_blocked"},
		{"signal without registration", netbirdLike(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/grpc")
			w.WriteHeader(200)
		}), true, "netbird_server_signal_blocked"},
		{"signal refused", netbirdLike(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(403) }), true, "netbird_server_signal_blocked"},
		{"signal not routed yet", netbirdLike(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(530) }), true, "netbird_server_unreachable"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := grpcServer(t, c.h, c.http2)
			transport := s.Client().Transport.(*http.Transport).Clone()
			transport.ForceAttemptHTTP2 = c.http2
			if !c.http2 {
				transport.TLSClientConfig.NextProtos = []string{"http/1.1"}
				transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
			}
			start := time.Now()
			e := GRPCCheck{Transport: transport, SignalWait: 500 * time.Millisecond}.Check(context.Background(), s.URL)
			if got := code(e); (e == nil) != (c.want == "") || e != nil && got != c.want {
				t.Fatal(e)
			}
			if time.Since(start) > 5*time.Second {
				t.Fatal("the check outlived its wait")
			}
		})
	}
	if e := (GRPCCheck{Transport: http.DefaultTransport}).Check(context.Background(), "https://127.0.0.1:1"); code(e) != "netbird_server_unreachable" {
		t.Fatal(e)
	}
}
