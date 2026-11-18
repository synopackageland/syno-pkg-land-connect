package relay

import (
	"context"
	"time"
)

func (s *session) waitWS() func() {
	s.wsBlocked.Add(1)
	return func() {
		s.wsResumed.Store(time.Now().UnixNano())
		s.wsBlocked.Add(-1)
	}
}

// A full queue pauses the shared reader, including the peer's heartbeat pong.
func (s *session) wsBackpressured(now time.Time) bool {
	return s.wsBlocked.Load() != 0 || now.Sub(time.Unix(0, s.wsResumed.Load())) < heartbeatGrace
}

func (s *session) reserveWS(ctx context.Context, size int64) bool {
	var resume func()
	defer func() {
		if resume != nil {
			resume()
		}
	}()
	for {
		if ctx.Err() != nil {
			return false
		}
		used := s.wsBytes.Load()
		if used+size <= wsBufferBytes && s.wsBytes.CompareAndSwap(used, used+size) {
			return true
		}
		if resume == nil {
			resume = s.waitWS()
		}
		select {
		case <-ctx.Done():
			return false
		case <-s.wsBudgetWake:
		}
	}
}

func (s *session) releaseWS(size int64) {
	s.wsBytes.Add(-size)
	select {
	case s.wsBudgetWake <- struct{}{}:
	default:
	}
}
