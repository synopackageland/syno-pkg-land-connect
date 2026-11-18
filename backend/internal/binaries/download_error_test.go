package binaries

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"syscall"
	"testing"
)

func TestDownloadFailurePreservesReadableCauseWithoutSignedURL(t *testing.T) {
	for _, row := range []struct {
		name      string
		status    int
		transport error
		writer    bool
		want      string
	}{
		{name: "registry rate limit", status: 429, want: "ghcr.io returned HTTP 429"},
		{name: "registry unavailable", transport: errors.New("dial tcp: network is unreachable"), want: "network is unreachable"},
		{name: "disk full", status: 200, writer: true, want: "no space left on device"},
	} {
		t.Run(row.name, func(t *testing.T) {
			s := NewOfficialSource()
			s.client = &http.Client{Transport: nbTransport(func(*http.Request) (*http.Response, error) {
				if row.transport != nil {
					return nil, row.transport
				}
				return &http.Response{StatusCode: row.status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("payload"))}, nil
			})}
			var w io.Writer = io.Discard
			if row.writer {
				w = fullDisk{}
			}
			e := s.fetch(context.Background(), "https://ghcr.io/v2/image/blobs/sha256:abc?token=secret", 1024, w)
			if e == nil || !strings.Contains(e.Error(), row.want) || strings.Contains(e.Error(), "secret") {
				t.Fatalf("lost or unsafe failure: %v", e)
			}
			if row.writer && !errors.Is(e, syscall.ENOSPC) {
				t.Fatalf("lost disk cause: %v", e)
			}
		})
	}
}

type fullDisk struct{}

func (fullDisk) Write([]byte) (int, error) { return 0, syscall.ENOSPC }
