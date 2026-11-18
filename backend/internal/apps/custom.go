package apps

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"dsmapp/internal/connector"
)

func customCode(code, message string) error { return connector.Coded(code, message) }

// CustomLabel is the readable base of a new custom App's ID and public name.
func CustomLabel(name string) string {
	label := strings.Trim(nonLabelRune.ReplaceAllString(strings.ToLower(name), "-"), "-")
	label = strings.TrimRight(label[:min(len(label), 40)], "-")
	if label == "" {
		return "app"
	}
	return label
}

// NewCustomID picks custom.<label>, then -2, -3, ... among saved IDs.
func NewCustomID(state State, name string) (string, error) {
	base := CustomLabel(name)
	for i := 1; i <= MaxCustom+1024; i++ {
		label := base
		if i > 1 {
			suffix := "-" + strconv.Itoa(i)
			label = strings.TrimRight(base[:min(len(base), 40-len(suffix))], "-") + suffix
		}
		id := CustomPrefix + label
		if _, used := state.Custom[id]; used {
			continue
		}
		if _, used := state.Apps[id]; used {
			continue
		}
		return id, nil
	}
	return "", errCustomLimit
}

// CustomRequest adds (no ID) or edits a custom App. AcceptRisk confirms that
// AppSignIn lets anyone with the link reach the App's own sign-in.
type CustomRequest struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name"`
	URL         string `json:"url"`
	AppSignIn   bool   `json:"appSignIn"`
	InsecureTLS bool   `json:"insecureTls"`
	AcceptRisk  bool   `json:"acceptRisk,omitempty"`
}

type CustomID struct {
	ID string `json:"id"`
}
type CustomResult struct {
	ID string `json:"id"`
}

