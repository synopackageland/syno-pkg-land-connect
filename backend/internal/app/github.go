package app

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"dsmapp/internal/githubtoken"
	"github.com/synopackageland/syno-pkg-land-sdk/api"
)

// githubTokenView is all the page ever sees of the token: whether one is
// saved, its last four characters, and the limit GitHub reported on saving.
type githubTokenView struct {
	githubtoken.Status
	Limit     int    `json:"limit,omitempty"`
	Remaining int    `json:"remaining,omitempty"`
	Reset     string `json:"reset,omitempty"`
}

func (cfg Config) githubStore() githubtoken.Store { return githubtoken.Store{Dir: cfg.DataDir} }

func (cfg Config) githubTokenStatus(_ context.Context, _ api.Caller, params json.RawMessage) (any, error) {
	if e := decodeParams(params, &struct{}{}); e != nil {
		return nil, e
	}
	return githubTokenView{Status: cfg.githubStore().Status()}, nil
}

// githubTokenSet checks the token with GitHub once before saving it, so a
// mistyped or expired token is refused here and not on the next update check.
func (cfg Config) githubTokenSet(ctx context.Context, _ api.Caller, params json.RawMessage) (any, error) {
	var in struct {
		Token string `json:"token"`
	}
	if e := decodeParams(params, &in); e != nil {
		return nil, e
	}
	client := cfg.GitHubHTTP
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	limit, e := githubtoken.Validate(ctx, client, in.Token)
	switch {
	case errors.Is(e, githubtoken.ErrMalformed):
		return nil, &api.Error{Code: "github_token_malformed", Message: "the GitHub token is not in a recognized format"}
	case errors.Is(e, githubtoken.ErrInvalid):
		return nil, &api.Error{Code: "github_token_invalid", Message: "GitHub rejected the token; it is invalid or expired"}
	case e != nil:
		return nil, &api.Error{Code: "github_unavailable", Message: "GitHub could not be reached to check the token"}
	}
	if e = cfg.githubStore().Save(in.Token); e != nil {
		return nil, &api.Error{Code: "github_token_unsaved", Message: "the GitHub token could not be saved"}
	}
	return githubTokenView{Status: cfg.githubStore().Status(), Limit: limit.Limit, Remaining: limit.Remaining, Reset: limit.Reset.UTC().Format(time.RFC3339)}, nil
}

func (cfg Config) githubTokenRemove(_ context.Context, _ api.Caller, params json.RawMessage) (any, error) {
	if e := decodeParams(params, &struct{}{}); e != nil {
		return nil, e
	}
	if e := cfg.githubStore().Remove(); e != nil {
		return nil, &api.Error{Code: "github_token_unsaved", Message: "the GitHub token could not be removed"}
	}
	return githubTokenView{Status: cfg.githubStore().Status()}, nil
}
