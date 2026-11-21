package bigupload

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const slotSize = 1024

// fakeDSM answers the session check and records upload requests.
type fakeDSM struct {
	t        *testing.T
	mu       sync.Mutex
	requests []*http.Request
	bodies   [][]byte
	uploads  atomic.Int32
	opened   chan struct{} // closed when the first upload request opens
	once     sync.Once
	// upload handles an upload; default reads all and answers 200 JSON.
	upload func(req *http.Request) (*http.Response, error)
}

func newDSM(t *testing.T) *fakeDSM { return &fakeDSM{t: t, opened: make(chan struct{})} }

func jsonResponse(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Status: fmt.Sprintf("%d %s", code, http.StatusText(code)),
		Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
}

func (d *fakeDSM) forward(req *http.Request) (*http.Response, error) {
	if strings.Contains(req.URL.RawQuery, "SYNO.Core.Desktop.Timeout") {
		c, _ := req.Cookie("id")
		if c != nil && c.Value == "good" && req.Header.Get("X-SYNO-TOKEN") == "tok" {
			return jsonResponse(200, `{"success":true}`), nil
		}
		return jsonResponse(200, `{"error":{"code":119},"success":false}`), nil
	}
	d.uploads.Add(1)
	d.once.Do(func() { close(d.opened) })
	if d.upload != nil {
		return d.upload(req)
	}
	body, err := io.ReadAll(req.Body)
	d.mu.Lock()
	d.requests = append(d.requests, req)
	d.bodies = append(d.bodies, body)
	d.mu.Unlock()
	if err != nil {
		return nil, err
	}
	if int64(len(body)) != req.ContentLength {
		d.t.Errorf("body %d bytes, Content-Length %d", len(body), req.ContentLength)
	}
	res := jsonResponse(200, fmt.Sprintf(`{"success":true,"data":{"n":%d}}`, len(body)))
	res.Header.Add("Set-Cookie", "a=b")
	return res, nil
}

type rig struct {
	t     *testing.T
	m     *Manager
	dsm   *fakeDSM
	srv   *httptest.Server
	dir   string
	nonce string // sent with create when set
}

func newRig(t *testing.T, mod func(*Config)) *rig {
	dir := t.TempDir() + "/upload-tmp"
	cfg := Config{Dir: dir, Slot: slotSize, Free: func(string) (int64, error) { return 10 << 30, nil }}
	if mod != nil {
		mod(&cfg)
	}
	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	d := newDSM(t)
	srv := httptest.NewServer(m.Handler(d.forward))
	r := &rig{t: t, m: m, dsm: d, srv: srv, dir: dir}
	t.Cleanup(func() { srv.Close(); m.Close() })
	return r
}

func (r *rig) do(method, path string, body io.Reader, size int64, hdr map[string]string) (int, map[string]any, http.Header) {
	r.t.Helper()
	req, _ := http.NewRequest(method, r.srv.URL+path, body)
	if body != nil {
		req.ContentLength = size
	}
	req.Header.Set("Cookie", "id=good")
	req.Header.Set("X-SYNO-TOKEN", "tok")
	req.Header.Set("User-Agent", "test-browser")
	for k, v := range hdr {
		if v == "" {
			req.Header.Del(k)
		} else {
			req.Header.Set(k, v)
		}
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		r.t.Fatal(err)
	}
	defer res.Body.Close()
	out := map[string]any{}
	b, _ := io.ReadAll(res.Body)
	_ = json.Unmarshal(b, &out)
	return res.StatusCode, out, res.Header
}

func b64(s string) *string { v := base64.StdEncoding.EncodeToString([]byte(s)); return &v }

type createReq struct {
	Method  string      `json:"method"`
	URL     string      `json:"url"`
	Headers [][2]string `json:"headers"`
	Parts   []any       `json:"parts"`
	Mode    string      `json:"mode"`
	Nonce   string      `json:"nonce,omitempty"`
}

