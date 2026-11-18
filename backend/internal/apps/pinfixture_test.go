package apps

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// LabCA issues test-only certificates. Its key lives in memory only.
type LabCA struct {
	Cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func NewLabCA(name string) (*LabCA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	t := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: name, Organization: []string{"PinProxy lab (test only)"}},
		NotBefore: time.Now().Add(-30 * 24 * time.Hour), NotAfter: time.Now().Add(365 * 24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	der, err := x509.CreateCertificate(rand.Reader, t, t, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	c, _ := x509.ParseCertificate(der)
	return &LabCA{Cert: c, key: key}, nil
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	return n
}

func NewKey() *ecdsa.PrivateKey {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	return k
}

// Leaf issues a server certificate for names with key, valid in [from, to].
// ca == nil makes it self-signed.
func Leaf(ca *LabCA, key *ecdsa.PrivateKey, names []string, from, to time.Time) tls.Certificate {
	t := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: names[0]}, DNSNames: names,
		NotBefore: from, NotAfter: to, KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	parent, signer := t, any(key)
	if ca != nil {
		parent, signer = ca.Cert, ca.key
	}
	der, err := x509.CreateCertificate(rand.Reader, t, parent, &key.PublicKey, signer)
	if err != nil {
		panic(err)
	}
	leaf, _ := x509.ParseCertificate(der)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}
}

// PEM encodes a certificate pair (leaf key included) for a lab server.
func PEM(c tls.Certificate) (certPEM, keyPEM []byte) {
	for _, d := range c.Certificate {
		certPEM = append(certPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: d})...)
	}
	der, _ := x509.MarshalECPrivateKey(c.PrivateKey.(*ecdsa.PrivateKey))
	return certPEM, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})
}

// LabServer is a TLS listener that records, per connection, whether the
// handshake completed and how many application bytes it received.
type LabServer struct {
	Handler    http.Handler
	Hellos     atomic.Int64
	PingStatus atomic.Int64
	SNI        atomic.Bool
	Resumed    atomic.Bool
	Name       string
	Package    string // answers /_spl/ping as this package; "" = 404
	Other      string // optional: /other is proxied here (boundary record)

	cert  atomic.Pointer[tls.Certificate]
	ln    net.Listener
	srv   *http.Server
	mu    sync.Mutex
	Conns []ConnRecord
	live  map[*countConn]bool
}

type ConnRecord struct {
	Server    string `json:"server"`
	Handshake bool   `json:"handshake"`
	Error     string `json:"error,omitempty"`
	AppBytes  int64  `json:"app_bytes"`
	FirstLine string `json:"first_line,omitempty"`
	// UserRequest: the bytes carried the test's user-request marker header.
	UserRequest bool `json:"user_request"`
}

// UserMarker is the header every test user request carries.
const UserMarker = "X-PinProxy-Marker"

func (s *LabServer) SetCert(c tls.Certificate) { s.cert.Store(&c) }

// Records lists closed connections and a snapshot of open ones.
func (s *LabServer) Records() []ConnRecord {
	s.mu.Lock()
	out := append([]ConnRecord(nil), s.Conns...)
	live := make([]*countConn, 0, len(s.live))
	for c := range s.live {
		live = append(live, c)
	}
	s.mu.Unlock()
	for _, c := range live {
		out = append(out, c.record())
	}
	return out
}

func (s *LabServer) Reset() { s.mu.Lock(); s.Conns, s.live = nil, map[*countConn]bool{}; s.mu.Unlock() }

// AppBytes sums application bytes received by all connections so far.
func (s *LabServer) AppBytes() int64 {
	var n int64
	for _, r := range s.Records() {
		n += r.AppBytes
	}
	return n
}

type countConn struct {
	*tls.Conn
	s     *LabServer
	n     atomic.Int64
	first []byte
	once  sync.Once
	mu    sync.Mutex
}

