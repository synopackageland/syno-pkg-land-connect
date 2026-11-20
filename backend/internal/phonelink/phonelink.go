// Package phonelink lists the NAS's own LAN DSM addresses for the "add to
// phone" QR code. It is read-only: it reads DSM's port settings, the network
// interfaces and the certificate DSM serves on loopback, and it never writes
// state, starts a process or reaches past loopback (apart from one bounded DNS
// lookup of the host name the browser is using). Nothing it returns is secret.
package phonelink

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"dsmapp/internal/ports"
)

// MaxLAN bounds how many LAN addresses go into the QR code.
const MaxLAN = 4

// Address is one DSM origin. Kind is "lan" for an address on the NAS's own
// networks and "other" for the address the browser uses when it is not one.
// SHA256 is the lowercase hex SHA-256 of the TLS leaf certificate DER, set
// only for an https origin whose certificate a phone would not trust.
type Address struct {
	URL    string `json:"url"`
	Kind   string `json:"kind"`
	SHA256 string `json:"sha256,omitempty"`
}

// Result is the reply of phone.addnas. Ports reports whether DSM's port
// settings were read; without them no address is offered.
type Result struct {
	Name      string    `json:"name"`
	Addresses []Address `json:"addresses"`
	Ports     bool      `json:"ports"`
}

// Interface is one network interface's name and addresses.
type Interface struct {
	Name  string
	Addrs []netip.Addr
}

// Source collects the addresses. Every field is injectable for tests; the
// zero value uses the live system.
type Source struct {
	Ports      func(context.Context) (ports.DSMPorts, error)
	Hostname   func() (string, error)
	Interfaces func() ([]Interface, error)
	Lookup     func(context.Context, string) ([]netip.Addr, error)
	// Probe handshakes with DSM on loopback port and returns the chain it serves for serverName ("" sends no SNI).
	Probe func(ctx context.Context, port int, serverName string) ([]*x509.Certificate, error)
	Roots func() *x509.CertPool
	Now   func() time.Time
}

var hostLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// validHostName accepts a DNS name; IP literals are handled separately.
func validHostName(h string) bool {
	if h == "" || len(h) > 253 {
		return false
	}
	for _, l := range strings.Split(h, ".") {
		if !hostLabel.MatchString(l) {
			return false
		}
	}
	return true
}

var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// lanAddr reports whether a is an address a phone on the same network could
// use: private IPv4 or IPv6 ULA. Link-local, loopback and the 100.64.0.0/10
// range used by NetBird and Tailscale are not.
func lanAddr(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsPrivate() && !cgnat.Contains(a)
}

