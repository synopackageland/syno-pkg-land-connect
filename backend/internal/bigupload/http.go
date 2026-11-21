package bigupload

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Forwarder sends a request to DSM. The request URL is origin-form (path and
// query) and Host is the public name; the caller supplies scheme, address and
// the pinned TLS connection.
type Forwarder func(*http.Request) (*http.Response, error)

// Prefix is the path the page uses; everything under it belongs to this package.
const (
	Prefix     = "/_connect/upload"
	ScriptPath = "/_connect/upload.js"
)

// Handles reports whether a request path belongs to this package.
func Handles(path string) bool {
	return path == ScriptPath || path == Prefix+"/sessions" || strings.HasPrefix(path, Prefix+"/sessions/")
}

// Handler serves the protocol for one DSM binding.
func (m *Manager) Handler(forward Forwarder) http.Handler {
	h := &handler{m: m, forward: forward}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+ScriptPath, serveScript)
	mux.HandleFunc("POST "+Prefix+"/sessions", h.create)
	mux.HandleFunc("DELETE "+Prefix+"/sessions/{id}", h.cancel)
	mux.HandleFunc("GET "+Prefix+"/sessions/{id}/result", h.result)
	mux.HandleFunc("PUT "+Prefix+"/sessions/{id}/files/{n}", h.put)
	mux.HandleFunc("GET "+Prefix+"/sessions/{id}/files/{n}", h.probe)
	return mux
}

type handler struct {
	m       *Manager
	forward Forwarder
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, reason string) {
	writeJSON(w, code, map[string]string{"error": reason})
}

// Request headers the page may not choose; the session request's own values
// (cookie, browser identity) are used instead, or none at all.
var dropped = map[string]bool{
	"cookie": true, "host": true, "content-length": true, "transfer-encoding": true, "connection": true,
	"keep-alive": true, "upgrade": true, "te": true, "trailer": true, "expect": true, "proxy-authorization": true,
	"proxy-connection": true, "forwarded": true, "x-forwarded-for": true, "x-forwarded-host": true,
	"x-forwarded-proto": true, "user-agent": true, "origin": true, "referer": true, "accept-encoding": true,
	"accept-language": true, "cf-connecting-ip": true,
}

type createBody struct {
	Method  string      `json:"method"`
	URL     string      `json:"url"`
	Headers [][2]string `json:"headers"`
	Parts   []struct {
		Data *string   `json:"data"`
		File *fileSpec `json:"file"`
	} `json:"parts"`
	Mode string `json:"mode"`
	// Nonce makes a repeated create (its answer was lost) return the same
	// session instead of a second one.
	Nonce string `json:"nonce"`
}

const maxCreate = 1 << 20

