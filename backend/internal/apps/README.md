# App access core

`Store{Dir: DataDir}` owns `apps-state.json` (0600) and a process-shared writer
lock. `Load` returns an empty approval set **and** `ErrCorrupt` on invalid input.
Never discard that error and save the empty set: revision history must survive.
`State.Revision()` allocates the next global revision, including on revocation.
`Update` is the durable boundary for daemon checks that change approvals.

`Lifecycle` implements opening, pausing/revoking and startup reconciliation.
The daemon supplies `Runtime`, holding its provider/proxy ownership lock. All
runtime callbacks execute under the state writer lock; they must not call
`Store.Update` recursively. Readers use `Load` without waiting for network work.

- `Start` enforces state before any listener accepts requests
- `Validate` checks the current binding, authority, pin and readiness/ping
- `PrepareDNS` runs only on an explicit new public approval
- `Activate` starts the socket and installs the provider route
- `Block` refuses requests and closes existing streams immediately
- `Remove` removes a scope's provider route idempotently
- `Prune` removes every route outside the validated active set, including orphan
  routes left by a crash after a scope was deleted

Provider callbacks must use `PinSocket(Target.Scope.Socket)` under the package's
private `var/run` directory and must not infer approval from an existing route.
`Recover` must run at daemon startup; retry reconciliation at the existing
30-second check for failed cleanup. No runtime dependency means unavailable,
never successful exposure. `Open(..., true)` reuses DNS on reenable.

`WriteProjection` takes the selected `var/spl/external.json` or
`target/spl/external.json` path explicitly. Only public scopes (active or paused)
and the public DSM origin are exported, with mode 0644. Missing projections are
rebuilt on startup. Private authorities, pin material and cleanup details are
never projected. The gateway treats invalid or missing projections as absent.

Every `WriteProjection` call also rewrites the menu-only launch map
`spl/launch.json` (0644) in the same checked directory. It maps each DSM
endpoint to the Apps approved on that provider (`cloudflare` or `workers` for
the public DSM endpoint, `tailscale` for the private one), with each App's
origin, port and `active`/`paused` state. The SDK's DSM menu launcher reads it
to open an App on the provider the browser used to reach DSM. Unlike the
projection it includes private authorities: Tailscale HTTPS names are already
public in Certificate Transparency logs, and the launcher never uses them for
sign-in. Pins, sockets, reasons and cleanup details are never written.
The state has one public DSM endpoint, so DSM is public on a Cloudflare domain
or on workers.dev, never both at once.

Run `go test -race ./internal/apps` from `backend`. The subprocess tests send
SIGKILL between lifecycle and file-write steps; they verify forwarding after
restart iff a binding is active and valid. These tests do not simulate power
loss or establish DSM ACL, provider or device lifecycle acceptance.


## Pin proxy

`NewPinProxy(PinConfig)` creates one immutable binding for one provider scope.
Use `PinBindings(state)` and `StorePinState` to adapt the durable state. Pass
INFO and gateway metadata paths from discovery, the confirmed protection summary,
and a verifier whose `ReferencePort` calls `ports.DatastoreDetector.Detect`.
The shipped verifier uses only the bundled Mozilla roots, not DSM's root store.
The fallback roots module requires Go 1.26, within the pinned build toolchain.

Start with `StartPinProxy(ctx, cfg)` under the daemon ownership lock. The SDK
owns its server handle and tracks accepted connections, including hijacks.
Providers receive `PinSocket(binding.Socket)`, a bounded direct child of the
package's private `var/run`. Persisted approval IDs remain unchanged.
Registry keys are the socket IDs. `Set` replaces and stops an old proxy;
`Stop` is the immediate post-persistence revoke/block step, before provider cleanup.
The daemon attaches `PinRegistry.Check` to `Checker.StartSafetyChecks`, independent
of UI requests. The runtime supplies `Observe` with fresh discovery, duplicate-port
scan results, DSM HTTPS port and MagicDNS. Missing evidence refuses temporarily.
The App/provider runtime still supplies activation, DNS and route ownership; an
empty registry grants nothing.

