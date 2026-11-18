package connector

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/synopackageland/syno-pkg-land-sdk/ipc"
	"io"
	"net"
	"net/http"
	"reflect"
	"time"
)

var ErrBinaryRequired = Coded("binary_required", "download verified connector binaries before connecting")

// CodedError is a refusal whose code is safe to show; it crosses the control
// socket and the CGI by code, so errors.Is matches equal codes.
type CodedError struct{ Code, Message string }

func Coded(code, message string) error { return &CodedError{code, message} }
func (e *CodedError) Error() string    { return e.Message }
func (e *CodedError) Is(target error) bool {
	t, ok := target.(*CodedError)
	return ok && t.Code == e.Code
}

type Control interface {
	Call(context.Context, string, any) (Status, error)
	Check(context.Context, string) (CheckResult, error)
}

type Client struct{ Socket string }

func (c Client) Call(ctx context.Context, method string, input any) (Status, error) {
	var reply struct {
		Status Status `json:"status"`
	}
	if e := c.exchange(ctx, method, input, &reply); e != nil {
		return Status{}, e
	}
	return reply.Status, nil
}

// Check asks connectd for one provider's connection check.
func (c Client) Check(ctx context.Context, provider string) (CheckResult, error) {
	var reply struct {
		Check CheckResult `json:"check"`
	}
	if e := c.exchange(ctx, "check", map[string]string{"provider": provider}, &reply); e != nil {
		return CheckResult{}, e
	}
	return reply.Check, nil
}

// exchange posts input to one control action and decodes the reply body into
// out; a coded or plain refusal in the reply becomes the returned error.
func (c Client) exchange(ctx context.Context, method string, input, out any) error {
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return ipc.DialUnix(ctx, c.Socket)
	}}
	defer transport.CloseIdleConnections()
	// connectd answers a slow connect or disconnect within Async.Accept, so
	// every call stays well inside the CGI deadline. Only a shutdown waits for
	// the stop itself and gets longer. Caller cancellation still wins.
	timeout := 8 * time.Second
	if method == "shutdown" {
		timeout = 15 * time.Second
	}
	client := &http.Client{Transport: transport, Timeout: timeout}
	pr, pw := io.Pipe()
	go func() { e := json.NewEncoder(pw).Encode(input); pw.CloseWithError(e) }()
	req, e := http.NewRequestWithContext(ctx, "POST", "http://connector/"+method, pr)
	if e != nil {
		return e
	}
	defer pr.Close()
	res, e := client.Do(req)
	if e != nil {
		return errors.New("connector service unavailable")
	}
	defer res.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(res.Body, 65537))
	if e != nil {
		return errors.New("invalid connector response")
	}
	var refusal struct {
		Error    string        `json:"error"`
		Code     string        `json:"code"`
		Conflict *NameConflict `json:"conflict,omitempty"`
	}
	if json.Unmarshal(raw, &refusal) != nil || json.Unmarshal(raw, out) != nil {
		return errors.New("invalid connector response")
	}
	if refusal.Code != "" {
		err := Coded(refusal.Code, refusal.Error)
		if refusal.Conflict != nil {
			return WithNameConflict(err, *refusal.Conflict)
		}
		return err
	}
	if refusal.Error != "" {
		return errors.New(refusal.Error)
	}
	return nil
}

type Controller interface {
	Status() Status
	Connect(context.Context, Request) (Status, error)
	Disconnect() (Status, error)
	Stop() error
}

func Handler(m Controller, shutdown func()) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var s Status
		var e error
		reply := map[string]any{}
		if r.Method != "POST" {
			http.Error(w, "POST required", 405)
			return
		}
		switch r.URL.Path {
		case "/status":
			s = m.Status()
		case "/ping":
			// Reachability only: never waits for a provider status read.
		case "/connect":
			var input Request
			d := json.NewDecoder(io.LimitReader(r.Body, 65537))
			d.DisallowUnknownFields()
			var extra any
			if d.Decode(&input) != nil || d.Decode(&extra) != io.EOF {
				e = errors.New("invalid request")
			} else {
				s, e = m.Connect(r.Context(), input)
			}
		case "/disconnect":
			var input struct {
				Provider string `json:"provider"`
			}
			d := json.NewDecoder(io.LimitReader(r.Body, 4097))
			d.DisallowUnknownFields()
			var extra any
			if d.Decode(&input) != nil || d.Decode(&extra) != io.EOF {
				e = errors.New("invalid request")
			} else if input.Provider == "" {
				s, e = m.Disconnect()
			} else if scoped, ok := m.(interface{ DisconnectProvider(string) (Status, error) }); ok {
				s, e = scoped.DisconnectProvider(input.Provider)
			} else {
				e = errors.New("scoped disconnect unavailable")
			}
		case "/forget":
			var input struct {
				Provider string `json:"provider"`
			}
			d := json.NewDecoder(io.LimitReader(r.Body, 4097))
			d.DisallowUnknownFields()
			var extra any
			forgetter, ok := m.(interface{ Forget(string) (Status, error) })
			if d.Decode(&input) != nil || d.Decode(&extra) != io.EOF || input.Provider == "" {
				e = errors.New("invalid request")
			} else if !ok {
				e = errors.New("forgetting services unavailable")
			} else {
				s, e = forgetter.Forget(input.Provider)
			}
		case "/check":
			var input struct {
				Provider string `json:"provider"`
			}
			d := json.NewDecoder(io.LimitReader(r.Body, 4097))
			d.DisallowUnknownFields()
			var extra any
			checker, ok := m.(interface {
				Check(context.Context, string) (CheckResult, error)
			})
			if d.Decode(&input) != nil || d.Decode(&extra) != io.EOF || !Checkable(input.Provider) {
				e = errors.New("invalid request")
			} else if !ok {
				e = errors.New("connection checks unavailable")
			} else {
				reply["check"], e = checker.Check(r.Context(), input.Provider)
			}
		case "/shutdown":
			e = m.Stop()
			s = m.Status()
			if e == nil {
				defer shutdown()
			}
		default:
			e = errors.New("unknown control action")
		}
		out := reply
		out["status"] = s
		if e != nil {
			out["error"] = e.Error()
			out["conflict"] = Conflict(e)
			if code := ErrorCode(e); code != "" {
				out["code"] = code
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(out)
	})
}

// ErrorCode names the refusals that are safe to show; others stay generic.
func ErrorCode(e error) string {
	var coded *CodedError
	if errors.As(e, &coded) {
		return coded.Code
	}
	return ""
}

// RetryOnly refuses configuration mixed with a retry of saved intent.
func RetryOnly(r Request) bool {
	return r.Retry && reflect.DeepEqual(r, Request{Provider: r.Provider, Retry: true}) && (r.Provider == "cloudflare" || r.Provider == "tailscale" || r.Provider == "netbird")
}
