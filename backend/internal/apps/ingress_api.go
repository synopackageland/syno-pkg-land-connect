package apps

import "context"

type IngressRequest struct {
	Socket          string   `json:"socket,omitempty"`
	AdditionalPaths []string `json:"additionalPaths,omitempty"`
	Key             string   `json:"key"`
	Port            int      `json:"port"`
	Path            string   `json:"path"`
	Hostname        string   `json:"hostname"`
}
type IngressRef struct {
	ID  string `json:"id,omitempty"`
	Key string `json:"key,omitempty"`
}
type IngressStatus struct {
	ID               string `json:"id"`
	State            string `json:"state"`
	Ready            bool   `json:"ready"`
	URL              string `json:"url,omitempty"`
	ListenerHealthy  bool   `json:"listenerHealthy"`
	ConnectorState   string `json:"connectorState"`
	ExternalVerified bool   `json:"externalVerified"`
}
type IngressRuntime interface {
	RequestIngress(context.Context, IngressSubject, IngressRequest) (IngressStatus, error)
	StatusIngress(context.Context, IngressSubject, IngressRef) (IngressStatus, error)
	RevokeIngress(context.Context, IngressSubject, IngressRef) (IngressStatus, error)
}

// IngressCall shares the ordinary App mutation lock, including admin approval
// and Access changes. A consumer cannot race a revoke against an activation.
func (s *Service) IngressCall(ctx context.Context, subject IngressSubject, method string, in IngressRequest, ref IngressRef) (IngressStatus, error) {
	r, ok := s.Lifecycle.Runtime.(IngressRuntime)
	if !ok {
		return IngressStatus{}, ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	switch method {
	case "request":
		return r.RequestIngress(ctx, subject, in)
	case "status":
		return r.StatusIngress(ctx, subject, ref)
	case "revoke":
		return r.RevokeIngress(ctx, subject, ref)
	default:
		return IngressStatus{}, ErrInvalid
	}
}
