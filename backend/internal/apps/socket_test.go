package apps

import (
	"path/filepath"
	"strings"
	"testing"

	"dsmapp/internal/localipc"
)

func TestPinSocketPreservesAllApprovalIDsInBoundedDisjointNames(t *testing.T) {
	seen := map[string]bool{}
	for _, id := range []string{"scope", "Scope", "control", "helper", strings.Repeat("a", 64)} {
		path := PinSocket(id)
		if filepath.Dir(path) != localipc.RunDir || len(filepath.Base(path)) > 64 ||
			path == localipc.RunDir+"/control.sock" || seen[path] || PinSocket(id) != path {
			t.Fatal("invalid or unstable socket name", id, path)
		}
		seen[path] = true
	}
}
