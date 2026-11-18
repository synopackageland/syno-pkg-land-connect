package apps

import (
	"context"
	"dsmapp/internal/connector"
	"reflect"
)

// DSM uses the App name/approval path, but never App access or removal rules.
type DSMNameRuntime interface {
	InspectDSMName(context.Context) (Approval, error)
	OpenDSMName(context.Context, Approval) error
}

func (s *Service) inspectDSMApp(ctx context.Context, kind string) (Inspection, error) {
	if kind != Public {
		return Inspection{}, ErrInvalid
	}
	runtime, ok := s.Lifecycle.Runtime.(DSMNameRuntime)
	if !ok {
		return Inspection{}, ErrUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.Lifecycle.Store.Load()
	if err != nil {
		return Inspection{}, err
	}
	a, err := runtime.InspectDSMName(ctx)
	if err != nil {
		return Inspection{}, err
	}
	return Inspection{Package: "dsm", Name: "DSM", Ready: true, Hostname: a.DSM.Hostname, Port: a.DSM.Port, Pin: &a.DSM.Pin, Token: confirmationToken(state.NextRev, []Approval{a}), ReuseName: a.ReuseName}, nil
}
func (s *Service) setDSMApp(ctx context.Context, in PublicRequest) (Mutation, error) {
	// No list Clear, bulk removal or implicit provider switch may withdraw DSM.
	if !in.Enabled || len(in.Approvals) != 1 {
		return Mutation{}, ErrInvalid
	}
	given, ok := in.Approvals["dsm"]
	if !ok {
		return Mutation{}, ErrInvalid
	}
	runtime, ok := s.Lifecycle.Runtime.(DSMNameRuntime)
	if !ok {
		return Mutation{}, ErrUnavailable
	}
	ctx = WithHostnames(ctx, map[string]string{"": given.Hostname})
	ctx = connector.WithNameReuse(ctx, given.ReuseName)
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.Lifecycle.Store.Load()
	if err != nil {
		return Mutation{}, err
	}
	a, err := runtime.InspectDSMName(ctx)
	if err != nil {
		return Mutation{}, err
	}
	if given.Port != a.DSM.Port || !reflect.DeepEqual(given.Pin, a.DSM.Pin) || given.Token == "" || given.Token != confirmationToken(state.NextRev, []Approval{a}) {
		return Mutation{}, ErrConfirmationChanged
	}
	a.ExpectedRevision = state.NextRev
	if err = runtime.OpenDSMName(ctx, a); err != nil {
		return Mutation{}, err
	}
	return Mutation{Results: []Result{{Package: "dsm", OK: true}}}, nil
}
