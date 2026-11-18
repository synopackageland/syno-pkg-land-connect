package apps

import "context"

type publicProviderKey struct{}

func WithPublicProvider(ctx context.Context, provider string) context.Context {
	return context.WithValue(ctx, publicProviderKey{}, provider)
}
func PublicProvider(ctx context.Context) string {
	v, _ := ctx.Value(publicProviderKey{}).(string)
	return v
}

// WorkersStatus deliberately contains no token, HMAC secret or upstream API body.
type WorkersStatus struct {
	Usage     *PublicUsage `json:"usage,omitempty"`
	Invalid   bool         `json:"invalid,omitempty"`
	Connected bool         `json:"connected"`
	// Token is set while a usable token is saved, with or without the
	// Free link's workers.dev name.
	Token      bool   `json:"token,omitempty"`
	RelayState string `json:"relayState"`
	AccountID  string `json:"accountId,omitempty"`
	Subdomain  string `json:"subdomain,omitempty"`
	Code       string `json:"code,omitempty"`
	// DSM is where public DSM opens: this Free link or the own domain.
	DSM *WorkersDSM `json:"dsm,omitempty"`
	// DSMCode says why turning the Free link on left DSM unpublished.
	DSMCode string `json:"dsmCode,omitempty"`
}

type PublicUsage struct {
	Public bool `json:"public"`
	Relay  bool `json:"relay"`
}

// WorkersDSM names the one provider serving public DSM and its address.
type WorkersDSM struct {
	Provider string `json:"provider"`
	URL      string `json:"url"`
}
type WorkersConnectRequest struct {
	Token     string `json:"token"`
	AccountID string `json:"accountId"`
	Subdomain string `json:"subdomain,omitempty"`
	// NoDSM saves and checks the token for the NetBird server's Signal
	// bridge only: DSM stays private and no workers.dev name is needed.
	// Turning the Free link on later reuses it; Add DSM stays separate.
	NoDSM bool `json:"noDsm,omitempty"`
}
type WorkersAPI interface {
	WorkersStatus(context.Context) (WorkersStatus, error)
	WorkersConnect(context.Context, WorkersConnectRequest) (WorkersStatus, error)
	WorkersDisconnect(context.Context) error
}

// WorkersDSMAPI publishes DSM on the Free link in one click, for a Free link
// turned on before it did so by itself or after DSM was cleared.
type WorkersDSMAPI interface {
	WorkersPublishDSM(context.Context) (WorkersStatus, error)
}

func (s *Service) WorkersPublishDSM(ctx context.Context) (WorkersStatus, error) {
	if p, ok := s.Provider.(WorkersDSMAPI); ok {
		s.mu.Lock()
		defer s.mu.Unlock()
		return p.WorkersPublishDSM(ctx)
	}
	return WorkersStatus{}, ErrUnavailable
}
func (c Client) WorkersPublishDSM(ctx context.Context) (out WorkersStatus, err error) {
	err = c.exchange(ctx, "workerspublishdsm", struct{}{}, &out)
	return
}

func (s *Service) WorkersStatus(ctx context.Context) (WorkersStatus, error) {
	if p, ok := s.Provider.(WorkersAPI); ok {
		return p.WorkersStatus(ctx)
	}
	return WorkersStatus{Code: "workers_unavailable"}, nil
}
func (s *Service) WorkersConnect(ctx context.Context, in WorkersConnectRequest) (WorkersStatus, error) {
	if p, ok := s.Provider.(WorkersAPI); ok {
		s.mu.Lock()
		defer s.mu.Unlock()
		return p.WorkersConnect(ctx, in)
	}
	return WorkersStatus{}, ErrUnavailable
}
func (s *Service) WorkersDisconnect(ctx context.Context) error {
	if p, ok := s.Provider.(WorkersAPI); ok {
		s.mu.Lock()
		defer s.mu.Unlock()
		return p.WorkersDisconnect(ctx)
	}
	return ErrUnavailable
}
func (c Client) WorkersStatus(ctx context.Context) (out WorkersStatus, err error) {
	err = c.exchange(ctx, "workersstatus", struct{}{}, &out)
	return
}
func (c Client) WorkersConnect(ctx context.Context, in WorkersConnectRequest) (out WorkersStatus, err error) {
	err = c.exchange(ctx, "workersconnect", in, &out)
	return
}
func (c Client) WorkersDisconnect(ctx context.Context) error {
	var out map[string]bool
	return c.exchange(ctx, "workersdisconnect", struct{}{}, &out)
}

// DSMClearAPI withdraws a public DSM approval after its App scopes were cleared.
type DSMClearAPI interface {
	ClearDSM(context.Context, DSMInspectRequest) error
}

func (s *Service) ClearDSM(ctx context.Context, in DSMInspectRequest) error {
	if in.Scope != Public || !ValidProvider(in.Provider) {
		return ErrInvalid
	}
	p, ok := s.Lifecycle.Runtime.(DSMClearAPI)
	if !ok {
		return ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return p.ClearDSM(ctx, in)
}
func (c Client) ClearDSM(ctx context.Context, in DSMInspectRequest) error {
	var out map[string]bool
	return c.exchange(ctx, "cleardsm", in, &out)
}