func (h *handler) create(w http.ResponseWriter, r *http.Request) {
	owner := ownerOf(r)
	if owner == "" {
		fail(w, http.StatusUnauthorized, "no_session")
		return
	}
	if r.ContentLength > maxCreate {
		fail(w, http.StatusRequestEntityTooLarge, "too_large")
		return
	}
	var in createBody
	dec := json.NewDecoder(io.LimitReader(r.Body, maxCreate+1))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		fail(w, http.StatusBadRequest, "bad_request")
		return
	}
	target, err := url.ParseRequestURI(in.URL)
	if err != nil || !strings.HasPrefix(in.URL, "/") || strings.HasPrefix(in.URL, "//") || Handles(target.Path) ||
		(in.Method != http.MethodPost && in.Method != http.MethodPut && in.Method != http.MethodPatch) ||
		(in.Mode != modeStream && in.Mode != modeStore) || len(in.Parts) == 0 || len(in.Parts) > 1024 || len(in.Nonce) > 64 {
		fail(w, http.StatusBadRequest, "bad_request")
		return
	}
	var files []fileSpec
	type planned struct {
		data []byte
		file int
	}
	plan := make([]planned, 0, len(in.Parts))
	for _, p := range in.Parts {
		switch {
		case p.Data != nil && p.File == nil:
			b, err := base64.StdEncoding.DecodeString(*p.Data)
			if err != nil {
				fail(w, http.StatusBadRequest, "bad_request")
				return
			}
			plan = append(plan, planned{data: b, file: -1})
		case p.File != nil && p.Data == nil && p.File.Size > 0 && len(p.File.Key) <= 512:
			plan = append(plan, planned{file: len(files)})
			files = append(files, *p.File)
		default:
			fail(w, http.StatusBadRequest, "bad_request")
			return
		}
	}
	if err := h.checkSession(r); err != nil {
		fail(w, http.StatusUnauthorized, "no_session")
		return
	}
	if s := h.m.byNonce(owner, in.Nonce); s != nil {
		h.created(w, s)
		return
	}
	dsmReq, err := http.NewRequest(in.Method, in.URL, nil)
	if err != nil {
		fail(w, http.StatusBadRequest, "bad_request")
		return
	}
	dsmReq.Host = r.Host
	for _, kv := range in.Headers {
		k := http.CanonicalHeaderKey(strings.TrimSpace(kv[0]))
		if k == "" || dropped[strings.ToLower(k)] || strings.ContainsAny(kv[1], "\r\n") {
			continue
		}
		dsmReq.Header.Add(k, kv[1])
	}
	for _, k := range []string{"Cookie", "User-Agent", "Origin", "Referer", "Accept-Language"} {
		if v := r.Header.Values(k); len(v) > 0 {
			dsmReq.Header[k] = append([]string(nil), v...)
		}
	}
	dsmReq.Header.Set("Accept-Encoding", "identity")

	blobs, err := h.m.reserve(owner, files)
	switch {
	case errors.Is(err, errBusy):
		fail(w, http.StatusTooManyRequests, "busy")
		return
	case errors.Is(err, errSpace):
		fail(w, http.StatusInsufficientStorage, "no_space")
		return
	case err != nil:
		fail(w, http.StatusInternalServerError, "unavailable")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &session{m: h.m, id: randomID(), owner: owner, nonce: in.Nonce, mode: in.Mode, files: blobs, request: dsmReq, forward: h.forward,
		sig: make(chan struct{}, 1), ctx: ctx, cancel: cancel, done: make(chan struct{})}
	for _, p := range plan {
		if p.file < 0 {
			s.parts = append(s.parts, part{data: p.data})
		} else {
			s.parts = append(s.parts, part{blob: blobs[p.file]})
		}
	}
	h.m.mu.Lock()
	if other := h.m.byNonceLocked(owner, in.Nonce); other != nil {
		// A repeat of this create won the race; keep that one.
		h.m.mu.Unlock()
		cancel()
		for _, b := range blobs {
			b.remove()
		}
		h.created(w, other)
		return
	}
	h.m.sessions[s.id] = s
	s.idle = time.AfterFunc(h.m.cfg.SessionIdle, s.expire)
	h.m.mu.Unlock()
	for _, b := range blobs {
		b.attach(s.poke)
	}
	go s.run()
	h.created(w, s)
}

// created answers a create with the session and the slots it already has.
func (h *handler) created(w http.ResponseWriter, s *session) {
	type fileOut struct {
		Have []int `json:"have"`
	}
	out := struct {
		ID    string    `json:"id"`
		Slot  int64     `json:"slot"`
		Files []fileOut `json:"files"`
	}{ID: s.id, Slot: h.m.cfg.Slot, Files: []fileOut{}}
	for _, b := range s.files {
		out.Files = append(out.Files, fileOut{Have: b.haveSlots()})
	}
	writeJSON(w, http.StatusCreated, out)
}

// checkSession asks DSM whether the cookie and token are a live session.
func (h *handler) checkSession(r *http.Request) error {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/webapi/entry.cgi?api=SYNO.Core.Desktop.Timeout&method=check&version=1", nil)
	if err != nil {
		return err
	}
	req.Host = r.Host
	req.Header.Set("Cookie", r.Header.Get("Cookie"))
	req.Header.Set("X-SYNO-TOKEN", r.Header.Get("X-SYNO-TOKEN"))
	req.Header.Set("Accept-Encoding", "identity")
	if ua := r.Header.Get("User-Agent"); ua != "" {
		req.Header.Set("User-Agent", ua)
	}
	res, err := h.forward(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	var v struct {
		Success bool `json:"success"`
	}
	if res.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(res.Body, 4096)).Decode(&v) != nil || !v.Success {
		return errors.New("no DSM session")
	}
	return nil
}

