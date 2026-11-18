// Package signalbridge holds the Worker that carries the self-hosted NetBird
// server's Signal stream through Cloudflare; see docs/netbird-server.md.
// It is plain JavaScript with no build step, tested by
// tests/signal-bridge.test.mjs.
package signalbridge

import _ "embed"

//go:embed bridge.js
var script string

// Script returns the ES module. Its hash is its version, so a Connect update
// that changes it replaces the deployed Worker.
func Script() string { return script }
