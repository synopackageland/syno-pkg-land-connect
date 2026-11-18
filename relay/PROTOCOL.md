# Syno Connect Center workers.dev relay protocol

The frame protocol is hostc v5, forked at
`88cdb41cb837cf81b7a5180916839348c1989bbf` (Apache-2.0).
Only discovery, authentication and tunnel lifetime differ. No client-side
Node runtime is needed; the NAS connector is implemented by Syno Connect Center in Go.

## Deployment contract

One Worker per public App (and a separate Worker for the DSM sign-in hop).
All public paths, queries and methods go to `TUNNEL.getByName("default")`;
there is no wildcard hostname, tunnel creation API or public tunnel selector.
The single reserved path is `/_spl/relay/connect`. App paths such as
`/api/tunnels` remain available to the App.

Upload `../backend/internal/relayworker/worker.js` as ES module `worker.js`, with metadata:

```json
{
  "main_module": "worker.js",
  "compatibility_date": "2026-10-24",
  "compatibility_flags": ["enable_request_signal"],
  "bindings": [
    {"type": "durable_object_namespace", "name": "TUNNEL", "class_name": "Tunnel"}
  ],
  "migrations": {"new_tag": "v1", "new_sqlite_classes": ["Tunnel"]},
  "observability": {"enabled": false}
}
```

The SQLite migration is required for free-plan DOs. Apply it only on the
initial upload; subsequent updates retain the namespace and migration tag.
Keep bindings when updating the Worker (including `RELAY_SECRET`). Set the
secret before enabling the public route. Disable preview URLs as well as
keeping observability off, to avoid a second public origin and request logs.
The deployment API and minimum token scopes are in [PERMISSIONS.md](PERMISSIONS.md).

## Connector handshake and secret

Generate an independent random 32-byte secret per Worker, encoded as 64
lowercase hexadecimal ASCII characters. The Worker `secret_text` binding is
`RELAY_SECRET`; Syno Connect Center keeps the same ASCII value in its private 0600 file
inside a 0700 directory. The HMAC key is those **ASCII characters**, not their
hex-decoded bytes. Do not send the secret, API token or session credentials in
URLs, public request headers, logs or browser-visible state.

Syno Connect Center opens `wss://<worker>.<account-subdomain>.workers.dev/_spl/relay/connect`:

```text
GET /_spl/relay/connect
Upgrade: websocket
Hostc-Protocol: 5
Authorization: Bearer <base64url(HMAC-SHA256(secret-as-UTF8, "connect:default")))>
```

The base64url value has no padding. There is no timestamp or challenge; this
is a reusable connector credential, valid until secret rotation/deletion.
TLS is required on real deployments. A bearer copied from one Worker does
not authenticate to another with an independent secret. No browser `Origin`
is required; this endpoint is for the NAS connector. Missing/wrong protocol
or upgrade returns 426; invalid credentials return 401; success is 101.

A new authenticated connector replaces the previous one (close 4001), aborts
its HTTP streams and closes its public WebSockets. Fixed tunnel identity and
URL survive disconnects, alarms and close 4000 (intentional shutdown); public
requests while offline return 502. Reconnect with backoff; do not redeploy to
reconnect. Persistent stream-id reservations prevent reuse across hibernation
and connector replacement. Worker deletion ends the service.

## Frames

Each binary WebSocket message is exactly one frame. All integers use unsigned
network byte order (big endian): `u8 type | u32 stream_id | payload`.
Stream id 0 is invalid. The Worker allocates ids monotonically, reserving blocks
of 1024 in DO storage; gaps are expected. Treat ids as opaque uint32 values.
Frames for already finished streams can be ignored.

