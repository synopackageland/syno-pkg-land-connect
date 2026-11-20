// Package lanswitch lets a browser that reached DSM through Cloudflare move to
// DSM's own LAN address when, and only when, it can prove that it reaches the
// NAS there directly.
//
// While the option is on, the daemon runs a tiny ICE-lite responder on the
// manifest-declared lan_probe TCP port. A signed-in page asks for a probe:
// fresh ICE credentials and the NAS's private IPv4 addresses with the DSM URL
// each one leads to. The page runs a WebRTC connectivity check (ICE over TCP)
// against every address in parallel; only a reply signed with the probe's
// password completes it. The page offers the switch for the first address
// that completes and stays on Cloudflare otherwise. Browsers do not ask for
// local network access for ICE checks, unlike for fetch.
//
// Nothing here changes DSM. The responder answers nothing but a correctly
// signed Binding request for a live credential.
package lanswitch

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"dsmapp/internal/daemon"
	"dsmapp/internal/phonelink"
	"dsmapp/internal/ports"
)

// MaxCandidates bounds the addresses a probe offers.
const MaxCandidates = 8

// Settings is the saved option.
type Settings struct {
	Enabled bool `json:"enabled"`
}

// Status is what the settings page shows.
type Status struct {
	Enabled bool `json:"enabled"`
	// Port is the responder's TCP port, 0 when it is not listening.
	Port int `json:"port"`
	// Addresses lists the NAS's LAN addresses a probe would offer.
	Addresses []string `json:"addresses"`
}

// Candidate is one LAN address to check and the DSM URL it leads to.
type Candidate struct {
	Address string `json:"address"`
	Port    int    `json:"port"`
	URL     string `json:"url"`
}

// Probe is the answer to phone.lanprobe. With Enabled false nothing else is set.
type Probe struct {
	Enabled    bool        `json:"enabled"`
	Ufrag      string      `json:"ufrag,omitempty"`
	Pwd        string      `json:"pwd,omitempty"`
	HTTPS      bool        `json:"https,omitempty"`
	Candidates []Candidate `json:"candidates"`
}

// Service owns the option and the responder in the daemon.
type Service struct {
	// Path is the private settings file.
	Path string
	// DSM reads DSM's ports and its HTTP-to-HTTPS redirect setting.
	DSM func(context.Context) (ports.DSMPorts, bool, error)
	// Interfaces lists the network interfaces; nil reads the system.
	Interfaces func() ([]phonelink.Interface, error)
	// Dial checks that DSM accepts connections on an address; nil dials TCP.
	Dial func(context.Context, netip.AddrPort) error
	// Responder is created on first use; tests may set one.
	Responder *Responder

	mu      sync.Mutex
	enabled bool
}

func (s *Service) responder() *Responder {
	if s.Responder == nil {
		s.Responder = &Responder{}
	}
	return s.Responder
}

// load reads the saved option; a missing or unreadable file is off.
func (s *Service) load() Settings {
	var v Settings
	raw, err := daemon.ReadPrivate(s.Path)
	if err != nil || json.Unmarshal(raw, &v) != nil {
		return Settings{}
	}
	return v
}

// Restore starts the responder when the saved option is on.
func (s *Service) Restore() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enabled = s.load().Enabled
	if s.enabled {
		_, _ = s.responder().Start()
	}
}

// Close stops the responder.
func (s *Service) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Responder != nil {
		s.Responder.Stop()
	}
}

// Status reports the option and where the responder listens.
func (s *Service) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := Status{Enabled: s.enabled, Addresses: []string{}}
	if s.enabled {
		// A port lost at startup is retried here.
		out.Port, _ = s.responder().Start()
		for _, a := range s.lanAddresses() {
			out.Addresses = append(out.Addresses, a.String())
		}
	}
	return out
}

// Set saves the option and starts or stops the responder.
func (s *Service) Set(enabled bool) (Status, error) {
	raw, _ := json.Marshal(Settings{Enabled: enabled})
	s.mu.Lock()
	err := daemon.WritePrivate(s.Path, raw)
	if err == nil {
		s.enabled = enabled
		if enabled {
			_, _ = s.responder().Start()
		} else if s.Responder != nil {
			s.Responder.Stop()
		}
	}
	s.mu.Unlock()
	if err != nil {
		return Status{}, err
	}
	return s.Status(), nil
}

// lanAddresses lists the NAS's private IPv4 LAN addresses. The declared
// listener is IPv4 only, so IPv6 ULA addresses are not offered.
func (s *Service) lanAddresses() []netip.Addr {
	ifaces := s.Interfaces
	if ifaces == nil {
		ifaces = phonelink.LiveInterfaces
	}
	list, err := ifaces()
	if err != nil {
		return nil
	}
	out := phonelink.LANAddresses(list, false)
	if len(out) > MaxCandidates {
		out = out[:MaxCandidates]
	}
	return out
}

func dialTCP(ctx context.Context, a netip.AddrPort) error {
	d := net.Dialer{Timeout: 500 * time.Millisecond}
	c, err := d.DialContext(ctx, "tcp", a.String())
	if err != nil {
		return err
	}
	return c.Close()
}

// Probe issues credentials and lists the candidates for one page. An address
// is offered only when the responder is listening and DSM accepts
// connections on that address. The protocol and port follow DSM's own settings: HTTPS
// when DSM redirects HTTP to HTTPS, HTTP otherwise.
func (s *Service) Probe(ctx context.Context) (Probe, error) {
	s.mu.Lock()
	enabled := s.enabled
	var probePort int
	if enabled {
		probePort, _ = s.responder().Start()
	}
	s.mu.Unlock()
	out := Probe{Enabled: enabled, Candidates: []Candidate{}}
	if !enabled || probePort == 0 || s.DSM == nil {
		return out, nil
	}
	addrs := s.lanAddresses()
	p, redirect, err := s.DSM(ctx)
	if err != nil {
		return out, nil
	}
	scheme, dsmPort := "http", p.HTTP
	if redirect || p.HTTP < 1 {
		scheme, dsmPort = "https", p.HTTPS
	}
	if dsmPort < 1 || dsmPort > 65535 {
		return out, nil
	}
	dial := s.Dial
	if dial == nil {
		dial = dialTCP
	}
	type result struct {
		i  int
		ok bool
	}
	ch := make(chan result, len(addrs))
	for i, a := range addrs {
		go func() { ch <- result{i, dial(ctx, netip.AddrPortFrom(a, uint16(dsmPort))) == nil} }()
	}
	ok := make([]bool, len(addrs))
	for range addrs {
		r := <-ch
		ok[r.i] = r.ok
	}
	for i, a := range addrs {
		if ok[i] {
			out.Candidates = append(out.Candidates, Candidate{
				Address: a.String(), Port: probePort,
				URL: scheme + "://" + net.JoinHostPort(a.String(), strconv.Itoa(dsmPort)),
			})
		}
	}
	if len(out.Candidates) == 0 {
		return out, nil
	}
	out.HTTPS = scheme == "https"
	out.Ufrag, out.Pwd, err = s.responder().Issue()
	if err != nil {
		return Probe{Enabled: true, Candidates: []Candidate{}}, errors.New("credentials unavailable")
	}
	return out, nil
}
