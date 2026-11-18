# Syno Connect Center relay Worker

A small fork of [hostc](https://github.com/akazwz/hostc)'s server and protocol,
with one permanent Durable Object per Worker, per-Worker HMAC authentication,
WebSocket hibernation, streaming backpressure and exact Content-Length/Range.
See [NOTICE](NOTICE), [LICENSE](LICENSE), [wire protocol](../PROTOCOL.md) and
[Cloudflare permissions](../PERMISSIONS.md).

From this directory, on a build machine with Node.js 24 or later:

```sh
npm ci
npm run build
npm run check
```

The deterministic build writes a single ES module to
`backend/internal/relayworker/worker.js` (relative to the repository root),
embedded into the Go `relayworker` package. `relayworker.Script()` returns its
source for the deployment API. Only `cloudflare:workers` is external: it is a
Cloudflare runtime module, not an npm/NAS dependency. The NAS needs no Node.js
or npm. The committed generated artifact is checked for freshness by
`node build.mjs --check`; runtime changes require a rebuild. Preserve both
NOTICE and Apache-2.0 license when distributing the embedded source.

Tests run inside the real local Workers runtime (Cloudflare Vitest plugin),
including explicit DO eviction/hibernation, HTTP and WebSocket forwarding,
flow control, permanent reconnects, credential isolation and length failures.
The test runner/dependency overrides apply only to build tools, not NAS runtime
packages; the root/frontend SDK pins are unchanged.

To opt in to the real Cloudflare smoke, pass an env-file path and a fresh
uniquely prefixed Worker name (the file must contain CLOUDFLARE_API_TOKEN and
CLOUDFLARE_ACCOUNT_ID). The test account must already have a workers.dev
subdomain. It refuses to overwrite an existing Worker and deletes its Worker
in finally, checking subsequent GET is 404. It never changes the account-wide
subdomain, DNS, tunnels or other Workers. Do not run against production credentials.

```sh
node test/smoke.mjs /path/to/test.env "${RELAY_TEST_WORKER}"
```

The smoke starts a temporary Unix-socket echo origin and a minimal Node test
connector. It checks HTTP POST/query/authority, cookies, SSE, WebSocket
text/binary/subprotocol, an 8 MiB byte-verified download and a 900000-byte Range.
This harness does not certify the production Go connector, NAS pin proxy,
SDK login/projection, device lifecycle, free-plan quota or billing/idle time.
Those need the final device acceptance after the integrating changes land.
In particular, the App gateway must already support Syno Connect Center's public-origin
projection. Legacy gateways that insist on their local listener port reject a
portless public origin; changing Host to a local origin would bypass the intended
contract and is not a relay workaround.

This change supplies the server and embedded artifact. Syno Connect Center credential
storage, approvals, deployment/reconciliation, projection and production
connector supervision remain the integrating client's responsibility.