`Suspend` persists a refusal; the proxy closes all connections next, including
handshakes, streams and WebSockets; `AfterBlock` writes the projection and invokes
exact-scope provider cleanup. Failed persistence and cleanup retry during safety
checks without reopening traffic. Runtime callbacks must not recursively acquire
the state writer lock. The existing DSM schema has no paused endpoint flag:
a paused DSM scope is removed, requiring explicit approval again. Removed Apps
retain a paused scope with reason `removed`; they never automatically resume.

`Propose` and `PrepareReenable` require a ready App ping before offering a dialog.
The approval layer must retain the complete server-side proposal and require an
administrator click; never reconstruct `Proposal` or `Reenable` from browser input.
W names and S fingerprints are TOFU at approval; W never downgrades in background.
Ping is a routing diagnostic and cannot prove ownership of other URLs on a port.

Focused checks: `TMPDIR=/tmp go test -race ./internal/apps ./internal/connector
./cmd/connectd`. The 50-worker test records the local direct/proxy ratio. Set
`PINPROXY_MIN_THROUGHPUT=0.84` only for the controlled performance acceptance run;
normal fast checks assert successful transfers and connection-pool capacity.
These local fixtures do not establish the NAS throughput floor, cross-UID socket
isolation, real provider behavior or actual DSM CGI/login compatibility.
## Page API

The installed SDK registry requires lowercase method names. The App URLs page
uses `apps.list`, `apps.inspect`, `apps.setprivate`, `apps.setpublic`,
`apps.reenable`, `apps.clear`, `apps.cloudflareconnect` and
`apps.cloudflaredisconnect`. All routes require a current DSM administrator.
The CGI forwards these actions over connectd's existing private control socket.

Custom Apps add `apps.customadd`, `apps.customedit`, `apps.customremove`,
`apps.custompause` and `apps.customcandidates`. A definition lives in
`State.Custom` beside the approvals and is listed as an App with a `custom`
field (`url`, `appSignIn`, `insecureTls`, `reachable`). `ParseTarget` accepts an
http or https origin with an optional canonical start path, without user info, query or fragment and refuses
link-local, metadata, unspecified, multicast and broadcast literals.
`TargetGuard` repeats that check on every resolved address and also refuses
Connect's own listening ports. `NewCustomProxy` forwards only to that origin,
never follows redirects, rewrites only same-origin `Location` headers and, with
`Gate`, requires a DSM sign-in first. Reachability is a 1.5-second probe shown
as a hint; it never pauses or refuses a custom App.

The pick list always offers DSM on its detected HTTPS port. The administrator's
DSM window also reads `SYNO.Core.AppPortal list` v2 with `default_setting` and
passes only its suggestion metadata to `apps.customcandidates`. Saved HTTPS / HTTP
ports win; otherwise the saved alias or default application path uses DSM HTTPS.
Factory alternate ports are never mistaken for enabled listeners. The desktop's
`SYNO.Core.Desktop.Initdata get` (`action: jsconfig`) supplies matching native icons,
including `appPortalId` mappings. These are read-only, session-scoped suggestions,
not authority; saving and sharing retain the existing backend checks and approval.
A start path redirects the initial root GET/HEAD after Connect sign-in; it does
not confine access to a subtree. Assets and DSM sign-in APIs keep their own paths.
Candidates deduplicate by normalized URL, with named entries before listeners and
a 64-entry cap. Discovery errors stay visible with Retry and manual URL entry.

The pick list reads DSM's own port definitions on every request
(the SDK `dsmservices` package: `/usr/syno/etc/services.d`, its factory defaults,
`/usr/local/etc/services.d` for packages and Container Manager; world-readable,
no privilege). Single TCP ports of web-looking definitions come first, then
the other definitions, then unnamed listeners, so the cap of 64 drops only
those. Ports that never serve a page (SSH, mail, DNS, SMB, NFS and the like)
are left out, as definitions and as listeners. The page renames entries with DSM's translated
names from `SYNO.Core.Service.PortInfo` (administrator session, best effort),
matched by section (`port_id`) or, for an unnamed listener, by port.