| Type | Name | Direction | Payload |
| --- | --- | --- | --- |
| 1 | OPEN | Worker → connector | UTF-8 JSON described below |
| 2 | HEAD | connector → Worker | UTF-8 JSON described below |
| 3 | DATA | both | HTTP bytes or one complete binary WS message |
| 4 | TEXT | both | one complete UTF-8 WS text message |
| 5 | END | both | HTTP: empty; WS: u16 close code + UTF-8 reason |
| 6 | RESET | both | UTF-8 diagnostic; abort both directions |
| 7 | WINDOW | both | nonzero u32 additional HTTP byte credit |

OPEN: `{"method":"GET","path":"/path?query=1","headers":[["cookie","a=b"]],"body":false}`.
`path` is origin-form and starts with `/`; headers are ordered pairs, preserving
repeated values. For WebSocket upgrades, OPEN additionally contains
`"websocket":["offered-protocol", "other"]` (empty array is valid), and body is false.

HEAD: `{"status":200,"headers":[["content-length","123"]],"body":true}`.
Send once, before DATA. Set body false for HEAD/204/205/304; status 101 is only
valid for a WebSocket upgrade. Successful WS HEAD has status 101, body false,
and optional `"protocol":"selected-protocol"`, which must have been offered.
A rejected WS upgrade uses its HTTP status/headers with no response body.
Repeated `set-cookie` values must remain separate pairs.

## Flow control and limits

At OPEN, **each HTTP direction** starts with 262144 bytes of credit.
DATA consumes payload length; WINDOW adds credit. Only grant bytes as the
local consumer drains them. Never wait for request END before sending HEAD:
SSE and full duplex requests must stream. Each HTTP DATA is at most 65536 bytes.
END terminates only that direction and consumes no credit. RESET aborts both
and cancels the local request. A response can finish before upload completion;
then the Worker sends RESET to stop the upload.

The Worker's response window can grow to 4 MiB per stream, within a shared
16 MiB extra budget. Accept arbitrary positive WINDOW grants (do not assume
one chunk/window size), and apply backpressure. At most 256 concurrent HTTP
and WS streams per tunnel; excess public requests return 503. Waiting for HEAD
times out at 120 seconds (504 plus RESET). Long-running bodies have no protocol
deadline. Bad framing/direction, duplicate HEAD or exceeded response credit
closes the connector with 1002; malformed response headers fail the affected
stream with RESET/502. Do not retry non-idempotent requests after disconnect.

WebSocket messages are forwarded whole, with a maximum payload of 1048512
bytes; they do not use WINDOW. END preserves close code/reason where legal
(reserved codes become 1000; reason is capped to 123 UTF-8 bytes).

Every 15 seconds the connector sends the **text** message `hostc:ping` and
expects text `hostc:pong` within 10 seconds. The DO runtime auto-responds while
hibernating. After 45 seconds without heartbeat the Worker treats the connector
as stale. Ordinary text `ping`/`pong` on public WebSockets is forwarded to the
App; `hostc:ping` is reserved on all tunnel sockets by the hibernation runtime.
An alarm checks stale connectors every ten minutes; public requests also check
staleness. Idle connector and public WebSocket attachments survive hibernation;
HTTP streams keep the object awake until completion.

## Origin and proxy boundary

The Worker strips hop-by-hop headers (including names listed by Connection),
Cloudflare-specific request headers, `cdn-loop`, and Host. The connector must
set Host to its **configured Worker public authority**, and dial only the
approved App's pin-proxy Unix socket. Never derive a destination from OPEN,
Host, forwarded headers or query parameters. Preserve Origin/Referer and
Location; hostc's local-origin rewriting is intentionally not required here.
Never add a NAS TCP listener or bypass pin verification.

Response Content-Length, Content-Range, Accept-Ranges and Content-Encoding are
preserved. Known-length bodies use Cloudflare FixedLengthStream (a header alone
is ignored by the runtime); wrong byte counts fail the stream. Compressed bodies
are passed through with manual encoding. Cookies lose Domain so each Worker
origin can only set host cookies. This is transport, not App authentication:
DSM/App sign-in and approved state/projection remain Syno Connect Center/SDK responsibilities.
