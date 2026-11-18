package apps

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/synopackageland/syno-pkg-land-sdk/ipc"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"dsmapp/internal/connector"
)

type API interface {
	List(context.Context) (List, error)
	Inspect(context.Context, InspectRequest) (Inspection, error)
	CloudflareConnect(context.Context) (LoginResult, error)
	CloudflareDisconnect(context.Context) error
	SetPrivate(context.Context, PrivateRequest) (Mutation, error)
	SetPublic(context.Context, PublicRequest) (Mutation, error)
	Reenable(context.Context, ReenableRequest) (Mutation, error)
	Clear(context.Context, ScopedChange) error
	InspectDSM(context.Context, DSMInspectRequest) (DSMInspection, error)
	SetDSM(context.Context, DSMRequest) (DSMResult, error)
}

func ErrorCode(err error) string {
	switch {
	case errors.Is(err, ErrCorrupt):
		return "apps_state_corrupt"
	case errors.Is(err, ErrNotReady):
		return "apps_waiting"
	case errors.Is(err, ErrNeedsUpdate):
		return "apps_update_required"
	case errors.Is(err, ErrConfirmationChanged):
		return "apps_confirmation_changed"
	case errors.Is(err, ErrInvalid):
		return "bad_request"
	case errors.Is(err, ErrCleanup):
		return "apps_cleanup_required"
	}
	// A provider's own refusal, such as a public name already in DNS, keeps
	// its code so the page can say what to change.
	if code := connector.ErrorCode(err); providerCode.MatchString(code) {
		return code
	}
	return "apps_unavailable"
}

var providerCode = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func codeError(code string) error {
	for _, err := range []error{ErrCorrupt, ErrNotReady, ErrNeedsUpdate, ErrConfirmationChanged, ErrInvalid, ErrCleanup} {
		if ErrorCode(err) == code {
			return err
		}
	}
	if code != "apps_unavailable" && providerCode.MatchString(code) {
		return connector.Coded(code, ErrUnavailable.Error())
	}
	return ErrUnavailable
}

