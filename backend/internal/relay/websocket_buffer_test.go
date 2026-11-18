package relay

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

func TestWebSocketQueueBackpressure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		s := &session{wsBudgetWake: make(chan struct{}, 1), streams: map[uint32]*stream{}}
		st := &stream{s: s, ctx: ctx, cancel: cancel, ws: make(chan frame, 1), websocket: true}
		s.streams[1] = st
		if err := s.receive(textFrame, 1, []byte("first")); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- s.receive(textFrame, 1, []byte("second")) }()
		synctest.Wait()
		if s.wsBlocked.Load() != 1 || ctx.Err() != nil {
			t.Fatal("full queue must wait without canceling stream")
		}
		first := <-st.ws
		s.releaseWS(int64(len(first.payload)))
		synctest.Wait()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		second := <-st.ws
		if string(first.payload) != "first" || string(second.payload) != "second" {
			t.Fatal("message order changed")
		}
		s.releaseWS(int64(len(second.payload)))
		if s.wsBytes.Load() != 0 {
			t.Fatal("payload reservation leaked")
		}
	})
}

func TestWebSocketBudgetBackpressure(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := &session{wsBudgetWake: make(chan struct{}, 1)}
		s.wsBytes.Store(wsBufferBytes)
		done := make(chan bool, 1)
		go func() { done <- s.reserveWS(context.Background(), 1) }()
		synctest.Wait()
		if s.wsBytes.Load() != wsBufferBytes || s.wsBlocked.Load() != 1 {
			t.Fatal("budget overflow did not wait at cap")
		}
		s.releaseWS(1)
		synctest.Wait()
		if !<-done || s.wsBytes.Load() != wsBufferBytes {
			t.Fatal("released budget did not resume reservation")
		}
		s.releaseWS(wsBufferBytes)
	})
}

func TestWebSocketBudgetCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		s := &session{wsBudgetWake: make(chan struct{}, 1)}
		s.wsBytes.Store(wsBufferBytes)
		done := make(chan bool, 1)
		go func() { done <- s.reserveWS(ctx, 1) }()
		synctest.Wait()
		cancel()
		synctest.Wait()
		if <-done || s.wsBytes.Load() != wsBufferBytes || s.wsBlocked.Load() != 0 {
			t.Fatal("canceled reservation leaked state")
		}
	})
}

func TestWebSocketQueueCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		s := &session{wsBudgetWake: make(chan struct{}, 1), streams: map[uint32]*stream{}}
		st := &stream{s: s, ctx: ctx, cancel: cancel, ws: make(chan frame, 1), websocket: true}
		s.streams[1] = st
		if err := s.receive(textFrame, 1, []byte("first")); err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { done <- s.receive(textFrame, 1, []byte("second")) }()
		synctest.Wait()
		cancel()
		synctest.Wait()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		st.drainWS()
		if s.wsBytes.Load() != 0 || s.wsBlocked.Load() != 0 {
			t.Fatal("canceled queue leaked state")
		}
	})
}

func TestWebSocketBackpressureHeartbeat(t *testing.T) {
	now := time.Now()
	s := &session{}
	resume := s.waitWS()
	if !s.wsBackpressured(now.Add(24 * time.Hour)) {
		t.Fatal("blocked reader must not time out its queued pong")
	}
	resume()
	s.wsResumed.Store(now.UnixNano())
	if !s.wsBackpressured(now.Add(heartbeatGrace - time.Nanosecond)) {
		t.Fatal("resumed reader needs time to receive its pong")
	}
	if s.wsBackpressured(now.Add(heartbeatGrace)) {
		t.Fatal("heartbeat timeout must resume after backpressure")
	}
}