// create starts a session for prefix + file(size, key) + suffix.
func (r *rig) create(mode, key string, file []byte, hdr map[string]string) (int, map[string]any) {
	r.t.Helper()
	c := createReq{Method: "POST", URL: "/webapi/entry.cgi?api=SYNO.FileStation.Upload&method=upload&version=2",
		Headers: [][2]string{{"Content-Type", "multipart/form-data; boundary=xx"}, {"X-SYNO-TOKEN", "tok"}, {"Cookie", "id=evil"}, {"X-App", "1"}},
		Parts: []any{map[string]any{"data": b64("--xx\r\nhead\r\n\r\n")}, map[string]any{"file": map[string]any{"key": key, "size": len(file)}},
			map[string]any{"data": b64("\r\n--xx--\r\n")}}, Mode: mode, Nonce: r.nonce}
	j, _ := json.Marshal(c)
	code, out, _ := r.do("POST", "/_connect/upload/sessions", bytes.NewReader(j), int64(len(j)), hdr)
	return code, out
}

func (r *rig) put(id string, off int64, data []byte) (int, map[string]any) {
	r.t.Helper()
	code, out, _ := r.do("PUT", fmt.Sprintf("/_connect/upload/sessions/%s/files/0?offset=%d", id, off), bytes.NewReader(data), int64(len(data)), nil)
	return code, out
}

func (r *rig) result(id string) map[string]any {
	r.t.Helper()
	for i := 0; i < 50; i++ {
		_, out, _ := r.do("GET", "/_connect/upload/sessions/"+id+"/result", nil, 0, nil)
		if out["state"] != "pending" {
			return out
		}
	}
	r.t.Fatal("no result")
	return nil
}

