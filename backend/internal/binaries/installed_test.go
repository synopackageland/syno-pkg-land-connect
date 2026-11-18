package binaries

import (
	"context"
	"testing"
)

type ownerProbe struct{ ids []string }

func (o *ownerProbe) UpdateBinaries(_ context.Context, id string, _ func() (func() error, func() error, error)) error {
	o.ids = append(o.ids, id)
	return nil
}

func TestInstalledHookActivatesEachProductForItsConnection(t *testing.T) {
	owner := &ownerProbe{}
	hook := RuntimeInstalledHook(owner, nil)
	for _, product := range []string{"cloudflared", "tailscale", "netbird", "unknown"} {
		if e := hook(context.Background(), product, nil); e != nil {
			t.Fatal(e)
		}
	}
	if len(owner.ids) != 3 || owner.ids[0] != "cloudflare" || owner.ids[1] != "tailscale" || owner.ids[2] != "netbird" {
		t.Fatalf("owners %q", owner.ids)
	}
}

type serverProbe struct{ prepared []error }

func (s *serverProbe) UpdateBinary(_ context.Context, prepare func() (func() error, func() error, error)) error {
	_, _, e := prepare()
	s.prepared = append(s.prepared, e)
	return e
}

func TestServerHookActivatesOnlyItsProduct(t *testing.T) {
	owner, server := &ownerProbe{}, &serverProbe{}
	m := New(t.TempDir(), nil)
	hook := WithServer(RuntimeInstalledHook(owner, nil), "netbird-server", server, NewRuntimeAdapter(m))
	if e := hook(context.Background(), "netbird", nil); e != nil || len(owner.ids) != 1 || len(server.prepared) != 0 {
		t.Fatal("client update reached the server", e)
	}
	// Nothing installed: the server gets the refusal, never a substitute.
	if e := hook(context.Background(), "netbird-server", nil); e == nil || len(server.prepared) != 1 || len(owner.ids) != 1 {
		t.Fatal("server update", e)
	}
}
