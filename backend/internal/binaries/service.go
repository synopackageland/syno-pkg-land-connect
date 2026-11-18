package binaries

import (
	"context"
	"errors"
	"sync"
	"time"
)

// InstalledHook is called by connectd after a verified pointer change. The
// provider owns serialization with Start/Stop, Refresh, readiness and recovery.
// A nil hook only downloads; it never restarts or silently substitutes a runtime.
type InstalledHook func(context.Context, string, map[string]string) error

// Service owns bounded jobs in the long-lived daemon, independent of CGI request
// cancellation. Each product has at most one job; jobs of different products
// queue and run one at a time, so the filesystem lock never refuses them.
type Service struct {
	// CurrentVersion is an optional startup-only observer, not an activation hook.
	CurrentVersion   func(string) string
	manager          *Manager
	hook             InstalledHook
	ctx              context.Context
	cancel           context.CancelFunc
	mu               sync.Mutex
	closed, draining bool
	jobs             map[string]*job
	queue            []string
	wg               sync.WaitGroup
}

// job is the latest request for one product: active while queued or running,
// then it keeps the failure until the next request for that product.
type job struct {
	action, lastError string
	active            bool
}

func NewService(m *Manager, hook InstalledHook) *Service {
	ctx, cancel := context.WithCancel(context.Background())
	return &Service{manager: m, hook: hook, ctx: ctx, cancel: cancel, jobs: map[string]*job{}}
}
func (s *Service) Call(ctx context.Context, action, product string) (Status, error) {
	if e := ctx.Err(); e != nil {
		return Status{}, e
	}
	if e := validProduct(product); e != nil {
		return Status{}, e
	}
	if action == "status" {
		return s.status(product)
	}
	if action != "check" && action != "update" && action != "rollback" {
		return Status{}, errors.New("unknown binary action")
	}
	// Refuse a rollback with nothing to return to before queuing it, so no
	// product is left showing a failed job for a request that never ran.
	if action == "rollback" {
		if v, e := s.manager.Status(product); e == nil && v.Previous == "" {
			return Status{}, ErrNoPrevious
		}
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return Status{}, errors.New("binary service stopped")
	}
	if j := s.jobs[product]; j != nil && j.active {
		s.mu.Unlock()
		return Status{}, ErrBusy
	}
	s.jobs[product] = &job{action: action, active: true}
	s.queue = append(s.queue, product)
	if !s.draining {
		s.draining = true
		s.wg.Add(1)
		go s.drain()
	}
	s.mu.Unlock()
	return s.status(product)
}
func (s *Service) status(product string) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, e := s.manager.Status(product)
	if e != nil {
		return v, e
	}
	if s.CurrentVersion != nil {
		v.RuntimeVersion = s.CurrentVersion(product)
	}
	if j := s.jobs[product]; j != nil {
		v.Busy = j.active
		if j.active && (v.Phase == "idle" || v.Phase == "ready" || v.Phase == "failed") {
			v.Phase = "queued"
		}
		if !j.active && j.lastError != "" {
			v.Phase = "failed"
			v.Error = j.lastError
		}
	}
	return v, nil
}

// drain runs queued jobs in order and exits once the queue is empty.
func (s *Service) drain() {
	defer s.wg.Done()
	for {
		s.mu.Lock()
		if len(s.queue) == 0 {
			s.draining = false
			s.mu.Unlock()
			return
		}
		product := s.queue[0]
		s.queue = s.queue[1:]
		j := s.jobs[product]
		s.mu.Unlock()
		e := s.work(j.action, product)
		s.mu.Lock()
		if e != nil {
			j.lastError = e.Error()
		}
		j.active = false
		s.mu.Unlock()
	}
}
func (s *Service) work(action, product string) error {
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Minute)
	defer cancel()
	var e error
	switch action {
	case "check":
		e = s.manager.Check(ctx, product)
	case "update":
		e = s.manager.Update(ctx, product)
	case "rollback":
		e = s.manager.Rollback(ctx, product)
	}
	if e == nil && action != "check" && s.hook != nil {
		e = s.manager.Activate(ctx, product, func(ctx context.Context, paths map[string]string) error { return s.hook(ctx, product, paths) })
	}
	return e
}
func (s *Service) Close() error {
	s.mu.Lock()
	s.closed = true
	s.cancel()
	s.mu.Unlock()
	done := make(chan struct{})
	go func() { s.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("binary worker shutdown incomplete")
	}
}