func (c *countConn) Read(b []byte) (int, error) {
	n, err := c.Conn.Read(b)
	if n > 0 {
		c.n.Add(int64(n))
		c.mu.Lock()
		if len(c.first) < 8192 {
			c.first = append(c.first, b[:min(n, 8192-len(c.first))]...)
		}
		c.mu.Unlock()
	}
	return n, err
}

func (c *countConn) record() ConnRecord {
	c.mu.Lock()
	line := string(c.first)
	c.mu.Unlock()
	user := strings.Contains(strings.ToLower(line), strings.ToLower(UserMarker))
	if i := indexCRLF(line); i >= 0 {
		line = line[:i]
	}
	return ConnRecord{Server: c.s.Name, Handshake: true, AppBytes: c.n.Load(), FirstLine: line, UserRequest: user}
}

func (c *countConn) Close() error {
	c.once.Do(func() {
		r := c.record()
		c.s.mu.Lock()
		if c.s.live[c] {
			delete(c.s.live, c)
			c.s.Conns = append(c.s.Conns, r)
		}
		c.s.mu.Unlock()
	})
	return c.Conn.Close()
}

func indexCRLF(s string) int {
	for i := 0; i+1 < len(s); i++ {
		if s[i] == '\r' && s[i+1] == '\n' {
			return i
		}
	}
	return -1
}

type labListener struct {
	net.Listener
	s *LabServer
}

func (l labListener) Accept() (net.Conn, error) {
	for {
		raw, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		c := tls.Server(raw, &tls.Config{GetCertificate: func(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			l.s.Hellos.Add(1)
			if hello.ServerName != "" {
				l.s.SNI.Store(true)
			}
			return l.s.cert.Load(), nil
		}, NextProtos: []string{"http/1.1"}})
		raw.SetDeadline(time.Now().Add(5 * time.Second))
		if err := c.Handshake(); err != nil {
			// A refused handshake: count any bytes the peer still managed to send.
			l.s.mu.Lock()
			l.s.Conns = append(l.s.Conns, ConnRecord{Server: l.s.Name, Handshake: false, Error: err.Error()})
			l.s.mu.Unlock()
			raw.Close()
			continue
		}
		raw.SetDeadline(time.Time{})
		if c.ConnectionState().DidResume {
			l.s.Resumed.Store(true)
		}
		cc := &countConn{Conn: c, s: l.s}
		l.s.mu.Lock()
		if l.s.live == nil {
			l.s.live = map[*countConn]bool{}
		}
		l.s.live[cc] = true
		l.s.mu.Unlock()
		return cc, nil
	}
}

// Start listens on addr ("127.0.0.1:0" in tests, a fixed port on the NAS).
func (s *LabServer) Start(addr string) (int, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return 0, err
	}
	s.ln = ln
	mux := http.NewServeMux()
	mux.HandleFunc("/_spl/ping", func(w http.ResponseWriter, r *http.Request) {
		if code := s.PingStatus.Load(); code != 0 && code != 200 {
			w.WriteHeader(int(code))
			return
		}
		if s.Package == "" {
			http.NotFound(w, r)
			return
		}
		body, _ := json.Marshal(map[string]string{"package": s.Package})
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	})
	if s.Other != "" {
		u, _ := url.Parse(s.Other)
		mux.Handle("/other/", httputil.NewSingleHostReverseProxy(u))
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"served_by": s.Name, "host": r.Host, "path": r.URL.Path,
			"forwarded": r.Header.Values("Forwarded"), "x_forwarded_host": r.Header.Values("X-Forwarded-Host"), "x_forwarded_proto": r.Header.Values("X-Forwarded-Proto"), "x_forwarded_for": r.Header.Values("X-Forwarded-For"), "marker": r.Header.Get("X-PinProxy-Marker")})
	})
	handler := http.Handler(mux)
	if s.Handler != nil {
		handler = s.Handler
	}
	s.srv = &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	go s.srv.Serve(labListener{ln, s})
	return ln.Addr().(*net.TCPAddr).Port, nil
}

func (s *LabServer) Close() {
	if s.srv != nil {
		s.srv.Close()
	}
}
