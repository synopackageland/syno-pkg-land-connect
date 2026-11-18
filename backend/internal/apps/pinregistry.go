package apps

import (
	"context"
	"errors"
	"sync"
)

// PinRegistry owns the live per-scope proxies. The durable state owner calls
// Stop immediately after persisting a pause/revoke, before projection/cleanup.
// Observe re-reads discovery, MagicDNS and ports.DatastoreDetector each poll.
// The daemon runs its Check through connector.RunSafetyChecks while Apps are open.
type PinRegistry struct {
	mu      sync.Mutex
	proxies map[string]*PinProxy
	Observe func(context.Context, PinBinding) (PinObservation, error)
}

// Set replaces a socket's old binding without reviving any old connections.
func (r *PinRegistry) Set(id string, p *PinProxy) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.proxies == nil {
		r.proxies = map[string]*PinProxy{}
	}
	if old := r.proxies[id]; old != nil {
		old.Stop()
	}
	if p == nil {
		delete(r.proxies, id)
	} else {
		r.proxies[id] = p
	}
}
func (r *PinRegistry) Stop(id string) { r.Set(id, nil) }
func (r *PinRegistry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, p := range r.proxies {
		p.Stop()
		delete(r.proxies, id)
	}
}
func (r *PinRegistry) Check(ctx context.Context) error {
	r.mu.Lock()
	ps := make([]*PinProxy, 0, len(r.proxies))
	for _, p := range r.proxies {
		ps = append(ps, p)
	}
	r.mu.Unlock()
	// Concurrent target checks keep one unavailable reference from delaying
	// refusal of another target. All work is joined and bounded by ctx.
	errs := make(chan error, len(ps))
	var wg sync.WaitGroup
	for _, p := range ps {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if !p.active() {
				if err := p.Check(ctx, PinObservation{}); err != nil {
					errs <- err
				}
				return
			}
			if r.Observe == nil {
				p.waiting.Store(true)
				p.closeConnections()
				errs <- errors.New("pin discovery unavailable")
				return
			}
			o, err := r.Observe(ctx, p.cfg.Binding)
			if err != nil {
				p.waiting.Store(true)
				p.closeConnections()
				errs <- err
				return
			}
			if err = p.Check(ctx, o); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	var all []error
	for err := range errs {
		all = append(all, err)
	}
	return errors.Join(all...)
}