func (r *rig) waitUsage(reserved, written int64, retained int) {
	r.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		res, wr, _, ret := r.m.Usage()
		if res == reserved && wr == written && ret == retained {
			return
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("usage reserved=%d written=%d retained=%d, want %d %d %d", res, wr, ret, reserved, written, retained)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (r *rig) tempFiles() int {
	ents, _ := os.ReadDir(r.dir)
	return len(ents)
}

func data(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7 + i/251)
	}
	return b
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func wantBody(file []byte) []byte {
	return append(append([]byte("--xx\r\nhead\r\n\r\n"), file...), "\r\n--xx--\r\n"...)
}

func TestUploadInOrderReachesDSMOnce(t *testing.T) {
	r := newRig(t, nil)
	file := data(3*slotSize + 100)
	code, out := r.create("stream", "k1", file, nil)
	if code != 201 {
		t.Fatalf("create %d %v", code, out)
	}
	id := out["id"].(string)
	if out["slot"].(float64) != slotSize {
		t.Fatalf("slot %v", out["slot"])
	}
	for off := 0; off < len(file); off += slotSize {
		end := min(off+slotSize, len(file))
		if c, o := r.put(id, int64(off), file[off:end]); c != 200 || o["fill"].(float64) != float64(end) {
			t.Fatalf("put %d: %d %v", off, c, o)
		}
	}
	res := r.result(id)
	if res["state"] != "done" || res["status"].(float64) != 200 {
		t.Fatalf("result %v", res)
	}
	body, _ := base64.StdEncoding.DecodeString(res["body"].(string))
	if !strings.Contains(string(body), fmt.Sprint(len(wantBody(file)))) {
		t.Fatalf("answer %s", body)
	}
	if r.dsm.uploads.Load() != 1 {
		t.Fatalf("DSM saw %d uploads", r.dsm.uploads.Load())
	}
	got := r.dsm.bodies[0]
	if !bytes.Equal(got, wantBody(file)) {
		t.Fatalf("DSM body differs")
	}
	req := r.dsm.requests[0]
	if req.Header.Get("Cookie") != "id=good" || req.Header.Get("X-App") != "1" || req.Header.Get("User-Agent") != "test-browser" ||
		req.Header.Get("Content-Type") != "multipart/form-data; boundary=xx" || req.Method != "POST" ||
		req.URL.RequestURI() != "/webapi/entry.cgi?api=SYNO.FileStation.Upload&method=upload&version=2" {
		t.Fatalf("DSM request %s %s %v", req.Method, req.URL, req.Header)
	}
	r.waitUsage(0, 0, 0)
	if r.tempFiles() != 0 {
		t.Fatalf("temp files left")
	}
}

func TestResultCarriesSetCookieAndCanBeFetchedAgain(t *testing.T) {
	r := newRig(t, nil)
	file := data(100)
	_, out := r.create("stream", "k", file, nil)
	id := out["id"].(string)
	r.put(id, 0, file)
	_, res, hdr := r.do("GET", "/_connect/upload/sessions/"+id+"/result", nil, 0, nil)
	if res["state"] != "done" || hdr.Get("Set-Cookie") != "a=b" {
		t.Fatalf("result %v %v", res, hdr)
	}
	for _, kv := range res["headers"].([]any) {
		if strings.EqualFold(kv.([]any)[0].(string), "set-cookie") {
			t.Fatal("Set-Cookie leaked into JSON headers")
		}
	}
	if again := r.result(id); again["state"] != "done" {
		t.Fatalf("second fetch %v", again)
	}
}

func TestOutOfOrderPiecesAreWrittenInOrder(t *testing.T) {
	r := newRig(t, nil)
	file := data(3 * slotSize)
	_, out := r.create("stream", "k", file, nil)
	id := out["id"].(string)
	if c, _ := r.put(id, slotSize, file[slotSize:2*slotSize]); c != 200 {
		t.Fatalf("slot 1 first: %d", c)
	}
	select {
	case <-r.dsm.opened:
		t.Fatal("DSM opened before the first byte")
	case <-time.After(50 * time.Millisecond):
	}
	r.put(id, 2*slotSize, file[2*slotSize:])
	r.put(id, 0, file[:slotSize])
	if res := r.result(id); res["state"] != "done" {
		t.Fatalf("%v", res)
	}
	if !bytes.Equal(r.dsm.bodies[0], wantBody(file)) {
		t.Fatal("body out of order")
	}
}

func TestPieceTooFarAheadWaits(t *testing.T) {
	r := newRig(t, nil)
	file := data(5 * slotSize)
	_, out := r.create("stream", "k", file, nil)
	id := out["id"].(string)
	if c, o := r.put(id, 3*slotSize, file[3*slotSize:4*slotSize]); c != 409 || o["wait"] != true {
		t.Fatalf("far ahead: %d %v", c, o)
	}
	if c, _ := r.put(id, 2*slotSize, file[2*slotSize:3*slotSize]); c != 200 {
		t.Fatalf("within window: %d", c)
	}
}

func TestStoreModeNeedsNoOrderAndOpensDSMAtTheEnd(t *testing.T) {
	r := newRig(t, nil)
	file := data(5 * slotSize)
	_, out := r.create("store", "k", file, nil)
	id := out["id"].(string)
	for _, s := range []int{4, 3, 0, 2} {
		if c, _ := r.put(id, int64(s*slotSize), file[s*slotSize:(s+1)*slotSize]); c != 200 {
			t.Fatalf("slot %d: %d", s, c)
		}
	}
	select {
	case <-r.dsm.opened:
		t.Fatal("store mode opened DSM before every byte arrived")
	case <-time.After(50 * time.Millisecond):
	}
	r.put(id, slotSize, file[slotSize:2*slotSize])
	if res := r.result(id); res["state"] != "done" || !bytes.Equal(r.dsm.bodies[0], wantBody(file)) {
		t.Fatalf("%v", res)
	}
}

func TestStreamOpensDSMBeforeTheLastPiece(t *testing.T) {
	r := newRig(t, nil)
	file := data(2 * slotSize)
	_, out := r.create("stream", "k", file, nil)
	id := out["id"].(string)
	r.put(id, 0, file[:slotSize])
	select {
	case <-r.dsm.opened:
	case <-time.After(2 * time.Second):
		t.Fatal("DSM not opened while pieces arrive")
	}
	r.put(id, slotSize, file[slotSize:])
	if res := r.result(id); res["state"] != "done" {
		t.Fatalf("%v", res)
	}
}

func TestPiecesWithinASlotContinueWhereTheSlotStopped(t *testing.T) {
	r := newRig(t, nil)
	file := data(slotSize)
	_, out := r.create("stream", "k", file, nil)
	id := out["id"].(string)
	if c, o := r.put(id, 0, file[:300]); c != 200 || o["fill"].(float64) != 300 {
		t.Fatalf("%d %v", c, o)
	}
	if c, o := r.put(id, 200, file[200:400]); c != 409 || o["fill"].(float64) != 300 {
		t.Fatalf("overlap: %d %v", c, o)
	}
	if c, o := r.put(id, 300, append(append([]byte(nil), file[300:]...), 1, 2)); c != 400 {
		t.Fatalf("crossing the slot end: %d %v", c, o)
	}
	if c, _ := r.put(id, 300, file[300:]); c != 200 {
		t.Fatalf("continue: %d", c)
	}
	if res := r.result(id); res["state"] != "done" || !bytes.Equal(r.dsm.bodies[0], wantBody(file)) {
		t.Fatalf("%v", res)
	}
}

// A stalled piece is replaced by a new request: the old one stops writing.
func TestNewRequestTakesOverAStalledPiece(t *testing.T) {
	r := newRig(t, nil)
	file := data(slotSize)
	_, out := r.create("stream", "k", file, nil)
	id := out["id"].(string)
	pr, pw := io.Pipe()
	stalled := make(chan int, 1)
	go func() {
		req, _ := http.NewRequest("PUT", r.srv.URL+"/_connect/upload/sessions/"+id+"/files/0?offset=0", pr)
		req.ContentLength = slotSize
		req.Header.Set("Cookie", "id=good")
		req.Header.Set("X-SYNO-TOKEN", "tok")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			stalled <- 0 // the connection ended: no success either
			return
		}
		res.Body.Close()
		stalled <- res.StatusCode
	}()
	_, _ = pw.Write(file[:100])
	r.waitUsage(slotSize, 100, 0)
	// The fresh request learns where the slot continues.
	if c, o := r.put(id, 0, file); c != 409 || o["fill"].(float64) != 100 {
		t.Fatalf("takeover probe %d %v", c, o)
	}
	if c, o := r.put(id, 100, file[100:]); c != 200 {
		t.Fatalf("takeover %d %v", c, o)
	}
	go func() { _, _ = pw.Write(file[100:200]); pw.Close() }() // the old request wakes up and must not write
	if c := <-stalled; c == 200 {
		t.Fatal("stalled request reported success")
	}
	if res := r.result(id); res["state"] != "done" || !bytes.Equal(r.dsm.bodies[0], wantBody(file)) {
		t.Fatalf("%v", res)
	}
}

