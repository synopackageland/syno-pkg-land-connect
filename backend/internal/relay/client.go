// Package relay implements hostc v5 over a single authenticated WebSocket.
// Local requests can only reach the administrator-approved pin proxy socket.
package relay

import (
	"bytes"
	"context"
	"dsmapp/internal/publicnet"
	"dsmapp/internal/supervise"
	"encoding/binary"
	"encoding/json"
	"errors"
	"github.com/synopackageland/syno-pkg-land-sdk/ipc"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

const (
	openFrame       byte = 1
	headFrame       byte = 2
	dataFrame       byte = 3
	textFrame       byte = 4
	endFrame        byte = 5
	resetFrame      byte = 6
	windowFrame     byte = 7
	window               = 256 * 1024
	chunk                = 64 * 1024
	maxMessage           = 1024*1024 - 64
	maxStreams           = 256
	wsQueueMessages      = 512
	wsBufferBytes        = 16 << 20
	heartbeatGrace       = 10 * time.Second
)

var errProtocol = errors.New("relay protocol violation")

type Config struct {
	URL, Token, Socket, Authority string
	Dialer                        *websocket.Dialer
	// Connected reports the live WebSocket, never the existence of a local proxy.
	Connected func(bool)
}
type session struct {
	wsBytes      atomic.Int64
	wsBlocked    atomic.Int64
	wsResumed    atomic.Int64
	wsBudgetWake chan struct{}
	pong         chan struct{}
	ctx          context.Context
	conn         *websocket.Conn
	cfg          Config
	write        sync.Mutex
	mu           sync.Mutex
	streams      map[uint32]*stream
	wg           sync.WaitGroup
}
type stream struct {
	finished   bool
	s          *session
	id         uint32
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	input      bytes.Buffer
	ended      bool
	credit     uint64
	inputWake  chan struct{}
	creditWake chan struct{}
	ws         chan frame
	websocket  bool
}
type frame struct {
	kind    byte
	payload []byte
}
type openMessage struct {
	Method    string      `json:"method"`
	Path      string      `json:"path"`
	Headers   [][2]string `json:"headers"`
	Body      bool        `json:"body"`
	Websocket *[]string   `json:"websocket,omitempty"`
}
type headMessage struct {
	Status   int         `json:"status"`
	Headers  [][2]string `json:"headers"`
	Body     bool        `json:"body"`
	Protocol string      `json:"protocol,omitempty"`
}

// Run reconnects until canceled. In-flight streams are never replayed. Backoff
// resets only after a stable session, so a peer that immediately closes cannot spin.
func Run(ctx context.Context, cfg Config) {
	retry := supervise.Backoff{First: time.Second, Max: time.Minute}
	for ctx.Err() == nil {
		started := time.Now()
		_ = Connect(ctx, cfg)
		if ctx.Err() != nil {
			return
		}
		if time.Since(started) > time.Minute {
			retry.Reset()
		}
		delay := retry.Next()
		timer := time.NewTimer(delay + time.Duration(rand.Int64N(int64(delay/4))))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// Connect runs one session. No HTTP response/body timeout is imposed on a slow NAS.
func Connect(ctx context.Context, cfg Config) (retErr error) {
	d := cfg.Dialer
	if d == nil {
		d = &websocket.Dialer{HandshakeTimeout: time.Minute, NetDialContext: (publicnet.Dialer{}).DialContext}
	}
	h := http.Header{"Hostc-Protocol": []string{"5"}, "Authorization": []string{"Bearer " + cfg.Token}}
	c, res, err := d.DialContext(ctx, cfg.URL, h)
	if res != nil && res.Body != nil {
		res.Body.Close()
	}
	if err != nil {
		return errors.New("relay connection unavailable")
	}
	defer c.Close()
	if cfg.Connected != nil {
		cfg.Connected(true)
		defer cfg.Connected(false)
	}
	stopClose := context.AfterFunc(ctx, func() { c.Close() })
	defer stopClose()
	c.SetReadLimit(maxMessage + 5)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s := &session{ctx: ctx, conn: c, cfg: cfg, pong: make(chan struct{}, 1), wsBudgetWake: make(chan struct{}, 1), streams: map[uint32]*stream{}}
	done := make(chan struct{})
	go func() { defer close(done); s.heartbeat() }()
	defer func() {
		if errors.Is(retErr, errProtocol) {
			_ = c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseProtocolError, "relay protocol violation"), time.Now().Add(time.Second))
		}
		cancel()
		c.Close()
		<-done
		s.mu.Lock()
		for _, st := range s.streams {
			st.cancel()
		}
		s.mu.Unlock()
		s.wg.Wait()
	}()
	for {
		kind, raw, err := c.ReadMessage()
		if err != nil {
			return errors.New("relay disconnected")
		}
		if kind == websocket.TextMessage {
			if string(raw) != "hostc:pong" {
				return errProtocol
			}
			select {
			case s.pong <- struct{}{}:
			default:
			}
			continue
		}
		if kind != websocket.BinaryMessage || len(raw) < 5 || raw[0] < 1 || raw[0] > 7 {
			return errProtocol
		}
		id := binary.BigEndian.Uint32(raw[1:5])
		if id == 0 {
			return errProtocol
		}
		if err = s.receive(raw[0], id, raw[5:]); err != nil {
			return err
		}
	}
}
func (s *session) heartbeat() {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-t.C:
		}
		select {
		case <-s.pong:
		default:
		}
		s.write.Lock()
		err := s.conn.WriteMessage(websocket.TextMessage, []byte("hostc:ping"))
		s.write.Unlock()
		if err != nil {
			s.conn.Close()
			return
		}
		timeout := time.NewTimer(heartbeatGrace)
	waiting:
		for {
			select {
			case <-s.ctx.Done():
				timeout.Stop()
				return
			case <-s.pong:
				timeout.Stop()
				break waiting
			case <-timeout.C:
				if s.wsBackpressured(time.Now()) {
					timeout.Reset(heartbeatGrace)
					continue
				}
				s.conn.Close()
				return
			}
		}
	}
}
func (s *session) send(kind byte, id uint32, payload []byte) error {
	raw := make([]byte, 5+len(payload))
	raw[0] = kind
	binary.BigEndian.PutUint32(raw[1:5], id)
	copy(raw[5:], payload)
	s.write.Lock()
	defer s.write.Unlock()
	return s.conn.WriteMessage(websocket.BinaryMessage, raw)
}
func (s *session) receive(kind byte, id uint32, p []byte) error {
	s.mu.Lock()
	st := s.streams[id]
	s.mu.Unlock()
	if kind == openFrame {
		if st != nil {
			return errProtocol
		}
		var op openMessage
		if json.Unmarshal(p, &op) != nil || op.Method == "" || op.Websocket != nil && op.Body || !strings.HasPrefix(op.Path, "/") {
			return errProtocol
		}
		// The path is visitor text that the Worker forwards verbatim, so an
		// unparsable one refuses only this stream, not the whole session.
		if u, err := url.ParseRequestURI(op.Path); err != nil || u.IsAbs() || u.Host != "" {
			return s.send(resetFrame, id, []byte("invalid path"))
		}
		s.mu.Lock()
		if len(s.streams) >= maxStreams {
			s.mu.Unlock()
			return s.send(resetFrame, id, []byte("stream limit"))
		}
		ctx, cancel := context.WithCancel(s.ctx)
		st = &stream{s: s, id: id, ctx: ctx, cancel: cancel, credit: window, inputWake: make(chan struct{}, 1), creditWake: make(chan struct{}, 1), ws: make(chan frame, wsQueueMessages), websocket: op.Websocket != nil, ended: !op.Body}
		s.streams[id] = st
		s.wg.Add(1)
		s.mu.Unlock()
		go func() {
			defer s.wg.Done()
			defer st.drainWS()
			defer cancel()
			defer func() { s.mu.Lock(); delete(s.streams, id); s.mu.Unlock() }()
			if err := st.run(op); err != nil && ctx.Err() == nil {
				_ = s.send(resetFrame, id, []byte("local request unavailable"))
			}
		}()
		return nil
	}
	if st == nil {
		return nil
	}
	if kind == resetFrame {
		st.cancel()
		return nil
	}
	if st.websocket {
		if kind != dataFrame && kind != textFrame && kind != endFrame || len(p) > maxMessage {
			return errProtocol
		}
		st.mu.Lock()
		defer st.mu.Unlock()
		if st.finished {
			return nil
		}
		size := int64(len(p))
		if !s.reserveWS(st.ctx, size) {
			return nil
		}
		f := frame{kind, p}
		select {
		case st.ws <- f:
			return nil
		default:
		}
		resume := s.waitWS()
		defer resume()
		select {
		case st.ws <- f:
		case <-st.ctx.Done():
			s.releaseWS(size)
		}
		return nil
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	switch kind {
	case dataFrame:
		if st.ended || len(p) > chunk || st.input.Len()+len(p) > window {
			return errProtocol
		}
		st.input.Write(p)
		select {
		case st.inputWake <- struct{}{}:
		default:
		}
	case endFrame:
		if len(p) != 0 || st.ended {
			return errProtocol
		}
		st.ended = true
		select {
		case st.inputWake <- struct{}{}:
		default:
		}
	case windowFrame:
		if len(p) != 4 {
			return errProtocol
		}
		grant := uint64(binary.BigEndian.Uint32(p))
		if grant == 0 || st.credit+grant < st.credit {
			return errProtocol
		}
		st.credit += grant
		select {
		case st.creditWake <- struct{}{}:
		default:
		}
	default:
		return errProtocol
	}
	return nil
}
func (st *stream) Read(p []byte) (int, error) {
	for {
		st.mu.Lock()
		n, _ := st.input.Read(p)
		end := st.ended
		st.mu.Unlock()
		if n > 0 {
			var grant [4]byte
			binary.BigEndian.PutUint32(grant[:], uint32(n))
			if err := st.s.send(windowFrame, st.id, grant[:]); err != nil {
				return 0, err
			}
			return n, nil
		}
		if end {
			return 0, io.EOF
		}
		select {
		case <-st.ctx.Done():
			return 0, st.ctx.Err()
		case <-st.inputWake:
		}
	}
}
func (st *stream) Close() error { return nil }
func (st *stream) run(op openMessage) error {
	headers := http.Header{}
	for _, h := range op.Headers {
		headers.Add(h[0], h[1])
	}
	strip(headers)
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		return ipc.DialUnix(ctx, st.s.cfg.Socket)
	}
	if op.Websocket != nil {
		return st.runWS(op, headers, dial)
	}
	var body io.Reader
	if op.Body {
		body = st
	}
	req, err := http.NewRequestWithContext(st.ctx, op.Method, "http://"+st.s.cfg.Authority+op.Path, body)
	if err != nil {
		return err
	}
	req.Host = st.s.cfg.Authority
	req.Header = headers
	// net/http owns framing; never trust a peer's transfer-encoding or request Host.
	req.Header.Del("Content-Length")
	tr := &http.Transport{DialContext: dial, DisableCompression: true}
	defer tr.CloseIdleConnections()
	res, err := tr.RoundTrip(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	strip(res.Header)
	hasBody := op.Method != "HEAD" && res.StatusCode >= 200 && res.StatusCode != 204 && res.StatusCode != 205 && res.StatusCode != 304
	if err = st.head(headMessage{Status: res.StatusCode, Headers: pairs(res.Header), Body: hasBody}); err != nil || !hasBody {
		return err
	}
	buf := make([]byte, chunk)
	for {
		st.mu.Lock()
		credit := st.credit
		st.mu.Unlock()
		if credit == 0 {
			select {
			case <-st.ctx.Done():
				return st.ctx.Err()
			case <-st.creditWake:
				continue
			}
		}
		limit := chunk
		if credit < uint64(limit) {
			limit = int(credit)
		}
		n, e := res.Body.Read(buf[:limit])
		if n > 0 {
			st.mu.Lock()
			st.credit -= uint64(n)
			st.mu.Unlock()
			if err = st.s.send(dataFrame, st.id, buf[:n]); err != nil {
				return err
			}
		}
		if e == io.EOF {
			return st.s.send(endFrame, st.id, nil)
		}
		if e != nil {
			return e
		}
	}
}
func (st *stream) head(h headMessage) error {
	p, _ := json.Marshal(h)
	return st.s.send(headFrame, st.id, p)
}
func strip(h http.Header) {
	for _, v := range h.Values("Connection") {
		for _, k := range strings.Split(v, ",") {
			h.Del(strings.TrimSpace(k))
		}
	}
	for _, k := range []string{"Connection", "Proxy-Connection", "Keep-Alive", "Transfer-Encoding", "TE", "Trailer", "Upgrade", "Proxy-Authorization", "Proxy-Authenticate", "Forwarded", "X-Forwarded-Host", "X-Forwarded-Proto", "Host"} {
		h.Del(k)
	}
}
func pairs(h http.Header) [][2]string {
	out := make([][2]string, 0)
	for k, vs := range h {
		for _, v := range vs {
			out = append(out, [2]string{k, v})
		}
	}
	return out
}
func (st *stream) runWS(op openMessage, h http.Header, dial func(context.Context, string, string) (net.Conn, error)) error {
	for _, k := range []string{"Sec-WebSocket-Key", "Sec-WebSocket-Version", "Sec-WebSocket-Extensions", "Sec-WebSocket-Protocol"} {
		h.Del(k)
	}
	// DialContext does not interrupt a stalled upgrade response on cancel, so
	// close the local connection itself when the stream ends.
	var local struct {
		sync.Mutex
		conn net.Conn
	}
	stopLocal := context.AfterFunc(st.ctx, func() {
		local.Lock()
		defer local.Unlock()
		if local.conn != nil {
			local.conn.Close()
		}
	})
	defer stopLocal()
	closing := func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dial(ctx, network, addr)
		if err != nil {
			return nil, err
		}
		local.Lock()
		local.conn = conn
		local.Unlock()
		if st.ctx.Err() != nil {
			conn.Close()
		}
		return conn, nil
	}
	d := websocket.Dialer{NetDialContext: closing, Subprotocols: *op.Websocket, HandshakeTimeout: 0}
	c, res, err := d.DialContext(st.ctx, "ws://"+st.s.cfg.Authority+op.Path, h)
	if err != nil {
		if res != nil && res.Body != nil {
			res.Body.Close()
			strip(res.Header)
			return st.head(headMessage{Status: res.StatusCode, Headers: pairs(res.Header), Body: false})
		}
		return err
	}
	defer c.Close()
	stopClose := context.AfterFunc(st.ctx, func() { c.Close() })
	defer stopClose()
	c.SetReadLimit(maxMessage)
	strip(res.Header)
	for _, k := range []string{"Sec-WebSocket-Accept", "Sec-WebSocket-Protocol", "Sec-WebSocket-Extensions"} {
		res.Header.Del(k)
	}
	if err = st.head(headMessage{Status: 101, Headers: pairs(res.Header), Body: false, Protocol: c.Subprotocol()}); err != nil {
		return err
	}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		defer c.Close()
		for {
			select {
			case <-st.ctx.Done():
				return
			case f := <-st.ws:
				if f.kind == endFrame {
					st.s.releaseWS(int64(len(f.payload)))
					if len(f.payload) >= 2 {
						code := int(binary.BigEndian.Uint16(f.payload))
						if validClose(code) {
							_ = c.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, string(f.payload[2:])), time.Now().Add(time.Second))
						}
					}
					return
				}
				kind := websocket.BinaryMessage
				if f.kind == textFrame {
					kind = websocket.TextMessage
				}
				err := c.WriteMessage(kind, f.payload)
				st.s.releaseWS(int64(len(f.payload)))
				if err != nil {
					return
				}
			}
		}
	}()
	defer func() { st.cancel(); c.Close(); <-finished }()
	for {
		kind, p, err := c.ReadMessage()
		if err != nil {
			code := 1000
			reason := ""
			var closeErr *websocket.CloseError
			if errors.As(err, &closeErr) {
				if validClose(closeErr.Code) {
					code = closeErr.Code
				}
				reason = closeErr.Text
			}
			if len(reason) > 123 {
				reason = ""
			}
			payload := make([]byte, 2)
			binary.BigEndian.PutUint16(payload, uint16(code))
			return st.s.send(endFrame, st.id, append(payload, []byte(reason)...))
		}
		f := dataFrame
		if kind == websocket.TextMessage {
			f = textFrame
		}
		if err = st.s.send(f, st.id, p); err != nil {
			return err
		}
	}
}
func validClose(code int) bool {
	return code >= 1000 && code <= 1014 && code != 1004 && code != 1005 && code != 1006 || code >= 3000 && code <= 4999
}

func (st *stream) drainWS() {
	if !st.websocket {
		return
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	st.finished = true
	for {
		select {
		case f := <-st.ws:
			st.s.releaseWS(int64(len(f.payload)))
		default:
			return
		}
	}
}