// Handler is mounted only on connectd's private, package-account control
// socket. The public CGI performs DSM administrator authorization first.
func Handler(service API) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, 65537))
		if err != nil || len(raw) > 65536 {
			_ = json.NewEncoder(w).Encode(map[string]string{"code": "bad_request"})
			return
		}
		var result any
		switch strings.TrimPrefix(r.URL.Path, "/apps/") {
		case "accessget", "accessapply", "accesssync", "accesstoken":
			p, ok := service.(AccessAPI)
			if !ok {
				err = ErrUnavailable
				break
			}
			switch strings.TrimPrefix(r.URL.Path, "/apps/") {
			case "accessget":
				if err = controlDecode(raw, &struct{}{}); err == nil {
					result, err = p.AccessGet(r.Context())
				}
			case "accesssync":
				if err = controlDecode(raw, &struct{}{}); err == nil {
					result, err = p.AccessSync(r.Context())
				}
			case "accessapply":
				var in AccessRequest
				if err = controlDecode(raw, &in); err == nil {
					result, err = p.AccessApply(r.Context(), in)
				}
			case "accesstoken":
				var in AccessTokenRequest
				if err = controlDecode(raw, &in); err == nil {
					result, err = p.AccessToken(r.Context(), in)
				}
			}
		case "resetstate":
			p, ok := service.(StateResetAPI)
			if !ok {
				err = ErrUnavailable
				break
			}
			if err = controlDecode(raw, &struct{}{}); err == nil {
				result, err = p.ResetState(r.Context())
			}
		case "cleardsm":
			var in DSMInspectRequest
			p, ok := service.(DSMClearAPI)
			if !ok {
				err = ErrUnavailable
				break
			}
			if err = controlDecode(raw, &in); err == nil {
				err = p.ClearDSM(r.Context(), in)
				result = map[string]bool{"cleared": err == nil}
			}
		case "workersstatus", "workersconnect", "workersdisconnect":
			p, ok := service.(WorkersAPI)
			if !ok {
				err = ErrUnavailable
				break
			}
			switch strings.TrimPrefix(r.URL.Path, "/apps/") {
			case "workersstatus":
				if err = controlDecode(raw, &struct{}{}); err == nil {
					result, err = p.WorkersStatus(r.Context())
				}
			case "workersconnect":
				var in WorkersConnectRequest
				if err = controlDecode(raw, &in); err == nil {
					result, err = p.WorkersConnect(r.Context(), in)
				}
			case "workersdisconnect":
				if err = controlDecode(raw, &struct{}{}); err == nil {
					err = p.WorkersDisconnect(r.Context())
					result = map[string]bool{"disconnected": err == nil}
				}
			}
		case "workerspublishdsm":
			p, ok := service.(WorkersDSMAPI)
			if !ok {
				err = ErrUnavailable
				break
			}
			if err = controlDecode(raw, &struct{}{}); err == nil {
				result, err = p.WorkersPublishDSM(r.Context())
			}
		case "customadd", "customedit", "customremove", "custompause", "customcandidates":
			p, ok := service.(CustomAPI)
			if !ok {
				err = ErrUnavailable
				break
			}
			switch strings.TrimPrefix(r.URL.Path, "/apps/") {
			case "customadd", "customedit":
				var in CustomRequest
				if err = controlDecode(raw, &in); err == nil {
					if strings.HasSuffix(r.URL.Path, "add") {
						result, err = p.CustomAdd(r.Context(), in)
					} else {
						result, err = p.CustomEdit(r.Context(), in)
					}
				}
			case "customremove", "custompause":
				var in CustomID
				if err = controlDecode(raw, &in); err == nil {
					if strings.HasSuffix(r.URL.Path, "remove") {
						err = p.CustomRemove(r.Context(), in)
					} else {
						err = p.CustomPause(r.Context(), in)
					}
					result = map[string]bool{"done": err == nil}
				}
			case "customcandidates":
				if err = controlDecode(raw, &struct{}{}); err == nil {
					result, err = p.CustomCandidates(r.Context())
				}
			}
		case "list":
			if err = controlDecode(raw, &struct{}{}); err == nil {
				result, err = service.List(r.Context())
			}
		case "setprivate":
			var in PrivateRequest
			if err = controlDecode(raw, &in); err == nil {
				result, err = service.SetPrivate(r.Context(), in)
			}
		case "setpublic":
			var in PublicRequest
			if err = controlDecode(raw, &in); err == nil {
				result, err = service.SetPublic(r.Context(), in)
			}
		case "reenable":
			var in ReenableRequest
			if err = controlDecode(raw, &in); err == nil {
				result, err = service.Reenable(r.Context(), in)
			}
		case "inspect":
			var in InspectRequest
			if err = controlDecode(raw, &in); err == nil {
				result, err = service.Inspect(r.Context(), in)
			}
		case "inspectdsm":
			var in DSMInspectRequest
			if err = controlDecode(raw, &in); err == nil {
				result, err = service.InspectDSM(r.Context(), in)
			}
		case "setdsm":
			var in DSMRequest
			if err = controlDecode(raw, &in); err == nil {
				result, err = service.SetDSM(r.Context(), in)
			}
		case "cloudflareconnect":
			if err = controlDecode(raw, &struct{}{}); err == nil {
				result, err = service.CloudflareConnect(r.Context())
			}
		case "cloudflaredisconnect":
			if err = controlDecode(raw, &struct{}{}); err == nil {
				err = service.CloudflareDisconnect(r.Context())
				result = map[string]bool{"disconnected": err == nil}
			}
		case "cleanuporphans":
			p, ok := service.(OrphanCleanupAPI)
			if !ok {
				err = ErrUnavailable
				break
			}
			if err = controlDecode(raw, &struct{}{}); err == nil {
				result, err = p.CleanupPublicOrphans(r.Context())
			}
		case "clear":
			var in ScopedChange
			if err = controlDecode(raw, &in); err == nil {
				err = service.Clear(r.Context(), in)
				result = map[string]bool{"cleared": err == nil}
			}
		default:
			err = ErrInvalid
		}
		if err != nil {
			_ = json.NewEncoder(w).Encode(map[string]any{"code": ErrorCode(err), "result": result, "conflict": connector.Conflict(err)})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"result": result})
	})
}

