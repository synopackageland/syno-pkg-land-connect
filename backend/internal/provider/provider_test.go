package provider

import (
	"context"
	"testing"
)

func TestFakeIsExplicitAndNeverAcceptsRealCredentials(t *testing.T) {
	p := Fake{}
	ctx := context.Background()
	if p.ID() != "fake" {
		t.Fatal("id")
	}
	for _, token := range []string{"", "real-secret", "demo-fail"} {
		if _, err := p.Connect(ctx, token); err == nil {
			t.Fatal("accepted", token)
		}
	}
	s, err := p.Connect(ctx, "demo-connect")
	if err != nil || !s.Connected || s.ShareURL != "https://nas-demo.invalid/share/example" {
		t.Fatalf("%v %v", s, err)
	}
	if err := p.Disconnect(ctx, s); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := p.Connect(ctx, "demo-connect"); err == nil {
		t.Fatal("cancel ignored")
	}
}
