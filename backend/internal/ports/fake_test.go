package ports

import (
	"context"
	"sync"
)

// Fake records complete replacement plans without networking or process effects.
// Error injection is configured before Run, or between sequential Step calls.
type Fake struct {
	mu          sync.Mutex
	history     []Plan
	current     Plan
	ApplyError  error
	RevokeError error
}

func (f *Fake) Apply(ctx context.Context, p Plan) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	if f.ApplyError != nil {
		return f.ApplyError
	}
	f.current = clone(p)
	f.history = append(f.history, clone(p))
	return nil
}
func (f *Fake) Revoke(ctx context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e := ctx.Err(); e != nil {
		return e
	}
	if f.RevokeError != nil {
		return f.RevokeError
	}
	f.current = Plan{Mappings: []Mapping{}}
	return nil
}
func (f *Fake) Plans() []Plan {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Plan, len(f.history))
	for i, p := range f.history {
		out[i] = clone(p)
	}
	return out
}
func (f *Fake) Current() Plan { f.mu.Lock(); defer f.mu.Unlock(); return clone(f.current) }