type Client struct{ Socket string }

func (c Client) exchange(ctx context.Context, method string, in, out any) error {
	raw, err := json.Marshal(in)
	if err != nil {
		return ErrInvalid
	}
	transport := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		dialCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		return ipc.DialUnix(dialCtx, c.Socket)
	}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Minute}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://connect/apps/"+method, bytes.NewReader(raw))
	if err != nil {
		return ErrUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := client.Do(req)
	if err != nil {
		return ErrUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return ErrUnavailable
	}
	raw, err = io.ReadAll(io.LimitReader(res.Body, stateLimit+1))
	if err != nil || len(raw) > stateLimit {
		return ErrUnavailable
	}
	var envelope struct {
		Conflict *connector.NameConflict `json:"conflict,omitempty"`
		Code     string                  `json:"code"`
		Result   json.RawMessage         `json:"result"`
	}
	if controlDecode(raw, &envelope) != nil {
		return ErrUnavailable
	}
	if envelope.Code != "" {
		err := codeError(envelope.Code)
		if envelope.Conflict != nil {
			return connector.WithNameConflict(err, *envelope.Conflict)
		}
		return err
	}
	if json.Unmarshal(envelope.Result, out) != nil {
		return ErrUnavailable
	}
	return nil
}
func (c Client) List(ctx context.Context) (out List, err error) {
	err = c.exchange(ctx, "list", struct{}{}, &out)
	return
}
func (c Client) SetPrivate(ctx context.Context, in PrivateRequest) (out Mutation, err error) {
	err = c.exchange(ctx, "setprivate", in, &out)
	return
}
func (c Client) SetPublic(ctx context.Context, in PublicRequest) (out Mutation, err error) {
	err = c.exchange(ctx, "setpublic", in, &out)
	return
}
func (c Client) Reenable(ctx context.Context, in ReenableRequest) (out Mutation, err error) {
	err = c.exchange(ctx, "reenable", in, &out)
	return
}
func (c Client) Clear(ctx context.Context, in ScopedChange) error {
	var out map[string]bool
	return c.exchange(ctx, "clear", in, &out)
}

func controlDecode(raw []byte, out any) error {
	if decode(raw, out) != nil {
		return ErrInvalid
	}
	return nil
}

func (c Client) Inspect(ctx context.Context, in InspectRequest) (out Inspection, err error) {
	err = c.exchange(ctx, "inspect", in, &out)
	return
}
func (c Client) InspectDSM(ctx context.Context, in DSMInspectRequest) (out DSMInspection, err error) {
	err = c.exchange(ctx, "inspectdsm", in, &out)
	return
}
func (c Client) SetDSM(ctx context.Context, in DSMRequest) (out DSMResult, err error) {
	err = c.exchange(ctx, "setdsm", in, &out)
	return
}
func (c Client) CloudflareConnect(ctx context.Context) (out LoginResult, err error) {
	err = c.exchange(ctx, "cloudflareconnect", struct{}{}, &out)
	return
}
func (c Client) CloudflareDisconnect(ctx context.Context) error {
	var out map[string]bool
	return c.exchange(ctx, "cloudflaredisconnect", struct{}{}, &out)
}
func (c Client) CleanupPublicOrphans(ctx context.Context) (out OrphanCleanup, err error) {
	err = c.exchange(ctx, "cleanuporphans", struct{}{}, &out)
	return
}
