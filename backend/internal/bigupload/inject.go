package bigupload

import (
	"bytes"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
)

// upload.js is built from frontend/src/upload/wrapper.ts by
// `pnpm --dir frontend run build:upload`; a frontend test fails when stale.
//
//go:embed upload.js
var script []byte

var scriptVersion = func() string {
	h := sha256.Sum256(script)
	return hex.EncodeToString(h[:6])
}()

var scriptTag = []byte(`<script src="` + ScriptPath + `?v=` + scriptVersion + `"></script>`)

func serveScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.URL.Query().Get("v") == scriptVersion {
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		w.Header().Set("Cache-Control", "no-cache")
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(script)))
	_, _ = w.Write(script)
}

// Navigation reports whether a request loads a page or frame, whose HTML
// response may receive the script; such requests ask DSM for identity encoding.
func Navigation(r *http.Request) bool {
	if r.Method != http.MethodGet {
		return false
	}
	switch r.Header.Get("Sec-Fetch-Dest") {
	case "document", "iframe", "frame":
		return true
	case "":
		return strings.HasPrefix(r.Header.Get("Accept"), "text/html")
	}
	return false
}

const maxPage = 4 << 20

// Inject adds the script right after <head> of an HTML page. Anything it
// cannot handle safely (compressed, too large, no <head>) passes unchanged.
func Inject(res *http.Response) error {
	if res.Request == nil || res.Request.Method != http.MethodGet || res.StatusCode != http.StatusOK ||
		res.Header.Get("Content-Encoding") != "" || res.Body == nil || res.Body == http.NoBody {
		return nil
	}
	if ct, _, _ := mime.ParseMediaType(res.Header.Get("Content-Type")); ct != "text/html" {
		return nil
	}
	if res.ContentLength > maxPage {
		return nil
	}
	head, err := io.ReadAll(io.LimitReader(res.Body, maxPage+1))
	if err != nil {
		return err
	}
	if len(head) > maxPage {
		res.Body = struct {
			io.Reader
			io.Closer
		}{io.MultiReader(bytes.NewReader(head), res.Body), res.Body}
		return nil
	}
	_ = res.Body.Close()
	out := head
	if at := headEnd(head); at >= 0 {
		out = make([]byte, 0, len(head)+len(scriptTag))
		out = append(append(append(out, head[:at]...), scriptTag...), head[at:]...)
		// The page is no longer DSM's representation; its validators no longer apply.
		res.Header.Del("ETag")
		res.Header.Del("Last-Modified")
	}
	res.Body = io.NopCloser(bytes.NewReader(out))
	res.ContentLength = int64(len(out))
	res.Header.Set("Content-Length", strconv.Itoa(len(out)))
	return nil
}

// headEnd returns the index just after the <head ...> start tag, or -1.
func headEnd(b []byte) int {
	lower := bytes.ToLower(b)
	for from := 0; ; {
		i := bytes.Index(lower[from:], []byte("<head"))
		if i < 0 {
			return -1
		}
		i += from
		next := i + 5
		if next < len(lower) && (lower[next] == '>' || lower[next] == ' ' || lower[next] == '\t' || lower[next] == '\n' || lower[next] == '\r') {
			end := bytes.IndexByte(lower[next:], '>')
			if end < 0 {
				return -1
			}
			return next + end + 1
		}
		from = next
	}
}
