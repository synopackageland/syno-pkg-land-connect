package ports

import (
	"context"
	"dsmapp/internal/connector"
	"errors"
	"reflect"
	"time"
)

// Reconciler has a single owner goroutine. Step is the deterministic test seam.
// Polling reopens the active file and desired settings on every observation, so
// atomic file replacement, CGI writes and missed optional hints are all covered.
type Reconciler struct {
	Store     Store
	Detector  Detector
	Applier   Applier
	Debounce  time.Duration
	OnStatus  func(Snapshot, error)
	ownerKey  string
	started   bool
	applied   Plan
	candidate Plan
	since     time.Time
}

func clone(p Plan) Plan { return Plan{Mappings: append([]Mapping{}, p.Mappings...)} }

// Provider recovery includes verified binary hashing and bounded login startup.
// Match the connect budget; cancellation and the three-second revoke budget remain.
func (r *Reconciler) apply(ctx context.Context, p Plan, v *Snapshot) error {
	bounded, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if r.Applier == nil {
		return errors.New("port applier unavailable")
	}
	if e := r.Applier.Apply(bounded, clone(p)); e != nil {
		if connector.IsPreserved(e) {
			v.MappingState = "needs-attention"
			return e
		}
		cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		v.MappingState = "needs-attention"
		if errors.Is(e, ErrProviderPending) {
			v.MappingState = "pending-provider"
		}
		if r.Applier.Revoke(cleanup) != nil {
			v.MappingState = "cleanup-required"
		}
		r.started = false
		r.applied = Plan{Mappings: []Mapping{}}
		if errors.Is(e, ErrProviderPending) {
			return ErrProviderPending
		}
		return errors.New("port mappings could not be applied")
	}
	r.applied = clone(p)
	return nil
}
func (r *Reconciler) Step(ctx context.Context, now time.Time) (Snapshot, error) {
	if owner, ok := r.Applier.(interface{ OwnershipKey() string }); ok {
		key := owner.OwnershipKey()
		if key != r.ownerKey {
			r.started = false
			r.applied = Plan{Mappings: []Mapping{}}
			r.ownerKey = key
		}
	}
	v, readErr := r.Store.List(ctx, r.Detector)
	if readErr != nil {
		v = Snapshot{Entries: []Entry{}, Detection: "needs-attention", MappingState: "needs-attention", Plan: Plan{Mappings: []Mapping{}}}
	}
	desired := v.Plan
	if !r.started {
		if e := r.apply(ctx, Plan{Mappings: []Mapping{}}, &v); e != nil {
			return v, e
		}
		r.started = true
		r.candidate = clone(desired)
		r.since = now
	}
	// Immediately withdraw obsolete or unsafe mappings, even during debounce.
	safe := Plan{Mappings: []Mapping{}}
	for _, old := range r.applied.Mappings {
		for _, new := range desired.Mappings {
			if old == new {
				safe.Mappings = append(safe.Mappings, old)
				break
			}
		}
	}
	if !reflect.DeepEqual(safe, r.applied) {
		if e := r.apply(ctx, safe, &v); e != nil {
			return v, e
		}
	}
	if !reflect.DeepEqual(r.candidate, desired) {
		r.candidate = clone(desired)
		r.since = now
	}
	if !reflect.DeepEqual(r.applied, desired) {
		v.MappingState = "debouncing"
		if now.Sub(r.since) >= r.Debounce {
			if e := r.apply(ctx, desired, &v); e != nil {
				return v, e
			}
			v.MappingState = "applied"
		}
	} else {
		v.MappingState = "applied"
	}
	if v.Detection != "ready" {
		v.MappingState = "needs-attention"
	}
	return v, readErr
}

// Run can consume optional trusted event hints; correctness relies on polling.
// It retries failed application on the next tick and revokes on shutdown.
func (r *Reconciler) Run(ctx context.Context, interval time.Duration, hints <-chan struct{}) error {
	if interval <= 0 {
		return ErrValidation
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	observe := func(now time.Time) {
		v, e := r.Step(ctx, now)
		if r.OnStatus != nil {
			r.OnStatus(v, e)
		}
	}
	observe(time.Now())
	for {
		select {
		case <-ctx.Done():
			if r.Applier == nil {
				return errors.New("port applier unavailable")
			}
			cleanup, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			e := r.Applier.Revoke(cleanup)
			if r.OnStatus != nil {
				v, _ := r.Store.List(cleanup, r.Detector)
				v.MappingState = "needs-attention"
				if e != nil {
					v.MappingState = "cleanup-required"
				}
				r.OnStatus(v, e)
			}
			return e
		case now := <-ticker.C:
			observe(now)
		case _, ok := <-hints:
			if !ok {
				hints = nil
			} else {
				observe(time.Now())
			}
		}
	}
}
