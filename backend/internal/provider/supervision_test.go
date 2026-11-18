package provider

import "testing"

// Every provider daemon restores through the shared supervise path, so a
// repair waits for a slow restore the same way for all of them.
func TestEveryProviderDaemonRestoresThroughTheSharedPath(t *testing.T) {
	for _, p := range []Runtime{&NetBird{}, &Tailscale{}, &CloudflareTunnel{}} {
		if _, ok := p.(backgroundRestorer); !ok {
			t.Errorf("%T repairs without waiting for its background restore", p)
		}
	}
}
