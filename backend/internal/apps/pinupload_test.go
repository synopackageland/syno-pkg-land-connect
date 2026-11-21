package apps

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"dsmapp/internal/bigupload"
)

// A public DSM binding injects the upload script into DSM pages and carries
// a large upload as pieces into one request to DSM; other bindings do neither.
func TestPublicDSMCarriesLargeUploads(t *testing.T) {
	for _, provider := range []string{"cloudflared", "tailscale"} {
		t.Run(provider, func(t *testing.T) {
			l := newPinLab(t)
			l.app.Close()
			var got atomic.Pointer[[]byte]
			l.app = &LabServer{Name: "dsm"}
			l.app.SetCert(l.leaf(l.key, approvedNames, true))
			l.app.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/":
					w.Header().Set("Content-Type", "text/html; charset=utf-8")
					io.WriteString(w, "<!DOCTYPE html><html><head><title>DSM</title></head></html>")
				case r.URL.Query().Get("api") == "SYNO.Core.Desktop.Timeout":
					ok := r.Header.Get("Cookie") == "id=sess" && r.Header.Get("X-SYNO-TOKEN") == "tok"
					fmt.Fprintf(w, `{"success":%v}`, ok)
				case r.URL.Query().Get("api") == "SYNO.FileStation.Upload":
					b, _ := io.ReadAll(r.Body)
					got.Store(&b)
					fmt.Fprintf(w, `{"success":true,"host":%q,"len":%d}`, r.Host, len(b))
				default:
					http.NotFound(w, r)
				}
			})
			var err error
			l.ap, err = l.app.Start("127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(l.app.Close)
			b := l.binding()
			b.Target, b.Package = TargetDSM, ""
			host := "nas.example.com"
			b.Authority = host
			if provider == "tailscale" {
				b.Authority = fmt.Sprintf("nas.example.com:%d", l.ap)
				host = b.Authority
			}
			m, err := bigupload.New(bigupload.Config{Dir: t.TempDir() + "/upload-tmp", Slot: 1024, Free: func(string) (int64, error) { return 10 << 30, nil }})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(m.Close)
			r := startPin(t, l, b, provider, func(c *PinConfig) { c.Uploads = m })
			do := func(method, path string, body []byte) (int, string) {
				req, _ := http.NewRequest(method, "http://localhost"+path, bytes.NewReader(body))
				req.Host = host
				req.Header.Set("Cookie", "id=sess")
				req.Header.Set("X-SYNO-TOKEN", "tok")
				req.Header.Set("Sec-Fetch-Dest", "document")
				res, err := r.client.Do(req)
				if err != nil {
					t.Fatal(err)
				}
				defer res.Body.Close()
				out, _ := io.ReadAll(res.Body)
				return res.StatusCode, string(out)
			}
			code, page := do("GET", "/", nil)
			injected := strings.Contains(page, `<head><script src="/_connect/upload.js?v=`)
			if code != 200 || injected != (provider == "cloudflared") {
				t.Fatalf("page %d injected=%v: %s", code, injected, page)
			}
			code, _ = do("GET", "/_connect/upload.js", nil)
			if provider == "tailscale" {
				if code != 404 {
					t.Fatalf("script on a private binding: %d", code)
				}
				return
			}
			if code != 200 {
				t.Fatalf("script %d", code)
			}
			file := bytes.Repeat([]byte("0123456789abcdef"), 150) // 2400 bytes, 3 slots
			create, _ := json.Marshal(map[string]any{"method": "POST", "url": "/webapi/entry.cgi?api=SYNO.FileStation.Upload&method=upload&version=2", "mode": "stream",
				"headers": [][2]string{{"Content-Type", "application/octet-stream"}}, "parts": []any{map[string]any{"file": map[string]any{"key": "f", "size": len(file)}}}})
			code, body := do("POST", "/_connect/upload/sessions", create)
			if code != 201 {
				t.Fatalf("create %d %s", code, body)
			}
			var s struct{ ID string }
			_ = json.Unmarshal([]byte(body), &s)
			for off := 0; off < len(file); off += 1024 {
				if code, body := do("PUT", fmt.Sprintf("/_connect/upload/sessions/%s/files/0?offset=%d", s.ID, off), file[off:min(off+1024, len(file))]); code != 200 {
					t.Fatalf("put %d %s", code, body)
				}
			}
			code, body = do("GET", "/_connect/upload/sessions/"+s.ID+"/result", nil)
			var res struct {
				State  string
				Status int
				Body   []byte
			}
			if err := json.Unmarshal([]byte(body), &res); err != nil || code != 200 || res.State != "done" || res.Status != 200 {
				t.Fatalf("result %d %s", code, body)
			}
			if p := got.Load(); p == nil || !bytes.Equal(*p, file) {
				t.Fatal("DSM did not receive the file")
			}
			if !strings.Contains(string(res.Body), `"host":"nas.example.com"`) {
				t.Fatalf("DSM answer %s", res.Body)
			}
		})
	}
}
