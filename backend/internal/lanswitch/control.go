package lanswitch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/synopackageland/syno-pkg-land-sdk/ipc"
)

// Request is the body of a control call; only set reads Enabled.
type Request struct {
	Enabled bool `json:"enabled"`
}

type reply struct {
	Status *Status `json:"status,omitempty"`
	Probe  *Probe  `json:"probe,omitempty"`
	Error  string  `json:"error,omitempty"`
}

// Handler serves /lanswitch/status, /lanswitch/set and /lanswitch/probe on
// the daemon's private control socket.
func (s *Service) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in Request
		var out reply
		dec := json.NewDecoder(io.LimitReader(r.Body, 257))
		dec.DisallowUnknownFields()
		var extra any
		action, _ := strings.CutPrefix(r.URL.Path, "/lanswitch/")
		if r.Method != http.MethodPost || dec.Decode(&in) != nil || dec.Decode(&extra) != io.EOF {
			out.Error = "invalid LAN switch request"
		} else {
			switch action {
			case "status":
				st := s.Status()
				out.Status = &st
			case "set":
				st, err := s.Set(in.Enabled)
				if err != nil {
					out.Error = "the setting could not be saved"
				} else {
					out.Status = &st
				}
			case "probe":
				ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
				p, err := s.Probe(ctx)
				cancel()
				if err != nil {
					out.Error = err.Error()
				} else {
					out.Probe = &p
				}
			default:
				out.Error = "unknown LAN switch action"
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
}

// Client reaches the Service in the daemon.
type Client struct{ Socket string }

func (c Client) call(ctx context.Context, action string, in Request) (reply, error) {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return ipc.DialUnix(ctx, c.Socket)
	}}
	defer transport.CloseIdleConnections()
	raw, _ := json.Marshal(in)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://connector/lanswitch/"+action, bytes.NewReader(raw))
	if err != nil {
		return reply{}, err
	}
	res, err := (&http.Client{Transport: transport, Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return reply{}, errors.New("the Syno Connect Center service is not running")
	}
	defer res.Body.Close()
	var out reply
	if res.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&out) != nil {
		return reply{}, errors.New("invalid LAN switch response")
	}
	if out.Error != "" {
		return reply{}, errors.New(out.Error)
	}
	return out, nil
}

// Status returns the option and the responder's addresses.
func (c Client) Status(ctx context.Context) (Status, error) {
	out, err := c.call(ctx, "status", Request{})
	if err != nil || out.Status == nil {
		return Status{}, errors.Join(err, errors.New("no status"))
	}
	return *out.Status, nil
}

// Set saves the option.
func (c Client) Set(ctx context.Context, enabled bool) (Status, error) {
	out, err := c.call(ctx, "set", Request{Enabled: enabled})
	if err != nil || out.Status == nil {
		return Status{}, errors.Join(err, errors.New("no status"))
	}
	return *out.Status, nil
}

// Probe asks for credentials and candidates for one page.
func (c Client) Probe(ctx context.Context) (Probe, error) {
	out, err := c.call(ctx, "probe", Request{})
	if err != nil || out.Probe == nil {
		return Probe{}, errors.Join(err, errors.New("no probe"))
	}
	return *out.Probe, nil
}
