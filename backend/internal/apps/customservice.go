package apps

import (
	"context"
	"sort"
	"sync"
	"time"
)

// customApps turns the saved definitions into App rows. A row is always
// ready to approve: an App that is not running yet can still be approved.
// With probe, each target gets one bounded TCP check for the list only.
func (s *Service) customApps(ctx context.Context, state State, probe bool) []App {
	ids := make([]string, 0, len(state.Custom))
	for id := range state.Custom {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]App, len(ids))
	var wg sync.WaitGroup
	for i, id := range ids {
		c := state.Custom[id]
		protection := Protection{Mode: "custom", Passthrough: []string{}, OptionalPassthrough: []string{}}
		if c.AppSignIn {
			protection.UpstreamAccess = "public"
		}
		out[i] = App{Package: id, Name: c.Name, Port: c.Port(), PingStatus: Ready, PublicCapable: true,
			External: ExternalPolicy{Connect: "optional", OriginChange: "none"}, Protection: protection, Summary: c.Digest(),
			Custom: &CustomView{Ingress: c.Ingress, URL: c.URL, AppSignIn: c.AppSignIn, InsecureTLS: c.InsecureTLS}}
		if !probe {
			continue
		}
		wg.Add(1)
		go func(a *App, c CustomApp) {
			defer wg.Done()
			ok := s.Guard.Reachable(ctx, c, 1500*time.Millisecond)
			a.Custom.Reachable = &ok
			if !ok {
				a.PingStatus, a.Waiting = Waiting, true
			}
		}(&out[i], c)
	}
	wg.Wait()
	return out
}

// CustomAPI is the page's add/edit/remove/pause and pick list. Resuming a
// paused custom App uses the ordinary reenable approval.
type CustomAPI interface {
	CustomAdd(context.Context, CustomRequest) (CustomResult, error)
	CustomEdit(context.Context, CustomRequest) (CustomResult, error)
	CustomRemove(context.Context, CustomID) error
	CustomPause(context.Context, CustomID) error
	CustomCandidates(context.Context) (Candidates, error)
}

// CustomRuntime is the daemon owner's side: definitions change in the same
// locked approval store, and the runtime restarts or withdraws the proxy.
type CustomRuntime interface {
	AddCustom(context.Context, CustomRequest) (string, error)
	EditCustom(context.Context, CustomRequest) error
	RemoveCustom(context.Context, string) error
	PauseCustom(context.Context, string) error
	CustomCandidates(context.Context) ([]Candidate, error)
}

func (s *Service) customRuntime() (CustomRuntime, error) {
	r, ok := s.Lifecycle.Runtime.(CustomRuntime)
	if !ok {
		return nil, ErrUnavailable
	}
	return r, nil
}

func (s *Service) CustomAdd(ctx context.Context, in CustomRequest) (CustomResult, error) {
	r, err := s.customRuntime()
	if err != nil || in.ID != "" {
		return CustomResult{}, firstErr(err, ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	id, err := r.AddCustom(ctx, in)
	return CustomResult{ID: id}, err
}
func (s *Service) CustomEdit(ctx context.Context, in CustomRequest) (CustomResult, error) {
	r, err := s.customRuntime()
	if err != nil || !customIDPattern.MatchString(in.ID) {
		return CustomResult{}, firstErr(err, ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return CustomResult{ID: in.ID}, r.EditCustom(ctx, in)
}
func (s *Service) CustomRemove(ctx context.Context, in CustomID) error {
	r, err := s.customRuntime()
	if err != nil || !customIDPattern.MatchString(in.ID) {
		return firstErr(err, ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return r.RemoveCustom(ctx, in.ID)
}
func (s *Service) CustomPause(ctx context.Context, in CustomID) error {
	r, err := s.customRuntime()
	if err != nil || !customIDPattern.MatchString(in.ID) {
		return firstErr(err, ErrInvalid)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return r.PauseCustom(ctx, in.ID)
}

// CustomCandidates is read-only and needs no mutation lock.
func (s *Service) CustomCandidates(ctx context.Context) (Candidates, error) {
	r, err := s.customRuntime()
	if err != nil {
		return Candidates{}, err
	}
	list, err := r.CustomCandidates(ctx)
	return Candidates{Candidates: list}, err
}

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

func (c Client) CustomAdd(ctx context.Context, in CustomRequest) (out CustomResult, err error) {
	err = c.exchange(ctx, "customadd", in, &out)
	return
}
func (c Client) CustomEdit(ctx context.Context, in CustomRequest) (out CustomResult, err error) {
	err = c.exchange(ctx, "customedit", in, &out)
	return
}
func (c Client) CustomRemove(ctx context.Context, in CustomID) error {
	var out map[string]bool
	return c.exchange(ctx, "customremove", in, &out)
}
func (c Client) CustomPause(ctx context.Context, in CustomID) error {
	var out map[string]bool
	return c.exchange(ctx, "custompause", in, &out)
}
func (c Client) CustomCandidates(ctx context.Context) (out Candidates, err error) {
	err = c.exchange(ctx, "customcandidates", struct{}{}, &out)
	return
}