// Candidate pre-fills the add dialog: a DSM package that declares its web
// port, a service DSM declares in its port definitions or a port something
// listens on. Picking one only fills the form.
type Candidate struct {
	Package string `json:"package,omitempty"`
	Icon    string `json:"icon,omitempty"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Source  string `json:"source"`
	// Service is the DSM port definition the port belongs to, so the page can
	// show DSM's own, translated name for it.
	Service *ServiceLabel `json:"service,omitempty"`
}

// ServiceLabel is one section of a DSM services.d file: its ID is the
// section name, which DSM's SYNO.Core.Service.PortInfo calls port_id, and the
// keys are DSM's "section:key" UI strings when the file names them.
type ServiceLabel struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Desc     string `json:"desc,omitempty"`
	TitleKey string `json:"titleKey,omitempty"`
	DescKey  string `json:"descKey,omitempty"`
}
type Candidates struct {
	Candidates []Candidate `json:"candidates"`
}

// Definition normalizes and checks a request against the saved definition.
func (r CustomRequest) Definition(previous *CustomApp) (CustomApp, error) {
	name := strings.TrimSpace(r.Name)
	if !validCustomName(name) {
		return CustomApp{}, errCustomName
	}
	u, err := ParseTarget(r.URL)
	if err != nil {
		return CustomApp{}, err
	}
	if r.InsecureTLS && u.Scheme != "https" {
		r.InsecureTLS = false
	}
	if r.AppSignIn && !r.AcceptRisk && (previous == nil || !previous.AppSignIn) {
		return CustomApp{}, errCustomRisk
	}
	def := CustomApp{Name: name, URL: u.String(), AppSignIn: r.AppSignIn, InsecureTLS: r.InsecureTLS}
	if previous != nil {
		def.Published = previous.Published
	}
	return def, nil
}

// TargetGuard is the dial-time boundary: it resolves the configured host,
// refuses every refused address and the ports Connect itself listens on,
// and dials only the vetted IP, so a DNS answer cannot change the target
// between the check and the connection.
type TargetGuard struct {
	Resolver *net.Resolver
	// OwnPorts lists the TCP ports this package's processes listen on.
	OwnPorts func() map[int]bool
	// LocalAddrs lists the NAS's own addresses; nil reads the interfaces.
	LocalAddrs func() []netip.Addr
	Timeout    time.Duration
}

func (g TargetGuard) local(ip netip.Addr) bool {
	if ip.IsLoopback() || ip.IsUnspecified() {
		return true
	}
	addrs := g.LocalAddrs
	if addrs == nil {
		addrs = interfaceAddrs
	}
	for _, a := range addrs() {
		if a.Unmap() == ip {
			return true
		}
	}
	return false
}

func interfaceAddrs() []netip.Addr {
	raw, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}
	out := []netip.Addr{}
	for _, a := range raw {
		if p, err := netip.ParsePrefix(a.String()); err == nil {
			out = append(out, p.Addr().Unmap())
		}
	}
	return out
}

// Check resolves host:port and returns the addresses that may be dialed.
func (g TargetGuard) Check(ctx context.Context, address string) ([]netip.AddrPort, error) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return nil, errCustomURL
	}
	port, err := strconv.Atoi(portText)
	if err != nil || !validPort(port) {
		return nil, errCustomURL
	}
	var ips []netip.Addr
	if ip, err := netip.ParseAddr(host); err == nil {
		ips = []netip.Addr{ip.Unmap()}
	} else {
		resolver := g.Resolver
		if resolver == nil {
			resolver = net.DefaultResolver
		}
		found, err := resolver.LookupNetIP(ctx, "ip", host)
		if err != nil || len(found) == 0 {
			return nil, customCode("custom_target_unresolved", "The App's host name does not resolve")
		}
		for _, ip := range found {
			ips = append(ips, ip.Unmap())
		}
	}
	own := map[int]bool{}
	if g.OwnPorts != nil {
		own = g.OwnPorts()
	}
	out := []netip.AddrPort{}
	for _, ip := range ips {
		// One refused answer refuses the name: a mixed answer is not a LAN App.
		if refusedIP(ip) || own[port] && g.local(ip) {
			return nil, errCustomRefused
		}
		out = append(out, netip.AddrPortFrom(ip, uint16(port)))
	}
	return out, nil
}

func (g TargetGuard) DialContext(ctx context.Context, _, address string) (net.Conn, error) {
	targets, err := g.Check(ctx, address)
	if err != nil {
		return nil, err
	}
	timeout := g.Timeout
	if timeout == 0 {
		timeout = 10 * time.Second
	}
	d := net.Dialer{Timeout: timeout, KeepAlive: 30 * time.Second}
	var errs []error
	for _, t := range targets {
		c, err := d.DialContext(ctx, "tcp", t.String())
		if err == nil {
			return c, nil
		}
		errs = append(errs, err)
	}
	return nil, errors.Join(errs...)
}

// Reachable is the list's health line: a TCP connection within the timeout.
// It is a display hint only and never pauses or refuses an App.
func (g TargetGuard) Reachable(ctx context.Context, c CustomApp, timeout time.Duration) bool {
	u, err := c.target()
	if err != nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := g.DialContext(ctx, "tcp", net.JoinHostPort(u.Hostname(), strconv.Itoa(c.Port())))
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// OwnListeningPorts reads the TCP sockets in LISTEN state owned by this
// process's UID, the package account Connect's children also run as:
// cloudflared's metrics, tailscaled and NetBird. A read failure returns none.
func OwnListeningPorts() map[int]bool {
	out := map[int]bool{}
	for _, l := range ListeningTCP() {
		if l.UID == os.Geteuid() {
			out[l.Port] = true
		}
	}
	return out
}

// Listener is one LISTEN socket from /proc/net/tcp or tcp6.
type Listener struct {
	Addr netip.Addr
	Port int
	UID  int
}

// ListeningTCP lists LISTEN sockets. Outside Linux it returns none.
func ListeningTCP() []Listener {
	out := []Listener{}
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		raw, err := readFile(path, 8<<20, false)
		if err != nil {
			continue
		}
		out = append(out, parseProcNet(string(raw))...)
	}
	return out
}

func parseProcNet(raw string) []Listener {
	out := []Listener{}
	s := bufio.NewScanner(strings.NewReader(raw))
	s.Buffer(make([]byte, 4096), 4096)
	first := true
	for s.Scan() {
		if first {
			first = false
			continue
		}
		f := strings.Fields(s.Text())
		if len(f) < 8 || f[3] != "0A" {
			continue
		}
		hostHex, portHex, ok := strings.Cut(f[1], ":")
		if !ok {
			continue
		}
		port, err := strconv.ParseUint(portHex, 16, 16)
		uid, uidErr := strconv.Atoi(f[7])
		addr, addrOK := procAddr(hostHex)
		if err != nil || uidErr != nil || !addrOK || port == 0 {
			continue
		}
		out = append(out, Listener{Addr: addr, Port: int(port), UID: uid})
	}
	return out
}

// procAddr decodes the kernel's per-32-bit-word host byte order (little
// endian on every DSM platform) into an address.
func procAddr(h string) (netip.Addr, bool) {
	raw, err := hex.DecodeString(h)
	if err != nil || len(raw) != 4 && len(raw) != 16 {
		return netip.Addr{}, false
	}
	for i := 0; i < len(raw); i += 4 {
		raw[i], raw[i+1], raw[i+2], raw[i+3] = raw[i+3], raw[i+2], raw[i+1], raw[i]
	}
	if len(raw) == 4 {
		return netip.AddrFrom4([4]byte(raw)), true
	}
	return netip.AddrFrom16([16]byte(raw)).Unmap(), true
}

// AddCustom saves a new definition. It opens nothing: sharing is the
// ordinary approval afterwards.
func (s *State) AddCustom(r CustomRequest) (string, error) {
	if len(s.Custom) >= MaxCustom {
		return "", errCustomLimit
	}
	def, err := r.Definition(nil)
	if err != nil {
		return "", err
	}
	id, err := NewCustomID(*s, def.Name)
	if err != nil {
		return "", err
	}
	if _, err = s.Revision(); err != nil {
		return "", err
	}
	if s.Custom == nil {
		s.Custom = map[string]CustomApp{}
	}
	s.Custom[id] = def
	return id, nil
}

// EditCustom replaces a definition. An approved public scope follows the new
// target under the same socket and address; restart reports whether the
// forwarding settings changed, so the running proxy must be replaced.
func (s *State) EditCustom(r CustomRequest) (restart *Scope, err error) {
	previous, ok := s.Custom[r.ID]
	if !ok {
		return nil, errCustomMissing
	}
	if previous.Ingress != nil {
		return nil, ErrInvalid
	}
	def, err := r.Definition(&previous)
	if err != nil {
		return nil, err
	}
	rev, err := s.Revision()
	if err != nil {
		return nil, err
	}
	s.Custom[r.ID] = def
	app, ok := s.Apps[r.ID]
	if !ok || app.Public == nil || def.Digest() == previous.Digest() {
		return nil, nil
	}
	old := *app.Public
	app.Rev = rev
	app.Public.Binding.Summary, app.Public.Binding.Port = def.Digest(), def.Port()
	s.Apps[r.ID] = app
	return &old, nil
}

// RemoveCustom deletes the definition. An approved scope stays only as
// removal intent, so cleanup deletes its Worker or DNS record, even after a
// restart; DropRemovedCustom forgets it once that is done.
func (s *State) RemoveCustom(id string) (*Scope, error) {
	def, ok := s.Custom[id]
	if !ok {
		return nil, errCustomMissing
	}
	rev, err := s.Revision()
	if err != nil {
		return nil, err
	}
	delete(s.Custom, id)
	app, ok := s.Apps[id]
	if !ok || app.Public == nil {
		if def.Published == nil {
			delete(s.Apps, id)
			return nil, nil
		}
		app = AppState{Rev: rev, Public: &Scope{
			Provider: def.Published.Provider, State: Paused, Reason: "removed", Cleanup: true,
			Origin: "https://" + def.Published.Authority, Authority: def.Published.Authority, Socket: def.Published.Socket,
			Binding: Binding{Port: def.Port(), Summary: def.Digest(), Pin: Pin{Mode: string(ModeCustom)}},
		}}
		s.Apps[id] = app
		copy := *app.Public
		return &copy, nil
	}
	old := *app.Public
	app.Rev = rev
	app.Public.State, app.Public.Reason, app.Public.Cleanup = Paused, "removed", true
	s.Apps[id] = app
	return &old, nil
}

// DropRemovedCustom forgets removed custom Apps whose cloud cleanup is done.
func (s *State) DropRemovedCustom() {
	for id, app := range s.Apps {
		if _, ok := s.Custom[id]; ok || !IsCustom(id) {
			continue
		}
		if app.Public == nil || app.Public.State == Paused && app.Public.Reason == "removed" && !app.Public.Cleanup {
			delete(s.Apps, id)
		}
	}
}

// CustomActive is a running custom proxy's per-request check: the scope is
// still active on this socket and address and the definition still matches.
func CustomActive(s State, b PinBinding) (CustomApp, bool) {
	def, ok := s.Custom[b.Package]
	scope := s.Apps[b.Package].Public
	return def, ok && (def.Ingress == nil || !def.Ingress.Revoked) && scope != nil && b.Scope == Public && scope.State == Active && scope.Socket == b.Socket &&
		scope.Authority == b.Authority && def.Digest() == b.Summary && scope.Binding.Summary == b.Summary
}

// ErrCustomMissing is returned for an ID without a saved definition.
func ErrCustomMissing() error { return errCustomMissing }
