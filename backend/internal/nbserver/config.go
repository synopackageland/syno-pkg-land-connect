package nbserver

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"dsmapp/internal/connector"
)

// ManagementPort is the server's one listener for its API, gRPC, relay and
// sign-in. NetBird's legacy compatibility listener uses this same port, so
// choosing it keeps the server to one main listener.
const ManagementPort = 33073

// externalSTUN stands in for the server's own STUN, which needs a UDP port
// Cloudflare cannot carry. Peers still punch holes for direct connections.
const externalSTUN = "stun:stun.cloudflare.com:3478"

var hostnameRE = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

// state is the server's restart intent and progress. It holds no secret.
type state struct {
	Enabled  bool   `json:"enabled"`
	Hostname string `json:"hostname,omitempty"`
	Metrics  int    `json:"metricsPort,omitempty"`
	Health   int    `json:"healthPort,omitempty"`
	Joined   bool   `json:"joined,omitempty"`
	// Routed is set while Cloudflare has a record for Hostname.
	Routed bool `json:"routed,omitempty"`
	// Bridged is set from before the Signal bridge is first made until it
	// is removed.
	Bridged bool `json:"bridged,omitempty"`
}

// secrets are generated once and kept in a private file beside the data.
type secrets struct {
	AuthSecret    string `json:"authSecret"`
	CookieKey     string `json:"cookieKey"`
	StoreKey      string `json:"storeKey"`
	AdminEmail    string `json:"adminEmail,omitempty"`
	AdminPassword string `json:"adminPassword,omitempty"`
	Token         string `json:"token,omitempty"`
	// The owner and the token's own ID and expiry, for renewing it.
	UserID       string    `json:"userId,omitempty"`
	TokenID      string    `json:"tokenId,omitempty"`
	TokenExpires time.Time `json:"tokenExpires,omitzero"`
}

type paths struct{ dir string }

func (p paths) state() string   { return filepath.Join(p.dir, "state.json") }
func (p paths) secrets() string { return filepath.Join(p.dir, "secrets.json") }
func (p paths) config() string  { return filepath.Join(p.dir, "config.yaml") }
func (p paths) cert() string    { return filepath.Join(p.dir, "tls", "cert.pem") }
func (p paths) key() string     { return filepath.Join(p.dir, "tls", "key.pem") }
func (p paths) data() string    { return filepath.Join(p.dir, "data") }

func readJSON(path string, v any) error {
	raw, e := connector.ReadPrivate(path)
	if e != nil {
		return e
	}
	return json.Unmarshal(raw, v)
}
func writeJSON(path string, v any) error {
	raw, _ := json.Marshal(v)
	return connector.WritePrivate(path, raw)
}

