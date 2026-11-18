package nbserver

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

	"dsmapp/internal/connector"
)

// Request is one call from the CGI to connectd.
type Request struct {
	UseAnotherName bool `json:"useAnotherName,omitempty"`
	RemoveData     bool `json:"removeData,omitempty"`
}

type reply struct {
	Status   *Status `json:"status,omitempty"`
	Email    string  `json:"email,omitempty"`
	Password string  `json:"password,omitempty"`
	Error    string  `json:"error,omitempty"`
	Code     string  `json:"code,omitempty"`
}

// Handler serves /nbserver/<action> on connectd's private control socket.
func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in Request
		dec := json.NewDecoder(io.LimitReader(r.Body, 1025))
		dec.DisallowUnknownFields()
		var extra any
		var out reply
		action, _ := strings.CutPrefix(r.URL.Path, "/nbserver/")
		if r.Method != "POST" || dec.Decode(&in) != nil || dec.Decode(&extra) != io.EOF {
			out.Error = "invalid NetBird server request"
		} else {
			var e error
			switch action {
			case "status":
			case "setup":
				e = s.SetupWithOptions(in.UseAnotherName)
			case "disable":
				// Removing a name waits on Cloudflare; the page shows the outcome.
				ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 2*time.Minute)
				e = s.Disable(ctx, in.RemoveData)
				cancel()
			case "credentials":
				out.Email, out.Password, e = s.Credentials()
			default:
				e = errors.New("unknown NetBird server action")
			}
			if e != nil {
				out.Error, out.Code = e.Error(), connector.ErrorCode(e)
			}
			if action != "credentials" {
				st := s.Status(r.Context())
				out.Status = &st
			}
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(out)
	})
}

// Client is the CGI side of the control socket.
type Client struct{ Socket string }

// Result is one answer: the status, or the owner's sign-in.
type Result struct {
	Status   Status
	Email    string
	Password string
}

func (c Client) Call(ctx context.Context, action string, in Request) (Result, error) {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return ipc.DialUnix(ctx, c.Socket)
	}}
	defer transport.CloseIdleConnections()
	raw, _ := json.Marshal(in)
	req, e := http.NewRequestWithContext(ctx, "POST", "http://connector/nbserver/"+action, bytes.NewReader(raw))
	if e != nil {
		return Result{}, e
	}
	timeout := 10 * time.Second
	if action == "disable" {
		timeout = 150 * time.Second
	}
	res, e := (&http.Client{Transport: transport, Timeout: timeout}).Do(req)
	if e != nil {
		return Result{}, connector.Coded("netbird_server_unavailable", "the Syno Connect Center service is not running")
	}
	defer res.Body.Close()
	var out reply
	if res.StatusCode != 200 || json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&out) != nil {
		return Result{}, errors.New("invalid NetBird server response")
	}
	result := Result{Email: out.Email, Password: out.Password}
	if out.Status != nil {
		result.Status = *out.Status
	}
	if out.Error != "" {
		if out.Code != "" {
			return result, connector.Coded(out.Code, out.Error)
		}
		return result, errors.New(out.Error)
	}
	return result, nil
}
