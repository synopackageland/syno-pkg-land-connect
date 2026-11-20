package lanswitch

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/ipc"
)

const (
	// sessionTTL bounds how long one page's probe credentials answer.
	sessionTTL = 2 * time.Minute
	// maxSessions bounds the credentials kept at once; the oldest go first.
	maxSessions = 64
	ufragLen    = 8  // 40 bits
	pwdLen      = 26 // 130 bits
	// Per-connection bounds: a browser's check sends a few small requests.
	maxConns     = 32
	maxFrame     = 1024
	maxFrames    = 32
	connLifetime = 15 * time.Second
)

// session holds one probe's ICE password.
type session struct {
	pwd     string
	expires time.Time
}

// Responder answers ICE connectivity checks over TCP (RFC 6544, passive
// candidate, RFC 4571 framing). It answers only a Binding request whose
// USERNAME starts with a ufrag it issued and whose MESSAGE-INTEGRITY is
// signed with that ufrag's password, so a reply proves both that the browser
// reached this address and that the NAS holding the password answered.
// Anything else gets no reply; a malformed frame closes the connection.
type Responder struct {
	// Listen opens the listener; nil uses the manifest-declared lan_probe listener.
	Listen func(context.Context) (net.Listener, error)
	Now    func() time.Time

	mu       sync.Mutex
	ln       net.Listener
	cancel   context.CancelFunc
	sessions map[string]session
	order    []string
	open     map[net.Conn]struct{}
	wg       sync.WaitGroup
}

func (r *Responder) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func listenProbe(ctx context.Context) (net.Listener, error) {
	return ipc.ListenTCP(ctx, "lan_probe", 0)
}

// Start opens the listener unless it is open and reports its port.
func (r *Responder) Start() (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ln != nil {
		return port(r.ln), nil
	}
	listen := r.Listen
	if listen == nil {
		listen = listenProbe
	}
	ctx, cancel := context.WithCancel(context.Background())
	ln, err := listen(ctx)
	if err != nil {
		cancel()
		return 0, err
	}
	r.ln, r.cancel = ln, cancel
	r.wg.Add(1)
	go r.accept(ln)
	return port(ln), nil
}

func port(ln net.Listener) int {
	if a, ok := ln.Addr().(*net.TCPAddr); ok {
		return a.Port
	}
	return 0
}

// Port reports the listening port, or 0 when stopped.
func (r *Responder) Port() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.ln == nil {
		return 0
	}
	return port(r.ln)
}

// Stop closes the listener and every open check and forgets every credential.
func (r *Responder) Stop() {
	r.mu.Lock()
	if r.ln != nil {
		r.cancel()
		r.ln.Close()
		r.ln, r.cancel = nil, nil
	}
	for c := range r.open {
		c.Close()
	}
	r.sessions, r.order = nil, nil
	r.mu.Unlock()
	r.wg.Wait()
}

// iceChars is a subset of the ICE ice-char set with nothing to escape in a
// URL or SDP; 32 symbols keep every byte unbiased (5 bits each).
const iceChars = "abcdefghijklmnopqrstuvwxyz234567"

func randomICE(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = iceChars[int(b[i])%len(iceChars)]
	}
	return string(b), nil
}

// Issue returns fresh ICE credentials for one probe.
func (r *Responder) Issue() (ufrag, pwd string, err error) {
	if ufrag, err = randomICE(ufragLen); err != nil {
		return "", "", err
	}
	if pwd, err = randomICE(pwdLen); err != nil {
		return "", "", err
	}
	now := r.now()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sessions == nil {
		r.sessions = map[string]session{}
	}
	r.prune(now)
	for len(r.order) >= maxSessions {
		delete(r.sessions, r.order[0])
		r.order = r.order[1:]
	}
	if _, taken := r.sessions[ufrag]; taken {
		return "", "", errors.New("credential collision")
	}
	r.sessions[ufrag] = session{pwd: pwd, expires: now.Add(sessionTTL)}
	r.order = append(r.order, ufrag)
	return ufrag, pwd, nil
}

// prune drops expired credentials; r.mu is held.
func (r *Responder) prune(now time.Time) {
	keep := r.order[:0]
	for _, u := range r.order {
		if s, ok := r.sessions[u]; ok && now.Before(s.expires) {
			keep = append(keep, u)
		} else {
			delete(r.sessions, u)
		}
	}
	r.order = keep
}

// password returns the live password for the local ufrag in a STUN USERNAME ("ours:theirs").
func (r *Responder) password(username string) (string, bool) {
	ours, _, ok := strings.Cut(username, ":")
	if !ok || len(ours) != ufragLen {
		return "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[ours]
	if !ok || !r.now().Before(s.expires) {
		return "", false
	}
	return s.pwd, true
}

// Answer returns the reply to one STUN message, or nil when it gets none.
func (r *Responder) Answer(msg []byte, from netip.AddrPort) []byte {
	req, ok := parseBinding(msg)
	if !ok {
		return nil
	}
	pwd, ok := r.password(req.username)
	if !ok || !validIntegrity(msg, req, []byte(pwd)) {
		return nil
	}
	return successResponse(req, from, []byte(pwd))
}

func (r *Responder) accept(ln net.Listener) {
	defer r.wg.Done()
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			r.mu.Lock()
			closed := r.ln != ln
			r.mu.Unlock()
			if closed {
				return
			}
			time.Sleep(50 * time.Millisecond)
			continue
		}
		r.mu.Lock()
		full := len(r.open) >= maxConns || r.ln != ln
		if !full {
			if r.open == nil {
				r.open = map[net.Conn]struct{}{}
			}
			r.open[c] = struct{}{}
			r.wg.Add(1)
		}
		r.mu.Unlock()
		if full {
			c.Close()
			continue
		}
		go r.serve(c)
	}
}

// serve answers framed checks on one connection until it ends, misbehaves,
// reaches its frame bound or lives out connLifetime, or Stop closes it.
func (r *Responder) serve(c net.Conn) {
	defer func() {
		c.Close()
		r.mu.Lock()
		delete(r.open, c)
		r.mu.Unlock()
		r.wg.Done()
	}()
	_ = c.SetDeadline(time.Now().Add(connLifetime))
	from := netip.AddrPort{}
	if a, ok := c.RemoteAddr().(*net.TCPAddr); ok {
		from = a.AddrPort()
	}
	var head [2]byte
	buf := make([]byte, maxFrame)
	for range maxFrames {
		if _, err := io.ReadFull(c, head[:]); err != nil {
			return
		}
		n := int(binary.BigEndian.Uint16(head[:]))
		if n < headerLen || n > maxFrame {
			return
		}
		if _, err := io.ReadFull(c, buf[:n]); err != nil {
			return
		}
		out := r.Answer(buf[:n], from)
		if out == nil {
			continue
		}
		framed := make([]byte, 2+len(out))
		binary.BigEndian.PutUint16(framed, uint16(len(out)))
		copy(framed[2:], out)
		if _, err := c.Write(framed); err != nil {
			return
		}
	}
}