// own finds a session of the requesting DSM session.
func (h *handler) own(w http.ResponseWriter, r *http.Request) *session {
	owner := ownerOf(r)
	if owner == "" {
		fail(w, http.StatusUnauthorized, "no_session")
		return nil
	}
	s := h.m.session(r.PathValue("id"))
	if s == nil {
		fail(w, http.StatusNotFound, "unknown")
		return nil
	}
	if subtle.ConstantTimeCompare([]byte(owner), []byte(s.owner)) != 1 {
		fail(w, http.StatusForbidden, "forbidden")
		return nil
	}
	s.touch()
	return s
}

func (h *handler) cancel(w http.ResponseWriter, r *http.Request) {
	s := h.own(w, r)
	if s == nil {
		return
	}
	s.finish(stateCancelled, "", nil)
	s.drop()
	w.WriteHeader(http.StatusNoContent)
}

func stateName(st state) string {
	return [...]string{"pending", "done", "failed", "cancelled"}[st]
}

func (h *handler) result(w http.ResponseWriter, r *http.Request) {
	s := h.own(w, r)
	if s == nil {
		return
	}
	t := time.NewTimer(h.m.cfg.ResultWait)
	defer t.Stop()
	select {
	case <-s.done:
	case <-t.C:
	case <-r.Context().Done():
		return
	}
	st, code, a := s.state()
	switch st {
	case stateActive:
		writeJSON(w, http.StatusOK, map[string]any{"state": "pending", "received": s.received()})
	case stateDone:
		headers := [][2]string{}
		for k, vs := range a.Header {
			if k == "Set-Cookie" {
				for _, v := range vs {
					w.Header().Add("Set-Cookie", v)
				}
				continue
			}
			for _, v := range vs {
				headers = append(headers, [2]string{strings.ToLower(k), v})
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"state": "done", "status": a.Status, "statusText": a.StatusText,
			"headers": headers, "body": base64.StdEncoding.EncodeToString(a.Body)})
	default:
		writeJSON(w, http.StatusOK, map[string]any{"state": stateName(st), "code": code})
	}
}

// stopped answers a piece that cannot continue: 410 when the session ended,
// else 409 (another request took over the slot) or 507 (disk write failed).
func (h *handler) stopped(w http.ResponseWriter, s *session, reason string) {
	if st, code, _ := s.state(); st != stateActive {
		writeJSON(w, http.StatusGone, map[string]any{"state": stateName(st), "code": code})
		return
	}
	if reason == "write_failed" {
		fail(w, http.StatusInsufficientStorage, reason)
		return
	}
	fail(w, http.StatusConflict, reason)
}

// probe tells where the slot holding offset continues, so the page can tell
// a piece that arrived but whose answer was lost from one that did not.
func (h *handler) probe(w http.ResponseWriter, r *http.Request) {
	s := h.own(w, r)
	if s == nil {
		return
	}
	if st, code, _ := s.state(); st != stateActive {
		writeJSON(w, http.StatusGone, map[string]any{"state": stateName(st), "code": code})
		return
	}
	n, err := strconv.Atoi(r.PathValue("n"))
	off, err2 := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if err != nil || err2 != nil || n < 0 || n >= len(s.files) || off < 0 || off >= s.files[n].size {
		fail(w, http.StatusBadRequest, "bad_request")
		return
	}
	b := s.files[n]
	idx := int(off / h.m.cfg.Slot)
	b.mu.Lock()
	fill := int64(idx)*h.m.cfg.Slot + b.slots[idx].fill
	b.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]int64{"fill": fill})
}

var copyBuffers = sync.Pool{New: func() any { b := make([]byte, 256<<10); return &b }}

