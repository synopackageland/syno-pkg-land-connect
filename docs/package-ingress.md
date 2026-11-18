# Package-owned machine ingress

Syno Connect Center can publish one package-owned reverse WebSocket listener at a fixed
HTTPS hostname. This uses the existing custom App store, administrator approval,
Unix proxy, Cloudflare own-domain tunnel and provider supervisor. It is not a
general port-forwarding API. No NAS or real Cloudflare acceptance is recorded.

## Existing capability and delivered extension

| Existing | Required for a reverse listener | Current implementation |
| --- | --- | --- |
| `provider.Runtime` owns enabled intent, child status and restart backoff | Survive connector interruption without creating another grant | Same Cloudflare owner and supervisor; no per-grant process or retry loop |
| Cloudflare quick/domain/token modes ordinarily publish DSM | Fixed HTTPS hostname with a restricted machine route | Own-domain App ingress only; quick, token-only and Workers publication are not grant transports |
| SDK App discovery reads gateway metadata and approves an entire App | An independent package must request its own restricted listener | Peer-authenticated request creates a pending custom App; existing administrator inspection/approval publishes it |
| Custom Apps forward a configured origin and stream WebSockets | Exactly one listener and one WS path | Immutable ingress metadata pins package, nonzero UID, port, path and hostname into the existing approval digest |
| Custom App admin controls use Syno Connect Center's private control socket | A consumer needs only request/status/revoke on its own grants | A separate, bounded Unix transport exposes those three operations; it provides no admin/provider controls |
| DSM sign-in and optional Cloudflare Access email gates serve people | Machine clients cannot complete browser sign-in | No DSM gate for an ingress grant; Syno Connect Center refuses email-gated activation and refuses enabling its email gate around active grants |
| Custom proxy Stop owns upgraded connections | Revoke must terminate existing WS streams | Durable revoked tombstone, paused approval, then the same proxy Stop closes owned connections |
| Provider status and external DSM probe are separate | Health must describe this listener, not infer external reachability from DSM | Grant status reports listener ownership/health, connector state and local readiness; external verification remains false |

Related: [Cloudflare Access](cloudflare-access.md),
[port mapping](port-mapping-contract.md).

## Consumer API

The consumer runs as its own DSM package account. Its installed, root-owned,
non-group/world-writable `conf/privilege` must declare `defaults.run-as: package`;
the account name must match the package ID. Root, Syno Connect Center's own UID, missing
packages, UID mismatches and user-supplied identity selectors are refused.
Custom DSM account-name overrides are not supported by this contract.

Syno Connect Center listens at `/var/packages/SynoPkgConnect/var/ingress/control.sock`.
Only this directory is traversable (0711) and this socket writable (0666);
the existing `var/run` sockets and private `var/connect` data remain private.
The package var ancestors must permit traversal. The endpoint authenticates
each connection with Linux `SO_PEERCRED`, then verifies the installed package
account and declaration. Socket access alone grants no publication authority.

Wire protocol: one UTF-8 JSON object, at most 4096 bytes, followed by a Unix
write-half close. Read one JSON response, then close. There are at most eight
concurrent clients, a ten-second connection deadline and five-second service
budget. Unknown fields, additional JSON values and other operations fail closed.
Never forward this socket through CGI, a TCP listener or Cloudflare.

### Request

Start a **separate HTTP reverse listener**, bound specifically to IPv4 loopback,
under the consumer's nonzero UID. Wildcard, DNS, IPv6, root and privileged-port
targets are refused. The public transport is HTTPS/WSS; local HTTP carries only
the consumer's WS endpoint. Syngle continues to authenticate agents inside SSH.

```json
{
  "method": "request",
  "package": "syngle",
  "request": {
    "key": "reverse",
    "port": 18080,
    "path": "/reverse/ws",
    "hostname": "reverse.example.com"
  }
}
```

`key` is a lower-case custom label of at most 40 characters. `(package, key)`
selects a stable opaque ID; retain that ID. Identical requests return the same
grant, including a revoked tombstone. Changing UID, listener, path or hostname
under that key returns a conflict. An explicitly new key creates a new pending
request and requires a new administrator approval.

The response starts with `state: pending`, `ready: false` and no URL. Requesting
does not start cloudflared, create DNS, open a forwarding proxy or start a ticker.
All definitions, including revoked tombstones, share the custom App limit of 64.

### Administrator publication

