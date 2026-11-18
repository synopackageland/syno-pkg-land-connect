package binaries

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/synopackageland/syno-pkg-land-sdk/ipc"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
)

// Control is the same short-lived submission/status interface for CGI and tests.
type Control interface {
	Call(context.Context, string, string) (Status, error)
}
type Client struct{ Socket string }
type reply struct {
	Status Status `json:"status"`
	Error  string `json:"error,omitempty"`
	Code   string `json:"code,omitempty"`
}

func (c Client) Call(ctx context.Context, action, product string) (Status, error) {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return ipc.DialUnix(ctx, c.Socket)
	}}
	defer transport.CloseIdleConnections()
	raw, _ := json.Marshal(map[string]string{"product": product})
	req, e := http.NewRequestWithContext(ctx, "POST", "http://connector/binaries/"+action, bytes.NewReader(raw))
	if e != nil {
		return Status{}, e
	}
	res, e := (&http.Client{Transport: transport, Timeout: 8 * time.Second}).Do(req)
	if e != nil {
		return Status{}, errors.New("binary service unavailable")
	}
	defer res.Body.Close()
	var out reply
	dec := json.NewDecoder(io.LimitReader(res.Body, 16385))
	var extra any
	if res.StatusCode != 200 || dec.Decode(&out) != nil || dec.Decode(&extra) != io.EOF {
		return Status{}, errors.New("invalid binary service response")
	}
	if out.Code == "busy" {
		return Status{}, ErrBusy
	}
	if out.Code == "no_previous" {
		return Status{}, ErrNoPrevious
	}
	if out.Error != "" {
		return Status{}, errors.New(out.Error)
	}
	return out.Status, nil
}
func (s *Service) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "POST required", http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		Product string `json:"product"`
	}
	dec := json.NewDecoder(io.LimitReader(r.Body, 1025))
	dec.DisallowUnknownFields()
	var extra any
	var out reply
	if !strings.HasPrefix(r.URL.Path, "/binaries/") || dec.Decode(&in) != nil || dec.Decode(&extra) != io.EOF {
		out.Error = "invalid binary request"
	} else {
		v, e := s.Call(r.Context(), strings.TrimPrefix(r.URL.Path, "/binaries/"), in.Product)
		out.Status = v
		if e != nil {
			out.Error = e.Error()
			if errors.Is(e, ErrBusy) {
				out.Code = "busy"
			}
			if errors.Is(e, ErrNoPrevious) {
				out.Code = "no_previous"
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}
