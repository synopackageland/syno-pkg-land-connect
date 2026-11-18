package app

import (
	"context"
	"encoding/json"
	"net/http"

	"dsmapp/internal/advisor"
	"dsmapp/internal/dsmsecurity"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

// readSecurity, like readExternal, acts only with the request's own session;
// DSM decides what that user may read.
func (cfg Config) readSecurity(ctx context.Context, request *http.Request) dsmsecurity.Status {
	cookie, token := request.Header.Get("Cookie"), request.Header.Get("X-SYNO-TOKEN")
	if cfg.SecurityGet != nil {
		return cfg.SecurityGet(ctx, cookie, token)
	}
	return dsmsecurity.Read(ctx, dsmOrigin, cookie, token)
}

func (cfg Config) securityStatus(ctx context.Context, _ api.Caller, request *http.Request, params json.RawMessage) (any, error) {
	if err := decodeParams(params, &struct{}{}); err != nil {
		return nil, err
	}
	return cfg.readSecurity(ctx, request), nil
}

type securityDiagnosis struct {
	dsmsecurity.Status
	Next []advisor.Step `json:"next"`
}

// securityAdvice links each protection that is off to DSM's own help; the
// user turns it on in DSM. Unknown is not a problem to report.
func (cfg Config) securityAdvice(ctx context.Context, request *http.Request) securityDiagnosis {
	s := cfg.readSecurity(ctx, request)
	links := advisor.Default().Protections
	out := securityDiagnosis{Status: s, Next: []advisor.Step{}}
	for _, p := range []struct{ id, state string }{{"auto-block", s.AutoBlock}, {"dsm-2fa", s.TwoFactor}} {
		if p.state == dsmsecurity.Off {
			out.Next = append(out.Next, advisor.Step{Kind: "user", Action: "turn-on-" + p.id, URL: links[p.id]})
		}
	}
	return out
}
