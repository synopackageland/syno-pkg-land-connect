package connector

import (
	"os/exec"
	"testing"
)

func TestStartOwnedStopsItsChild(t *testing.T) {
	// A local process fixture never contacts any provider or NAS.
	p, e := StartOwned(exec.Command("/bin/sh", "-c", "trap 'exit 0' TERM; while :; do sleep 0.05; done"))
	if e != nil {
		t.Fatal(e)
	}
	if !p.Alive() {
		t.Fatal("child not running")
	}
	if e = p.Stop(); e != nil || p.Alive() {
		t.Fatal("child not stopped", e)
	}
}