func TestBrokenDSMStreamFailsAndDataIsReusedWithoutReplay(t *testing.T) {
	r := newRig(t, nil)
	file := data(3 * slotSize)
	var calls atomic.Int32
	r.dsm.upload = func(req *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			buf := make([]byte, slotSize+500)
			_, _ = io.ReadFull(req.Body, buf)
			return nil, errors.New("connection reset") // nginx ended an idle body
		}
		body, err := io.ReadAll(req.Body)
		if err != nil || !bytes.Equal(body, wantBody(file)) {
			t.Errorf("second request body wrong (%v)", err)
		}
		return jsonResponse(200, `{"success":true}`), nil
	}
	_, out := r.create("stream", "k", file, nil)
	id := out["id"].(string)
	r.put(id, 0, file[:slotSize])
	r.put(id, slotSize, file[slotSize:2*slotSize])
	res := r.result(id)
	if res["state"] != "failed" || res["code"] != "stream_broken" {
		t.Fatalf("%v", res)
	}
	if calls.Load() != 1 {
		t.Fatalf("stored body sent again: %d", calls.Load())
	}
	if c, o := r.put(id, 2*slotSize, file[2*slotSize:]); c != 410 {
		t.Fatalf("piece after failure %d %v", c, o)
	}
	r.waitUsage(int64(len(file)), 2*slotSize, 1)

	// The App sends again: a new session offers the two complete slots.
	_, out = r.create("stream", "k", file, nil)
	id2 := out["id"].(string)
	have := out["files"].([]any)[0].(map[string]any)["have"].([]any)
	if len(have) != 2 {
		t.Fatalf("have %v", have)
	}
	reuse := func(off int64, h string) (int, map[string]any) {
		c, o, _ := r.do("PUT", fmt.Sprintf("/_connect/upload/sessions/%s/files/0?offset=%d", id2, off), nil, 0, map[string]string{"X-Upload-Reuse": h})
		return c, o
	}
	if c, _ := reuse(0, sum(file[:slotSize])); c != 200 {
		t.Fatalf("reuse slot 0: %d", c)
	}
	// A slot whose bytes differ is not reused, and must be sent.
	if c, o := reuse(slotSize, sum([]byte("other"))); c != 409 || o["fill"].(float64) != slotSize {
		t.Fatalf("mismatch: %d %v", c, o)
	}
	r.put(id2, slotSize, file[slotSize:2*slotSize])
	r.put(id2, 2*slotSize, file[2*slotSize:])
	if res := r.result(id2); res["state"] != "done" {
		t.Fatalf("%v", res)
	}
	if calls.Load() != 2 {
		t.Fatalf("DSM requests %d", calls.Load())
	}
	r.waitUsage(0, 0, 0)
	if r.tempFiles() != 0 {
		t.Fatal("temp files left")
	}
}