func (h *handler) put(w http.ResponseWriter, r *http.Request) {
	s := h.own(w, r)
	if s == nil {
		return
	}
	if st, code, _ := s.state(); st != stateActive {
		writeJSON(w, http.StatusGone, map[string]any{"state": stateName(st), "code": code})
		return
	}
	n, err := strconv.Atoi(r.PathValue("n"))
	off, err2 := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	if err != nil || err2 != nil || n < 0 || n >= len(s.files) {
		fail(w, http.StatusBadRequest, "bad_request")
		return
	}
	b := s.files[n]
	if off < 0 || off >= b.size {
		fail(w, http.StatusBadRequest, "bad_request")
		return
	}
	cfg := h.m.cfg
	idx := int(off / cfg.Slot)
	start := int64(idx) * cfg.Slot
	slotLen := b.slotLen(idx)
	if reuse := r.Header.Get("X-Upload-Reuse"); reuse != "" {
		want, err := hex.DecodeString(reuse)
		if err != nil || off != start || r.ContentLength > 0 {
			fail(w, http.StatusBadRequest, "bad_request")
			return
		}
		b.mu.Lock()
		sl := &b.slots[idx]
		ok := sl.fill == slotLen && sl.sum != nil && subtle.ConstantTimeCompare(sl.sum, want) == 1
		if ok {
			sl.verified = true
		} else if sl.fill == slotLen {
			b.resetLocked(idx)
		}
		fill := start + sl.fill
		b.mu.Unlock()
		if ok {
			s.poke()
			writeJSON(w, http.StatusOK, map[string]int64{"fill": fill})
		} else {
			writeJSON(w, http.StatusConflict, map[string]int64{"fill": fill})
		}
		return
	}
	if r.ContentLength <= 0 {
		fail(w, http.StatusLengthRequired, "length_required")
		return
	}
	if off+r.ContentLength > start+slotLen {
		fail(w, http.StatusBadRequest, "crosses_slot")
		return
	}
	b.mu.Lock()
	sl := &b.slots[idx]
	if !sl.verified && sl.fill == slotLen && off == start {
		b.resetLocked(idx) // a complete slot from before, sent again instead of confirmed
	}
	if start+sl.fill != off {
		fill := start + sl.fill
		b.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]int64{"fill": fill})
		return
	}
	if s.mode == modeStream && start > b.contiguousLocked()+cfg.Window {
		b.mu.Unlock()
		writeJSON(w, http.StatusConflict, map[string]bool{"wait": true})
		return
	}
	sl.gen++
	gen := sl.gen
	b.mu.Unlock()

	rc := http.NewResponseController(w)
	bufp := copyBuffers.Get().(*[]byte)
	defer copyBuffers.Put(bufp)
	buf := *bufp
	remaining := r.ContentLength
	pos := off
	for remaining > 0 {
		_ = rc.SetReadDeadline(time.Now().Add(cfg.ReadIdle))
		k, rerr := r.Body.Read(buf[:min(int64(len(buf)), remaining)])
		if k > 0 {
			b.mu.Lock()
			if sl.gen != gen {
				b.mu.Unlock()
				h.stopped(w, s, "superseded")
				return
			}
			if _, werr := b.f.WriteAt(buf[:k], pos); werr != nil {
				b.mu.Unlock()
				h.stopped(w, s, "write_failed")
				return
			}
			sl.h.Write(buf[:k])
			sl.fill += int64(k)
			b.written += int64(k)
			h.m.written.Add(int64(k))
			if sl.fill == slotLen {
				sl.sum, sl.verified = sl.h.Sum(nil), true
			}
			poke := b.poke
			b.mu.Unlock()
			if poke != nil {
				poke()
			}
			pos += int64(k)
			remaining -= int64(k)
			s.touch()
		}
		if rerr != nil && remaining > 0 {
			return // the client is gone; what arrived stays and the slot continues there
		}
	}
	_ = rc.SetReadDeadline(time.Time{})
	if st, code, _ := s.state(); st != stateActive && st != stateDone {
		writeJSON(w, http.StatusGone, map[string]any{"state": stateName(st), "code": code})
		return
	}
	writeJSON(w, http.StatusOK, map[string]int64{"fill": pos})
}