// skippedInterface names virtual interfaces whose addresses a phone on the LAN cannot reach.
func skippedInterface(name string) bool {
	for _, p := range []string{"lo", "docker", "br-", "veth", "tun", "wt", "tailscale", "utun", "sit", "ip6tnl", "lxc", "virbr", "zt"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func (s Source) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// LiveInterfaces lists the system's up, non-loopback interfaces.
func LiveInterfaces() ([]Interface, error) { return liveInterfaces() }

// LANAddresses returns the private IPv4 addresses, and the IPv6 ULA addresses
// when v6 is set, of the interfaces a device on the LAN could reach, in
// interface order and without duplicates.
func LANAddresses(list []Interface, v6 bool) []netip.Addr {
	var out []netip.Addr
	seen := map[netip.Addr]bool{}
	for _, i := range list {
		if skippedInterface(i.Name) {
			continue
		}
		for _, a := range i.Addrs {
			a = a.Unmap()
			if seen[a] || !lanAddr(a) || (a.Is6() && !v6) || a.Zone() != "" {
				continue
			}
			seen[a] = true
			out = append(out, a)
		}
	}
	return out
}

func liveInterfaces() ([]Interface, error) {
	list, err := net.Interfaces()
	if err != nil {
		return nil, err
	}
	var out []Interface
	for _, i := range list {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := i.Addrs()
		if err != nil {
			continue
		}
		v := Interface{Name: i.Name}
		for _, a := range addrs {
			if p, err := netip.ParsePrefix(a.String()); err == nil {
				v.Addrs = append(v.Addrs, p.Addr())
			}
		}
		out = append(out, v)
	}
	return out, nil
}

func liveLookup(ctx context.Context, host string) ([]netip.Addr, error) {
	return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
}

// LoopbackProbe handshakes with 127.0.0.1:port without resumption and sends no application data.
func LoopbackProbe(ctx context.Context, port int, serverName string) ([]*x509.Certificate, error) {
	if port < 1 || port > 65535 {
		return nil, errors.New("invalid port")
	}
	d := &net.Dialer{Timeout: 3 * time.Second}
	raw, err := d.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return nil, err
	}
	defer raw.Close()
	_ = raw.SetDeadline(time.Now().Add(3 * time.Second))
	c := tls.Client(raw, &tls.Config{InsecureSkipVerify: true, ServerName: serverName, NextProtos: []string{"http/1.1"}, MinVersion: tls.VersionTLS12})
	if err := c.HandshakeContext(ctx); err != nil {
		return nil, err
	}
	return c.ConnectionState().PeerCertificates, nil
}

// origin builds scheme://host:port with IPv6 literals bracketed.
func origin(scheme, host string, port int) string {
	return scheme + "://" + net.JoinHostPort(host, strconv.Itoa(port))
}

type candidate struct {
	host string
	kind string
	url  string
	sni  string
	pin  bool // the origin is DSM's own HTTPS port, so its certificate is the probed one
}

// Current is the browser's own location, as the frontend reports it. It is
// only a hint: the reply never contains anything the NAS did not confirm.
type Current struct {
	Protocol string `json:"protocol"`
	Host     string `json:"host"`
	Port     int    `json:"port,omitempty"`
}

// normalizeHost lowercases a host name and strips IPv6 brackets.
func normalizeHost(h string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(h), "["), "]"))
}

// Collect returns DSM's name and the LAN DSM origins: the browser's address
// first when it is on the NAS's network, then the NAS's own LAN interface
// addresses. A browser address that is not on the NAS's network comes back
// as kind "other" with its own scheme and port.
func (s Source) Collect(ctx context.Context, cur Current) Result {
	hostname := s.Hostname
	if hostname == nil {
		hostname = func() (string, error) { return "", errors.New("no hostname") }
	}
	name, _ := hostname()
	out := Result{Name: name, Addresses: []Address{}}
	if s.Ports == nil {
		return out
	}
	p, err := s.Ports(ctx)
	if err != nil || (p.HTTPS < 1 && p.HTTP < 1) || p.HTTPS > 65535 || p.HTTP > 65535 {
		return out
	}
	out.Ports = true
	ifaces := s.Interfaces
	if ifaces == nil {
		ifaces = liveInterfaces
	}
	own := map[netip.Addr]bool{}
	var lan []netip.Addr
	if list, err := ifaces(); err == nil {
		for _, i := range list {
			skip := skippedInterface(i.Name)
			for _, a := range i.Addrs {
				a = a.Unmap()
				own[a] = true
				// IPv4 only from interfaces: an IPv6 ULA is rarely what a phone uses.
				if !skip && a.Is4() && lanAddr(a) {
					lan = append(lan, a)
				}
			}
		}
	}
	var cands []candidate
	seen := map[string]bool{}
	add := func(c candidate) {
		if !seen[c.url] {
			seen[c.url] = true
			cands = append(cands, c)
		}
	}
	lanOrigin := func(host, sni string) candidate {
		if p.HTTPS > 0 {
			return candidate{host: host, kind: "lan", url: origin("https", host, p.HTTPS), sni: sni, pin: true}
		}
		return candidate{host: host, kind: "lan", url: origin("http", host, p.HTTP)}
	}
	host := normalizeHost(cur.Host)
	var other *candidate
	if addr, err := netip.ParseAddr(host); err == nil && host != "" {
		addr = addr.Unmap()
		usable := addr.Zone() == "" && !addr.IsLoopback() && !addr.IsLinkLocalUnicast() && !cgnat.Contains(addr)
		if usable && (lanAddr(addr) || own[addr]) {
			add(lanOrigin(addr.String(), ""))
		} else if usable {
			other = s.otherOrigin(cur, addr.String(), "", p)
		}
	} else if validHostName(host) && host != "localhost" {
		// A single-label or mDNS (.local) name only works on the local network.
		if s.lookup(ctx, host, own) || !strings.Contains(host, ".") || strings.HasSuffix(host, ".local") {
			add(lanOrigin(host, host))
		} else {
			other = s.otherOrigin(cur, host, host, p)
		}
	}
	for _, a := range lan {
		if len(cands) >= MaxLAN {
			break
		}
		add(lanOrigin(a.String(), ""))
	}
	if len(cands) > MaxLAN {
		cands = cands[:MaxLAN]
	}
	if other != nil {
		add(*other)
	}
	// One handshake per SNI value: every IP literal shares the no-SNI certificate.
	probe := s.Probe
	if probe == nil {
		probe = LoopbackProbe
	}
	chains := map[string][]*x509.Certificate{}
	probed := map[string]bool{}
	for _, c := range cands {
		a := Address{URL: c.url, Kind: c.kind}
		if c.pin {
			if !probed[c.sni] {
				probed[c.sni] = true
				if chain, err := probe(ctx, p.HTTPS, c.sni); err == nil && len(chain) > 0 {
					chains[c.sni] = chain
				}
			}
			if chain := chains[c.sni]; chain != nil && !s.trusted(chain, c.host) {
				a.SHA256 = leafSHA256(chain[0])
			}
		}
		out.Addresses = append(out.Addresses, a)
	}
	return out
}

