package workers

import (
	"context"
	"dsmapp/internal/accounttoken"
)

// AccountCredential migrates an old Workers secret into the single shared
// store without starting a relay, making a Worker or choosing workers.dev.
func (m *Manager) AccountCredential() (accounttoken.Credential, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, e := m.Store.load()
	if e != nil {
		return accounttoken.Credential{}, e
	}
	return accounttoken.Credential{Account: v.AccountID, Token: v.Token}, nil
}

// SaveAccountToken is the common token entry point for an Access-only setup.
// Existing Workers capabilities are checked before replacing the shared value.
func (m *Manager) SaveAccountToken(ctx context.Context, token string, validate func(context.Context, accounttoken.Credential) error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Store.Credentials == nil || !validToken(token) {
		return failure("workers_token_invalid")
	}
	old, e := m.Store.load()
	if e != nil {
		return failure("workers_state_corrupt")
	}
	account := old.AccountID
	if account == "" {
		account, e = m.API.account(ctx, token)
		if e != nil {
			return e
		}
	}
	// verify + read-only account discovery do not activate Workers
	var verified struct {
		Status string `json:"status"`
	}
	if e = m.API.call(ctx, token, "GET", "/user/tokens/verify", "", nil, &verified); e != nil {
		return sanitize(e)
	}
	if verified.Status != "active" {
		return failure("workers_token_invalid")
	}
	if len(old.Records) > 0 || old.SignalBridge != nil || old.Subdomain != "" {
		if _, e = m.API.validate(ctx, token, account); e != nil {
			return e
		}
	}
	next := accounttoken.Credential{Account: account, Token: token}
	if validate != nil {
		if e = validate(ctx, next); e != nil {
			return e
		}
	}
	if m.ValidateAccountToken != nil {
		if e = m.ValidateAccountToken(ctx, next); e != nil {
			return e
		}
	}
	return m.Store.Credentials.Save(next)
}