`Service` supplies the page's `AppsView`, `Inspection` and per-App public results.
`inspect` accepts `{package, scope}` and creates no forwarding state. It returns
the current port, protection and W/S certificate disclosure plus a `token`.
The confirmation passes `{port, pin, token, dsm?}` back as `approval` (or the
per-package `approvals` map for public batches). Before activation the service
rechecks these values, the protection digest and global revision. Any change
requires a new inspection. Browser-provided upstream ports, pins, origins and
socket IDs never become state directly.

The daemon owner implements `Approver` beside `Runtime`. Its `Inspect` returns
server-derived scope/authority/pin and certificate disclosure, including any
first-private-App DSM approval; `CheckPublic` rejects all hostname collisions
before the batch creates DNS. `ProviderAccess` supplies the private/public
availability and explicit Cloudflare connect/disconnect actions. Missing owners
leave these controls unavailable and refuse mutations. Lists overlay active,
paused, unreachable, removed and cleanup state without granting anything.

The production composition point in `cmd/connectd/main.go` installs
`appaccess.Engine` as Runtime, Approver and ProviderAccess. It binds the existing
PinRegistry, Tailscale owner and Cloudflare batch/auth owner. See
[composition and integration tests](../appaccess/README.md). Startup calls
`Lifecycle.Recover` before serving mutations, and the 30-second checker performs
refusal checks and cleanup retries. No provider observation populates approvals.

Discovery receives `DetectPorts`, a startup-only callback that wraps
`ports.DatastoreDetector.Detect` in connectd. This keeps the core independent of
provider packages while using fresh DSM HTTP/HTTPS ports on every scan. A missing
or failed detector refuses discovery; no default ports are assumed.


For the bulk acceptance workload (50 workers, 64 MiB fixed-length responses),
run `PINPROXY_BULK_PERFORMANCE=1 go test ./internal/apps -run
TestBulkFiftyConcurrentThroughput -count=1 -v` on a controlled Linux runner.
The test requires a proxy/direct throughput ratio of at least 0.84. The small
128 KiB workload remains a separate reported measurement; passing the bulk
workload does not assert the same ratio for arbitrary chunked responses.


Runtime/approval wiring can use these public interfaces directly:

- `PinBindingFor(Target)` maps an App scope; `PinBindings(State)` includes DSM scopes
- `Verifier.VerifyTarget(ctx, binding)` validates pin and ping on one connection,
  without persistence or listeners, and is safe under the lifecycle writer lock
- `Verifier.Propose` and `PrepareReenable` return the certificate disclosure;
  `Confirm` and `ConfirmReenable` preserve W/S policy for the approval owner
- `StartPinProxy`, `PinSocket`, `Wait`, `PinRegistry.Set/Stop/Check/Close`
  provide the runtime-owned socket lifecycle and background refusal checks
- `StorePinState` supplies durable refusal, projection and exact-scope cleanup;
  `Runtime()` reports transient waiting without writing it to state

The daemon composition and provider routing remain owned by the runtime wiring.


Fixed-length non-SSE bodies use a pooled 128 KiB copy buffer and coalesce short
TLS reads before writing to the Unix socket. SSE, unknown-length responses and
WebSocket upgrades retain immediate streaming. The SDK owns accepted Unix
sockets, which use the OS default buffers. Admission, pin verification,
metadata checks and connection cancellation apply while a body is buffered.

The bulk diagnostic keeps the original direct-TLS comparison and 0.84 assertion.
`PINPROXY_BULK_CLIENT=provider` uses a 32 KiB body copy, matching the standard HTTP
reverse proxy, rather than `io.Discard`'s 8 KiB drain. Add
`PINPROXY_BULK_COMPARE=relay` to measure an unpinned test-only Unix-to-TLS relay
with the same copy/socket settings. Repeated runs alternate relay/pin order and
report both ratios. `PINPROXY_BULK_TRACE_READS=1` adds network-read counters
to all compared clients; leave tracing off for timing acceptance so shared atomic
counters cannot bias the comparison. The relay control does not
replace or disable the direct comparison's assertion. Use it to isolate relay
cost from security-check overhead; final throughput acceptance requires the
actual NAS gateway and provider workload.
