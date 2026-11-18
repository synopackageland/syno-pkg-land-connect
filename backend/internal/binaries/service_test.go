package binaries

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"
)

type blockingSource struct {
	entered chan struct{}
	release chan struct{}
}

func (s blockingSource) Latest(ctx context.Context, p string) (Release, error) {
	select {
	case s.entered <- struct{}{}:
	default:
	}
	select {
	case <-s.release:
		return Release{Product: p, Version: "1.0.0"}, nil
	case <-ctx.Done():
		return Release{}, ctx.Err()
	}
}
func (blockingSource) Download(_ context.Context, _ Release, path string) (string, error) {
	return "sha256", os.WriteFile(path, []byte("fixture"), 0700)
}
func TestServiceJobOutlivesSubmitRequest(t *testing.T) {
	source := blockingSource{make(chan struct{}, 1), make(chan struct{})}
	svc := NewService(New(t.TempDir(), source), nil)
	defer svc.Close()
	ctx, cancel := context.WithCancel(context.Background())
	s, e := svc.Call(ctx, "update", "cloudflared")
	cancel()
	if e != nil || !s.Busy {
		t.Fatal(s, e)
	}
	<-source.entered
	close(source.release)
	s = waitService(t, svc, "cloudflared")
	if s.Current != "1.0.0" || s.Error != "" {
		t.Fatal(s)
	}
}
func TestServiceQueuesSecondProductInsteadOfRefusing(t *testing.T) {
	source := blockingSource{make(chan struct{}, 1), make(chan struct{})}
	svc := NewService(New(t.TempDir(), source), nil)
	defer svc.Close()
	if _, e := svc.Call(context.Background(), "update", "cloudflared"); e != nil {
		t.Fatal(e)
	}
	<-source.entered
	s, e := svc.Call(context.Background(), "check", "tailscale")
	if e != nil || !s.Busy || s.Phase != "queued" {
		t.Fatal("second product not queued", s, e)
	}
	if s, _ = svc.Call(context.Background(), "status", "netbird"); s.Busy {
		t.Fatal("idle product reported busy", s)
	}
	if _, e = svc.Call(context.Background(), "update", "cloudflared"); !errors.Is(e, ErrBusy) {
		t.Fatal("same product accepted twice", e)
	}
	close(source.release)
	if s = waitService(t, svc, "cloudflared"); s.Current != "1.0.0" || s.Error != "" {
		t.Fatal(s)
	}
	if s = waitService(t, svc, "tailscale"); s.Latest != "1.0.0" || s.Error != "" {
		t.Fatal(s)
	}
}

type productFailingSource struct {
	blockingSource
	failing string
}

func (s productFailingSource) Latest(ctx context.Context, p string) (Release, error) {
	if p == s.failing {
		return Release{}, errors.New("release feed unavailable")
	}
	return s.blockingSource.Latest(ctx, p)
}
func TestServiceFailureStaysOnItsOwnProduct(t *testing.T) {
	source := productFailingSource{blockingSource{make(chan struct{}, 1), make(chan struct{})}, "cloudflared"}
	close(source.release)
	svc := NewService(New(t.TempDir(), source), nil)
	defer svc.Close()
	svc.Call(context.Background(), "check", "cloudflared")
	svc.Call(context.Background(), "check", "tailscale")
	if s := waitService(t, svc, "cloudflared"); s.Phase != "failed" {
		t.Fatal(s)
	}
	if s := waitService(t, svc, "tailscale"); s.Phase == "failed" || s.Error != "" {
		t.Fatal("failure leaked to another product", s)
	}
}
func TestServiceShutdownCancelsDownload(t *testing.T) {
	source := blockingSource{make(chan struct{}, 1), make(chan struct{})}
	m := New(t.TempDir(), source)
	svc := NewService(m, nil)
	if _, e := svc.Call(context.Background(), "update", "cloudflared"); e != nil {
		t.Fatal(e)
	}
	<-source.entered
	if e := svc.Close(); e != nil {
		t.Fatal(e)
	}
	s, _ := m.Status("cloudflared")
	if s.Current != "" {
		t.Fatal(s)
	}
	if _, e := svc.Call(context.Background(), "update", "cloudflared"); e == nil {
		t.Fatal("closed service accepted work")
	}
}
func TestServiceProviderHookFailureDoesNotPretendActivation(t *testing.T) {
	source := blockingSource{make(chan struct{}, 1), make(chan struct{})}
	close(source.release)
	called := false
	svc := NewService(New(t.TempDir(), source), func(_ context.Context, _ string, _ map[string]string) error {
		called = true
		return errors.New("provider restart refused")
	})
	defer svc.Close()
	if _, e := svc.Call(context.Background(), "update", "cloudflared"); e != nil {
		t.Fatal(e)
	}
	s := waitService(t, svc, "cloudflared")
	if !called || s.Phase != "failed" || s.Error == "" {
		t.Fatal(s)
	}
}
func waitService(t *testing.T, s *Service, p string) Status {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		v, e := s.Call(context.Background(), "status", p)
		if e != nil {
			t.Fatal(e)
		}
		if !v.Busy {
			return v
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("job timed out")
	return Status{}
}
