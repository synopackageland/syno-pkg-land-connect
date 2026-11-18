package apps

import (
	"context"
	"dsmapp/internal/cfaccess"
)

type AccessRequest struct {
	cfaccess.Request
	Confirm bool `json:"confirm"`
}
type AccessTokenRequest struct {
	Token string `json:"token"`
}
type AccessAPI interface {
	AccessGet(context.Context) (cfaccess.Status, error)
	AccessApply(context.Context, AccessRequest) (cfaccess.Status, error)
	AccessSync(context.Context) (cfaccess.Status, error)
	AccessToken(context.Context, AccessTokenRequest) (cfaccess.Status, error)
}

func (s *Service) AccessGet(ctx context.Context) (cfaccess.Status, error) {
	if p, ok := s.Provider.(AccessAPI); ok {
		return p.AccessGet(ctx)
	}
	return cfaccess.Status{}, ErrUnavailable
}
func (s *Service) AccessApply(ctx context.Context, in AccessRequest) (cfaccess.Status, error) {
	if !in.Confirm {
		return cfaccess.Status{}, ErrInvalid
	}
	if p, ok := s.Provider.(AccessAPI); ok {
		s.mu.Lock()
		defer s.mu.Unlock()
		return p.AccessApply(ctx, in)
	}
	return cfaccess.Status{}, ErrUnavailable
}
func (s *Service) AccessSync(ctx context.Context) (cfaccess.Status, error) {
	if p, ok := s.Provider.(AccessAPI); ok {
		s.mu.Lock()
		defer s.mu.Unlock()
		return p.AccessSync(ctx)
	}
	return cfaccess.Status{}, ErrUnavailable
}
func (s *Service) AccessToken(ctx context.Context, in AccessTokenRequest) (cfaccess.Status, error) {
	if p, ok := s.Provider.(AccessAPI); ok {
		s.mu.Lock()
		defer s.mu.Unlock()
		return p.AccessToken(ctx, in)
	}
	return cfaccess.Status{}, ErrUnavailable
}
func (c Client) AccessGet(ctx context.Context) (out cfaccess.Status, err error) {
	err = c.exchange(ctx, "accessget", struct{}{}, &out)
	return
}
func (c Client) AccessApply(ctx context.Context, in AccessRequest) (out cfaccess.Status, err error) {
	err = c.exchange(ctx, "accessapply", in, &out)
	return
}
func (c Client) AccessSync(ctx context.Context) (out cfaccess.Status, err error) {
	err = c.exchange(ctx, "accesssync", struct{}{}, &out)
	return
}
func (c Client) AccessToken(ctx context.Context, in AccessTokenRequest) (out cfaccess.Status, err error) {
	err = c.exchange(ctx, "accesstoken", in, &out)
	return
}