// An unconfirmed retained slot is never streamed: DSM waits for confirmation.
func TestRetainedSlotIsNotStreamedUntilConfirmed(t *testing.T) {
	r := newRig(t, nil)
	file := data(2 * slotSize)
	first := true
	r.dsm.upload = func(req *http.Request) (*http.Response, error) {
		if first {
			first = false
			return nil, errors.New("reset")
		}
		body, _ := io.ReadAll(req.Body)
		if !bytes.Equal(body, wantBody(file)) {
			t.Error("wrong body")
		}
		return jsonResponse(200, `{}`), nil
	}
	_, out := r.create("stream", "k", file, nil)
	id := out["id"].(string)
	stale := append([]byte(nil), file[:slotSize]...)
	stale[0] ^= 0xff // the old data differs from the file sent now
	r.put(id, 0, stale)
	r.result(id)
	_, out = r.create("stream", "k", file, nil)
	id2 := out["id"].(string)
	r.put(id2, slotSize, file[slotSize:])
	time.Sleep(50 * time.Millisecond)
	// The page found the hash differs and sends slot 0 again.
	if c, _ := r.put(id2, 0, file[:slotSize]); c != 200 {
		t.Fatalf("resend %d", c)
	}
	if res := r.result(id2); res["state"] != "done" {
		t.Fatalf("%v", res)
	}
}