// otherOrigin keeps the browser's own scheme and port for an address off the
// NAS's network (a DDNS name, a port forward). Its certificate is DSM's only
// when it is DSM's HTTPS port.
func (s Source) otherOrigin(cur Current, host, sni string, p ports.DSMPorts) *candidate {
	scheme := strings.TrimSuffix(strings.ToLower(cur.Protocol), ":")
	if scheme != "https" && scheme != "http" {
		return nil
	}
	port := cur.Port
	if port == 0 {
		port = map[string]int{"https": 443, "http": 80}[scheme]
	}
	if port < 1 || port > 65535 {
		return nil
	}
	u := url.URL{Scheme: scheme, Host: net.JoinHostPort(host, strconv.Itoa(port))}
	if (scheme == "https" && port == 443) || (scheme == "http" && port == 80) {
		u.Host = host
		if strings.Contains(host, ":") {
			u.Host = "[" + host + "]"
		}
	}
	return &candidate{host: host, kind: "other", url: u.String(), sni: sni, pin: scheme == "https" && port == p.HTTPS}
}

// lookup resolves host within one second and reports whether it names this NAS on the LAN.
func (s Source) lookup(ctx context.Context, host string, own map[netip.Addr]bool) bool {
	look := s.Lookup
	if look == nil {
		look = liveLookup
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	addrs, err := look(ctx, host)
	if err != nil || len(addrs) == 0 {
		return false
	}
	for _, a := range addrs {
		a = a.Unmap()
		if a.IsLoopback() || cgnat.Contains(a) || !(own[a] || lanAddr(a)) {
			return false
		}
	}
	return true
}

// trusted reports whether a phone with the public root store would accept chain for host now.
func (s Source) trusted(chain []*x509.Certificate, host string) bool {
	roots := (*x509.CertPool)(nil)
	if s.Roots != nil {
		roots = s.Roots()
	}
	if roots == nil {
		return false
	}
	inter := x509.NewCertPool()
	for _, c := range chain[1:] {
		inter.AddCert(c)
	}
	_, err := chain[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: inter, DNSName: host, CurrentTime: s.now(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
	return err == nil
}

func leafSHA256(c *x509.Certificate) string {
	sum := sha256.Sum256(c.Raw)
	return hex.EncodeToString(sum[:])
}