Use the existing Syno Connect Center App inspection/confirmation API, authenticated as a
DSM administrator: `apps.inspect {package: <id>, scope: "public"}`, then
`apps.setpublic` with the returned approval. The returned `custom.ingress`
metadata discloses the owner, path and fixed hostname. The existing custom App
row is used; this change adds no separate frontend panel. Ordinary custom edit
cannot modify an ingress definition. Renaming a fixed hostname requires a new
grant and approval, rather than the ordinary App hostname-edit operation.

The selected Cloudflare own-domain zone must contain the requested hostname;
the usual DNS collision checks apply. Access authorization must already be
configured by the administrator. No consumer can select an account, supply a
Cloudflare credential, enable a provider or authorize itself.

This approval does **not** propose or create a DSM endpoint. When the tunnel has
ingress requests and no explicit public DSM approval, its default DSM hostname
has no service rule and receives the terminal 404. An existing independently
approved DSM or ordinary App hostname retains its separate approval; a machine
grant neither creates nor revokes those existing publications. To operate a
reverse-only deployment, request the grant before first own-domain connection,
or explicitly withdraw any prior DSM/other App public approvals first. The
domain tunnel's existing default DNS name may remain, but has no DSM route.

### Status and health

```json
{"method":"status","package":"syngle","ref":{"id":"<returned-id>"}}
```

Successful responses have a `result` object:

| Field | Meaning |
| --- | --- |
| `id` | Stable consumer reference |
| `state` | `pending`, `active`, `paused` or `revoked`; persisted grant intent |
| `url` | Fixed `https://hostname/path` when approved and active; use WSS for WS |
| `listenerHealthy` | Bounded TCP check verified both the listener and accepted server socket UID; not an application/SSH health claim |
| `connectorState` | The existing Cloudflare runtime state; default `disabled` without an observer |
| `ready` | Active live local proxy, verified listener, connected connector and no Syno Connect Center email gate |
| `externalVerified` | Always false here; no synthetic DSM probe establishes machine reachability |

Errors have `code` and no successful result. Codes include `bad_request`,
`ingress_subject_refused`, `apps_waiting`, `apps_confirmation_changed`,
`apps_state_corrupt`, `apps_unavailable` and `ingress_access_incompatible`.
Unknown/foreign references are refused without returning another grant.

Syngle must keep product enablement blocked until the grant is active/ready and
its actual WSS/SSH handshake succeeds with the expected Controller identity.
Never treat a local status or retained URL as evidence that Cloudflare Access,
DNS, TLS or the public edge accepts a machine. Unmanaged Access applications,
wildcard policies, WAF and external account changes require deployment-side
checks; no live check of those policies has been performed here.

### Revoke

```json
{"method":"revoke","package":"syngle","ref":{"id":"<returned-id>"}}
```

Revocation first persists a tombstone and pauses the existing public approval,
then closes the proxy's accepted connections, including upgraded WebSockets.
It is idempotent. Repeating the original request, restarting Syno Connect Center or ordinary
admin reenable cannot revive it. A consumer can revoke only its own package+UID
grant. Administrator pause/remove continues through existing App controls.

The existing safety owner makes its final route reconciliation on withdrawal.
Failed cloud/config cleanup never restores the proxy. A revoked grant alone
keeps no safety polling loop alive; reconciliation can also run on later explicit
provider/App activity. Revocation does not delete an independently owned tunnel,
Cloudflare account resources or other App routes.

### Go caller framing

The implementation's `internal/ingress.Call` is a Syno Connect Center-internal reference,
not an SDK export. An independent consumer can implement this small wire client
with the standard library: dial Unix, validate the server UID as Syno Connect Center's
nonzero package UID using `SO_PEERCRED`, set a deadline, JSON-encode the request,
call `(*net.UnixConn).CloseWrite`, decode one bounded response, close. Never send
DSM cookies, SynoToken, SSH keys or Cloudflare credentials in these messages.

## Data-plane enforcement

* The Cloudflare config contains an anchored, escaped path rule for the fixed
  hostname, followed by a hostname-specific 404 and the tunnel's terminal 404.
* The custom proxy independently checks exact Host, GET, WebSocket Upgrade,
  Connection upgrade token, exact canonical path and an empty query. Encoded
  aliases, suffixes, absolute-form URLs, other paths and ordinary HTTP fail
  before a dial. The proxy never chooses a target from headers, path or query.
* Every new dial revalidates the installed package account/UID (including the
  canonical UID account name), verifies the literal loopback LISTEN UID through `/proc/net/tcp`
  and checks the accepted reverse TCP tuple's UID before sending any HTTP bytes.
  A listener swap cannot forward bytes to DSM or another package's socket.
  Missing/unreadable evidence refuses; the short accept wait is bounded and
  runs only during that request. Existing streams remain pinned to their socket.
