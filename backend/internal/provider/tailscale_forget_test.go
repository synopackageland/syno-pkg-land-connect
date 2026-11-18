package provider

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestTailscaleOffKeepsNodeKeyAndForgetDeletesIt(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "tailscaled.state")
	if e := os.WriteFile(state, []byte("SYNTHETIC-NODE"), 0600); e != nil {
		t.Fatal(e)
	}
	p := NewTailscale(dir, nil)
	if e := p.Disable(); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(state); e != nil {
		t.Fatal("off deleted the node key", e)
	}
	// tailscaled saves a machine key before any sign-in: not yet a device.
	if p.Status().KeptIdentity {
		t.Fatal("a node that never signed in is called registered")
	}
	markRegistered(dir)
	if !p.Status().KeptIdentity {
		t.Fatal("a signed-in node kept off is not called registered")
	}
	p.saved.Enabled = true
	if e := p.Forget(); !errors.Is(e, ErrConnectionEnabled) {
		t.Fatal("forgot a live node", e)
	}
	p.saved.Enabled = false
	if e := p.Forget(); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(state); !os.IsNotExist(e) {
		t.Fatal("node key kept", e)
	}
	if registered(dir) || p.Status().KeptIdentity {
		t.Fatal("forgotten node still called registered")
	}
}

func TestNetBirdCallsOnlyASignedInPeerRegistered(t *testing.T) {
	dir := t.TempDir()
	p := NewNetBird(dir, nil)
	writeIdentity(t, dir, "PEER-A")
	if p.Status().KeptIdentity {
		t.Fatal("a peer that never connected is called registered")
	}
	markRegistered(filepath.Join(dir, "client"))
	if !p.Status().KeptIdentity {
		t.Fatal("a connected peer kept off is not called registered")
	}
	if e := p.Forget(); e != nil {
		t.Fatal(e)
	}
	if p.Status().KeptIdentity {
		t.Fatal("forgotten peer still called registered")
	}
}