func randomKey(n int) (string, error) {
	b := make([]byte, n)
	if _, e := rand.Read(b); e != nil {
		return "", e
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// newSecrets makes the server's keys and the owner's sign-in. The owner's
// address is under the server's own name, so it names no real mailbox.
func newSecrets(hostname string) (secrets, error) {
	var s secrets
	var e error
	for _, k := range []*string{&s.AuthSecret, &s.CookieKey, &s.StoreKey} {
		if *k, e = randomKey(32); e != nil {
			return s, e
		}
	}
	b := make([]byte, 18)
	if _, e = rand.Read(b); e != nil {
		return s, e
	}
	s.AdminPassword = base64.RawURLEncoding.EncodeToString(b)
	s.AdminEmail = "admin@" + hostname
	return s, nil
}

// ensureCertificate makes the private origin certificate cloudflared pins.
// It is its own issuer, valid for the server's name only.
func ensureCertificate(p paths, hostname string) error {
	if raw, e := os.ReadFile(p.cert()); e == nil {
		if block, _ := pem.Decode(raw); block != nil {
			if c, e := x509.ParseCertificate(block.Bytes); e == nil && c.VerifyHostname(hostname) == nil && time.Until(c.NotAfter) > 30*24*time.Hour {
				if _, e = os.Stat(p.key()); e == nil {
					return nil
				}
			}
		}
	}
	if e := os.MkdirAll(filepath.Dir(p.cert()), 0700); e != nil {
		return e
	}
	key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if e != nil {
		return e
	}
	serial := make([]byte, 16)
	if _, e = rand.Read(serial); e != nil {
		return e
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          new(big.Int).SetBytes(serial),
		Subject:               pkix.Name{CommonName: hostname},
		DNSNames:              []string{hostname},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, e := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if e != nil {
		return e
	}
	keyDER, e := x509.MarshalECPrivateKey(key)
	if e != nil {
		return e
	}
	if e = connector.WritePrivate(p.key(), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})); e != nil {
		return e
	}
	return connector.WritePrivate(p.cert(), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

var errConfig = errors.New("invalid server configuration")

// renderConfig is the server's whole configuration, written again before
// every start so it always matches this version of Connect.
func renderConfig(p paths, s state, k secrets) ([]byte, error) {
	if !hostnameRE.MatchString(s.Hostname) || s.Metrics < 1024 || s.Metrics > 65535 || s.Health < 1024 || s.Health > 65535 {
		return nil, errConfig
	}
	for _, v := range []string{k.AuthSecret, k.CookieKey, k.StoreKey} {
		if raw, e := base64.StdEncoding.DecodeString(v); e != nil || len(raw) != 32 {
			return nil, errConfig
		}
	}
	for _, v := range []string{p.dir} {
		if !filepath.IsAbs(v) || filepath.Clean(v) != v || strings.ContainsAny(v, "\"\\\n") {
			return nil, errConfig
		}
	}
	origin := "https://" + s.Hostname
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }
	w("server:\n")
	w("  listenAddress: %q\n", fmt.Sprintf(":%d", ManagementPort))
	w("  exposedAddress: %q\n", origin+":443")
	w("  metricsPort: %d\n", s.Metrics)
	w("  healthcheckAddress: %q\n", fmt.Sprintf("127.0.0.1:%d", s.Health))
	w("  logLevel: \"warn\"\n")
	w("  logFile: \"console\"\n")
	w("  authSecret: %q\n", k.AuthSecret)
	w("  dataDir: %q\n", p.data())
	w("  disableGeoliteUpdate: true\n")
	w("  disableAnonymousMetrics: true\n")
	w("  stuns:\n    - uri: %q\n", externalSTUN)
	w("  tls:\n    certFile: %q\n    keyFile: %q\n", p.cert(), p.key())
	w("  reverseProxy:\n    trustedHTTPProxies:\n      - \"127.0.0.1/32\"\n")
	w("  auth:\n")
	w("    issuer: %q\n", origin+"/oauth2")
	w("    signKeyRefreshEnabled: true\n")
	w("    sessionCookieEncryptionKey: %q\n", k.CookieKey)
	w("    dashboardRedirectURIs:\n      - %q\n      - %q\n", origin+"/nb-auth", origin+"/nb-silent-auth")
	w("    cliRedirectURIs:\n      - \"http://localhost:53000/\"\n")
	w("  store:\n    engine: \"sqlite\"\n    encryptionKey: %q\n", k.StoreKey)
	return []byte(b.String()), nil
}

// serverEnv is the whole environment: no inherited variable reaches the server.
func serverEnv(dir string) []string {
	return []string{
		"PATH=/usr/bin:/bin",
		"HOME=" + dir,
		// Geolocation downloads a database at start and stops the server when
		// the download fails; peers' locations are not used by Connect.
		"NB_DISABLE_GEOLOCATION=true",
		// Lets the one-time owner setup return an API token, so Connect can
		// make this NAS's setup key without a browser sign-in.
		"NB_SETUP_PAT_ENABLED=true",
	}
}

// DisableOffline turns an enabled server off in its saved state without
// starting anything, for an uninstall: a reinstall then starts with the
// server off. The name, the bridge and the data stay recorded, so turning it
// off from the page can still withdraw what is left.
func DisableOffline(dir string) error {
	p := paths{dir}
	raw, e := connector.ReadPrivate(p.state())
	if errors.Is(e, os.ErrNotExist) {
		return nil
	}
	if e != nil {
		return errSave
	}
	// An unreadable state already reads as off.
	var st state
	if json.Unmarshal(raw, &st) != nil {
		return nil
	}
	if !st.Enabled {
		return nil
	}
	st.Enabled = false
	if writeJSON(p.state(), st) != nil {
		return errSave
	}
	return nil
}
