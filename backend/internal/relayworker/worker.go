// Package relayworker holds the bundled Cloudflare Worker uploaded by Connect.
// Rebuild with (cd relay/worker && npm ci && npm run build) from the repo root.
package relayworker

import _ "embed"

//go:embed worker.js
var script string

// Script returns the self-contained ES module; no NAS JavaScript runtime is needed.
func Script() string { return script }