func TestCancelEndsDSMRequestAndDeletesData(t *testing.T) {
	r := newRig(t, nil)
	file := data(3 * slotSize)
	ended := make(chan error, 1)
	r.dsm.upload = func(req *http.Request) (*http.Response, error) {
		_, err := io.ReadAll(req.Body)
		ended <- err
		return nil, err
	}
	_, out := r.create("stream", "k", file, nil)
	id := out["id"].(string)
	r.put(id, 0, file[:slotSize])
	<-r.dsm.opened
	if c, _, _ := r.do("DELETE", "/_connect/upload/sessions/"+id, nil, 0, nil); c != 204 {
		t.Fatalf("cancel %d", c)
	}
	select {
	case err := <-ended:
		if err == nil {
			t.Fatal("DSM body completed after cancel")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("DSM request still open")
	}
	r.waitUsage(0, 0, 0)
	if r.tempFiles() != 0 {
		t.Fatal("temp files left")
	}
	if c, _ := r.put(id, slotSize, file[slotSize:2*slotSize]); c != 404 {
		t.Fatalf("piece after cancel %d", c)
	}
}

func TestDSMAnswerBeforeTheEndIsFinal(t *testing.T) {
	r := newRig(t, nil)
	file := data(3 * slotSize)
	r.dsm.upload = func(req *http.Request) (*http.Response, error) {
		buf := make([]byte, 10)
		_, _ = io.ReadFull(req.Body, buf)
		return jsonResponse(200, `{"error":{"code":414},"success":false}`), nil
	}
	_, out := r.create("stream", "k", file, nil)
	id := out["id"].(string)
	r.put(id, 0, file[:slotSize])
	res := r.result(id)
	body, _ := base64.StdEncoding.DecodeString(res["body"].(string))
	if res["state"] != "done" || !strings.Contains(string(body), "414") {
		t.Fatalf("%v", res)
	}
	if c, o := r.put(id, slotSize, file[slotSize:2*slotSize]); c != 410 || o["state"] != "done" {
		t.Fatalf("piece after answer %d %v", c, o)
	}
	r.waitUsage(0, 0, 0)
}

func TestSessionCheck(t *testing.T) {
	r := newRig(t, nil)
	file := data(10)
	for name, hdr := range map[string]map[string]string{
		"no cookie":    {"Cookie": ""},
		"no token":     {"X-SYNO-TOKEN": ""},
		"dead session": {"Cookie": "id=expired"},
		"wrong token":  {"X-SYNO-TOKEN": "other"},
	} {
		if c, _ := r.create("stream", "k", file, hdr); c != 401 {
			t.Errorf("%s: %d", name, c)
		}
	}
	r.waitUsage(0, 0, 0)
	if r.tempFiles() != 0 {
		t.Fatal("stored data without a session")
	}
}

func TestOtherSessionCannotTouchUpload(t *testing.T) {
	r := newRig(t, nil)
	file := data(slotSize)
	_, out := r.create("stream", "k", file, nil)
	id := out["id"].(string)
	other := map[string]string{"Cookie": "id=someone-else"}
	if c, _, _ := r.do("PUT", "/_connect/upload/sessions/"+id+"/files/0?offset=0", bytes.NewReader(file), slotSize, other); c != 403 {
		t.Fatalf("put %d", c)
	}
	if c, _, _ := r.do("GET", "/_connect/upload/sessions/"+id+"/result", nil, 0, other); c != 403 {
		t.Fatalf("result %d", c)
	}
	if c, _, _ := r.do("DELETE", "/_connect/upload/sessions/"+id, nil, 0, other); c != 403 {
		t.Fatalf("delete %d", c)
	}
	if c, _, _ := r.do("DELETE", "/_connect/upload/sessions/"+id, nil, 0, map[string]string{"X-SYNO-TOKEN": "x"}); c != 403 {
		t.Fatalf("same cookie, other token %d", c)
	}
	if c, _, _ := r.do("GET", "/_connect/upload/sessions/nope/result", nil, 0, nil); c != 404 {
		t.Fatalf("unknown %d", c)
	}
}

// Data of a failed session belongs to its DSM session only.
func TestRetainedDataIsNotOfferedToAnotherSession(t *testing.T) {
	r := newRig(t, nil)
	file := data(slotSize)
	r.dsm.upload = func(*http.Request) (*http.Response, error) { return nil, errors.New("reset") }
	_, out := r.create("stream", "k", file, nil)
	r.put(out["id"].(string), 0, file)
	r.result(out["id"].(string))
	owner2 := ownerOf(&http.Request{Header: http.Header{"Cookie": {"id=other"}, "X-Syno-Token": {"tok"}}})
	if _, err := r.m.reserve(owner2, []fileSpec{{Key: "k", Size: slotSize}}); err != nil {
		t.Fatal(err)
	}
	r.m.mu.Lock()
	n := len(r.m.retained)
	r.m.mu.Unlock()
	if n != 1 {
		t.Fatalf("another session took the retained data: %d left", n)
	}
}

func TestNoSpaceIsRefusedWithoutReserving(t *testing.T) {
	var free atomic.Int64
	free.Store(3 << 30)
	r := newRig(t, func(c *Config) { c.Free = func(string) (int64, error) { return free.Load(), nil } })
	r.m.cfg.Margin = 1 << 30
	file := data(slotSize)
	if c, _ := r.create("stream", "a", file, nil); c != 201 {
		t.Fatalf("fits: %d", c)
	}
	free.Store(1<<30 + slotSize) // only the margin and the first upload's reservation are left
	if c, o := r.create("stream", "b", file, nil); c != 507 || o["error"] != "no_space" {
		t.Fatalf("no space: %d %v", c, o)
	}
	r.waitUsage(slotSize, 0, 0)
}

func TestSessionLimits(t *testing.T) {
	r := newRig(t, func(c *Config) { c.MaxPerOwner = 2; c.MaxTotal = 3 })
	file := data(10)
	r.create("stream", "a", file, nil)
	_, out := r.create("stream", "b", file, nil)
	if c, _ := r.create("stream", "c", file, nil); c != 429 {
		t.Fatalf("per session limit %d", c)
	}
	// An ended session frees its place.
	r.do("DELETE", "/_connect/upload/sessions/"+out["id"].(string), nil, 0, nil)
	if c, _ := r.create("stream", "c", file, nil); c != 201 {
		t.Fatalf("after cancel %d", c)
	}
}

// A create whose answer was lost is sent again with the same nonce and gets the
// same session: no second reservation, and the bytes already stored count.
func TestRepeatedCreateReturnsTheSameSession(t *testing.T) {
	r := newRig(t, nil)
	file := data(slotSize + 10)
	r.nonce = "n1"
	c1, out1 := r.create("stream", "k", file, nil)
	if c1 != 201 {
		t.Fatalf("create %d", c1)
	}
	id := out1["id"].(string)
	if c, _ := r.put(id, 0, file[:slotSize]); c != 200 {
		t.Fatalf("put %d", c)
	}
	c2, out2 := r.create("stream", "k", file, nil)
	if c2 != 201 || out2["id"] != id {
		t.Fatalf("repeat %d %v, want %s", c2, out2["id"], id)
	}
	if have := out2["files"].([]any)[0].(map[string]any)["have"].([]any); len(have) != 1 || have[0].(float64) != 0 {
		t.Fatalf("have %v", have)
	}
	r.waitUsage(int64(len(file)), slotSize, 0)
	// Another DSM session with the same nonce gets its own session.
	_, out3 := r.create("stream", "k", file, map[string]string{"Cookie": "id=someone-else"})
	if out3["id"] == id {
		t.Fatal("nonce crossed DSM sessions")
	}
	// Without a nonce every create is new.
	r.nonce = ""
	_, out4 := r.create("stream", "k2", file, nil)
	if out4["id"] == id {
		t.Fatal("empty nonce matched")
	}
}

func TestIdleSessionExpiresAndRetainedDataIsDeleted(t *testing.T) {
	r := newRig(t, func(c *Config) {
		c.SessionIdle = 100 * time.Millisecond
		c.KeepFailed = 200 * time.Millisecond
		c.EndedKeep = 50 * time.Millisecond
	})
	file := data(2 * slotSize)
	_, out := r.create("store", "k", file, nil)
	r.put(out["id"].(string), 0, file[:slotSize])
	r.waitUsage(int64(len(file)), slotSize, 1) // the tab went away: data waits for reuse
	r.waitUsage(0, 0, 0)
	if _, _, s, _ := r.m.Usage(); s != 0 {
		t.Fatalf("sessions %d", s)
	}
	if r.tempFiles() != 0 {
		t.Fatal("temp files left")
	}
}

func TestBadRequests(t *testing.T) {
	r := newRig(t, nil)
	for name, body := range map[string]string{
		"absolute url": `{"method":"POST","url":"https://x/y","parts":[{"data":""}],"mode":"stream"}`,
		"own path":     `{"method":"POST","url":"/_connect/upload/sessions","parts":[{"data":""}],"mode":"stream"}`,
		"GET":          `{"method":"GET","url":"/webapi/entry.cgi","parts":[{"data":""}],"mode":"stream"}`,
		"mode":         `{"method":"POST","url":"/x","parts":[{"data":""}],"mode":"fast"}`,
		"no parts":     `{"method":"POST","url":"/x","parts":[],"mode":"stream"}`,
		"unknown":      `{"method":"POST","url":"/x","parts":[{"data":""}],"mode":"stream","extra":1}`,
		"empty file":   `{"method":"POST","url":"/x","parts":[{"file":{"key":"k","size":0}}],"mode":"stream"}`,
	} {
		if c, _, _ := r.do("POST", "/_connect/upload/sessions", strings.NewReader(body), int64(len(body)), nil); c != 400 {
			t.Errorf("%s: %d", name, c)
		}
	}
	file := data(slotSize)
	_, out := r.create("stream", "k", file, nil)
	id := out["id"].(string)
	if c, _, _ := r.do("PUT", "/_connect/upload/sessions/"+id+"/files/1?offset=0", bytes.NewReader(file), slotSize, nil); c != 400 {
		t.Errorf("file index %d", c)
	}
	if c, _, _ := r.do("PUT", "/_connect/upload/sessions/"+id+"/files/0?offset=-1", bytes.NewReader(file), slotSize, nil); c != 400 {
		t.Errorf("offset %d", c)
	}
}

func TestStartEmptiesTheTempDirectory(t *testing.T) {
	dir := t.TempDir() + "/upload-tmp"
	_ = os.MkdirAll(dir, 0o700)
	_ = os.WriteFile(dir+"/leftover", []byte("x"), 0o600)
	m, err := New(Config{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Fatal("leftover kept")
	}
	if st, _ := os.Stat(dir); st.Mode().Perm() != 0o700 {
		t.Fatalf("mode %v", st.Mode())
	}
}

func TestHandles(t *testing.T) {
	for p, want := range map[string]bool{"/_connect/upload.js": true, "/_connect/upload/sessions": true, "/_connect/upload/sessions/a/result": true,
		"/_connect/uploads": false, "/webapi/entry.cgi": false, "/_connect/login-resume.js": false} {
		if Handles(p) != want {
			t.Errorf("%s", p)
		}
	}
}

// A piece whose answer was lost is found complete by a probe, not sent again.
func TestProbeTellsWhereTheSlotContinues(t *testing.T) {
	r := newRig(t, nil)
	file := data(2 * slotSize)
	_, out := r.create("stream", "k", file, nil)
	id := out["id"].(string)
	probe := func(off int64) (int, map[string]any) {
		c, o, _ := r.do("GET", fmt.Sprintf("/_connect/upload/sessions/%s/files/0?offset=%d", id, off), nil, 0, nil)
		return c, o
	}
	if c, o := probe(slotSize + 5); c != 200 || o["fill"].(float64) != slotSize {
		t.Fatalf("empty slot: %d %v", c, o)
	}
	r.put(id, 0, file[:600])
	if c, o := probe(0); c != 200 || o["fill"].(float64) != 600 {
		t.Fatalf("partial: %d %v", c, o)
	}
	if c, _, _ := r.do("GET", fmt.Sprintf("/_connect/upload/sessions/%s/files/0?offset=0", id), nil, 0, map[string]string{"Cookie": "id=x"}); c != 403 {
		t.Fatalf("other session %d", c)
	}
}
