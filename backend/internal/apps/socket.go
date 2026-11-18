package apps

import (
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"

	"dsmapp/internal/localipc"
	"dsmapp/internal/publicname"
)

// Quick Tunnels reserve one socket before Cloudflare issues their public name.
// The listener starts only after that exact observed authority is bound.
const QuickDSMSocketID = "dsm-quick"

func QuickDSMHost(host string) bool {
	label, ok := strings.CutSuffix(host, ".trycloudflare.com")
	return ok && publicname.LabelPattern.MatchString(label)
}

// PinSocket preserves persisted approval IDs while bounding SDK socket names.
// A separate prefix keeps data-plane sockets disjoint from worker control.
func PinSocket(id string) string {
	if !socketPattern.MatchString(id) {
		return ""
	}
	sum := sha256.Sum256([]byte(id))
	return filepath.Join(localipc.RunDir, fmt.Sprintf("pin-%x.sock", sum[:24]))
}
