package bigupload

import (
	"context"
	"io"
	"net/http"
	"sync"
	"time"
)

type state int

const (
	stateActive state = iota
	stateDone
	stateFailed
	stateCancelled
)

const (
	modeStream = "stream"
	modeStore  = "store"
)

// part is a piece of the DSM request body: literal bytes or a file part.
type part struct {
	data []byte
	blob *blob
}

func (p part) size() int64 {
	if p.blob != nil {
		return p.blob.size
	}
	return int64(len(p.data))
}

// answer is DSM's response, kept for the page.
type answer struct {
	Status     int
	StatusText string
	Header     http.Header
	Body       []byte
}

type session struct {
	m       *Manager
	id      string
	owner   string
	nonce   string // client's create nonce, may be empty
	mode    string
	parts   []part
	files   []*blob
	request *http.Request // DSM request without body; cookies stay in memory only
	forward Forwarder

	sig    chan struct{} // data arrived; read by the DSM writer only
	ctx    context.Context
	cancel context.CancelFunc

	mu     sync.Mutex
	st     state
	code   string
	answer *answer
	done   chan struct{}
	sent   bool // DSM request opened
	idle   *time.Timer
}

func (s *session) poke() {
	select {
	case s.sig <- struct{}{}:
	default:
	}
}

func (s *session) isActive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st == stateActive
}

func (s *session) state() (state, string, *answer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.st, s.code, s.answer
}

// touch resets the idle timer; any request of the page counts.
func (s *session) touch() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.idle != nil {
		if s.st == stateActive {
			s.idle.Reset(s.m.cfg.SessionIdle)
		} else {
			s.idle.Reset(s.m.cfg.EndedKeep)
		}
	}
}

func (s *session) received() int64 {
	var n int64
	for _, p := range s.parts {
		if p.blob != nil {
			n += p.blob.contiguous()
		}
	}
	return n
}

// finish ends the session once. Data is deleted unless the session failed,
// in which case it waits for the App's new request.
func (s *session) finish(st state, code string, a *answer) {
	s.mu.Lock()
	if s.st != stateActive {
		s.mu.Unlock()
		return
	}
	s.st, s.code, s.answer = st, code, a
	close(s.done)
	if s.idle != nil {
		s.idle.Reset(s.m.cfg.EndedKeep) // the page may fetch the answer again
	}
	s.mu.Unlock()
	s.cancel()
	for _, b := range s.files {
		if st == stateFailed {
			s.m.retain(b)
		} else {
			b.remove()
		}
	}
}

// drop forgets an ended session.
func (s *session) drop() {
	s.mu.Lock()
	if s.idle != nil {
		s.idle.Stop()
	}
	s.mu.Unlock()
	s.m.mu.Lock()
	if s.m.sessions[s.id] == s {
		delete(s.m.sessions, s.id)
	}
	s.m.mu.Unlock()
}

// expire runs when the page has sent nothing for SessionIdle: an active
// session fails (its data waits for reuse). An ended session is forgotten
// EndedKeep after its last request.
func (s *session) expire() {
	s.finish(stateFailed, "expired", nil)
	s.drop()
}

func (s *session) totalSize() int64 {
	var n int64
	for _, p := range s.parts {
		n += p.size()
	}
	return n
}

// ready reports whether the DSM request may open: stream mode at the first
// stored byte (or a body without file parts), store mode with every byte.
func (s *session) ready() bool {
	if s.mode == modeStore {
		for _, b := range s.files {
			if !b.complete() {
				return false
			}
		}
		return true
	}
	if len(s.files) == 0 {
		return true
	}
	for _, b := range s.files {
		if b.contiguous() > 0 {
			return true
		}
	}
	return false
}

// run waits until the request may open, then sends it to DSM and keeps the
// answer. A DSM request is sent at most once: it is never replayed.
func (s *session) run() {
	for !s.ready() {
		select {
		case <-s.ctx.Done():
			return
		case <-s.sig:
		}
	}
	req := s.request.Clone(s.ctx)
	req.Body = io.NopCloser(&bodyReader{s: s})
	req.ContentLength = s.totalSize()
	req.GetBody = nil
	s.mu.Lock()
	s.sent = true
	s.mu.Unlock()
	res, err := s.forward(req)
	if err != nil {
		if s.ctx.Err() == nil {
			s.finish(stateFailed, "stream_broken", nil)
		}
		return
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, s.m.cfg.MaxAnswer+1))
	if err != nil {
		if s.ctx.Err() == nil {
			s.finish(stateFailed, "stream_broken", nil)
		}
		return
	}
	if int64(len(body)) > s.m.cfg.MaxAnswer {
		s.finish(stateFailed, "answer_too_large", nil)
		return
	}
	h := res.Header.Clone()
	for _, k := range []string{"Content-Length", "Transfer-Encoding", "Connection", "Keep-Alive", "Content-Encoding"} {
		h.Del(k)
	}
	text := res.Status
	if len(text) > 4 && text[3] == ' ' {
		text = text[4:]
	}
	s.finish(stateDone, "", &answer{Status: res.StatusCode, StatusText: text, Header: h, Body: body})
}

// bodyReader reads the DSM request body in order, waiting for missing bytes.
type bodyReader struct {
	s   *session
	idx int
	pos int64
}

func (r *bodyReader) Read(p []byte) (int, error) {
	for {
		if r.idx >= len(r.s.parts) {
			return 0, io.EOF
		}
		pt := r.s.parts[r.idx]
		if pt.blob == nil {
			n := copy(p, pt.data[r.pos:])
			r.pos += int64(n)
			if r.pos >= int64(len(pt.data)) {
				r.idx, r.pos = r.idx+1, 0
			}
			if n > 0 {
				return n, nil
			}
			continue
		}
		if r.pos >= pt.blob.size {
			r.idx, r.pos = r.idx+1, 0
			continue
		}
		if avail := pt.blob.contiguous(); r.pos < avail {
			n, err := pt.blob.f.ReadAt(p[:min(int64(len(p)), avail-r.pos)], r.pos)
			r.pos += int64(n)
			if n > 0 {
				return n, nil
			}
			if err != nil {
				return 0, err
			}
			continue
		}
		select {
		case <-r.s.ctx.Done():
			return 0, r.s.ctx.Err()
		case <-r.s.sig:
		}
	}
}