* The persisted approval digest covers the immutable ingress tuple and revoke
  bit. Existing ordinary custom App digests retain their previous encoding.
* There is no per-grant process, ticker, tunnel, router mapping, root helper or
  alternate provider supervisor. The control accept loop belongs to the existing
  connect-worker. Pending and withdrawn grants do not start a safety ticker.

## Local evidence and limits

`apps/ingress_test.go` covers canonical WS requests, invalid origins, approval
digest compatibility, and listener/accepted-socket ownership.
`appaccess/ingress_test.go` uses the actual custom proxy, Unix IPC, approval store,
administrator API and WS peers: pending/idempotency/conflict, cross-owner refusal,
fixed authority, noninteractive gating, no implicit DSM publication, path refusal,
reconnect status, active WS revoke and durable tombstone restore.
`cloudflare/ingress_test.go` exercises the production config renderer and existing
connector restart owner with a fake launcher/metrics peer; the exact path and
default refusal survive restart and disable prevents a successor.

Run `scripts/test-ingress-fixtures.sh` with a unique `CONNECT_FIXTURE_PREFIX` and
a pre-populated Go module cache. Its disposable, network-disabled container runs
Syno Connect Center and a consumer as distinct nonroot UIDs, verifies package identity and
real TCP ownership, and rejects unrelated/root peers, forged fields and malformed
frames. The transport fixture uses a fake grant runtime; the App suite above
tests the real grant runtime. The container is removed on exit.

These are local Linux/container results. They do not prove DSM `/proc` visibility,
package-var traversal/ACLs, NAS lifecycle, Cloudflare edge WS lifetime, a real
double-NAT topology, external policies or public reconnection. Real Cloudflare
verification and DSM installation remain separate authorized acceptance work.

## Protocol v2: SDK package Unix boundaries

An authenticated consumer can call
`{"method":"capabilities","package":"syngle"}` without a request/ref. A v2
reply advertises `version: 2` and `capabilities: ["package-unix",
"exact-path-set", "reference-by-key"]`. Older servers refuse the method;
consumers must stay disabled, never fall back to a broad TCP or DSM target.

A request may replace `port` with `socket`:

```json
{"method":"request","package":"syngle","request":{"key":"reverse","socket":"/var/packages/syngle/var/ingress-reverse/http.sock","path":"/reverse","additionalPaths":["/reverse/enroll"],"hostname":"reverse.example.com"}}
```

The socket must be the owner's `var/ingress-<label>/http.sock`, never `var/run`,
a foreign package, a browser path or a TCP fallback. Port must be zero/omitted.
The producer uses the released SDK `ipc.StartPackagePeerUnix`, separately from
its private backend socket, and authorizes Syno Connect Center's kernel-authenticated UID.
Syno Connect Center pins the dedicated directory, verifies owner/mode 0711, refuses symlink
socket leaves, verifies socket owner/mode 0666, and authenticates the accepted
server's nonzero UID with SO_PEERCRED before sending any HTTP bytes. Existing
private socket permissions are preserved. Ancestor traversal must already be
available; neither side repairs/chmods package data to gain access.

The custom definition uses `http://package.invalid` solely as its immutable
HTTP transport authority. The approved dialer always opens the pinned Unix
boundary; it never resolves that name or dials TCP. The entire socket and path
set is included in the existing administrator approval digest. Up to three
additional canonical, unique paths are allowed; the Cloudflare rule is an
anchored escaped union, followed by the same host-specific 404. The local proxy
checks the exact same set independently, including query/encoding refusals.

Status/revoke also accept `ref: {key: "reverse"}` instead of an ID. Exactly one
selector is required and package+UID still come from the verified peer. This
recovers an admission whose response was lost. A revoke arriving before any
request persists a separate bounded cancellation tombstone (up to 64 keys);
a later request cannot create or publish that key, including after restart.
Existing accepted grants retain their original revoked-definition tombstone.
Neither form can revive through ordinary administrator reenable.

`TestReverseBoundaryFixture` is an opt-in fixture entrypoint used by Syngle's
`scripts/proof-ingress.sh`. It runs the real Syno Connect Center peer/control, store,
administrator approval and proxy as a separate UID; a test-only outbound SSH
connector/edge stands in for Cloudflare. Two isolated private networks and two
fixed-destination userspace NAT gateways have no inbound endpoint mappings.
This is local simulation, not live Cloudflare, DSM ACL or hardware NAT evidence.
