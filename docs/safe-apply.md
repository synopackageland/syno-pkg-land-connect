# Safe connection changes

Connection updates use the same owner for the UI, ctl and trusted port additions.
The existing connection is not disabled to stage a change to that provider.
`connection.status` and `ctl.connection.status` return an optional `apply` object:
`phase`, a bounded public `code`, `addedPorts` and `removedPorts`. An accepted
request is not a committed setting.

The shared owner performs read-only prerequisites, captures accepted owner and
runtime settings, writes a private journal, stages the provider's native update,
checks native readback and the existing services, then commits. A failed stage
restores the snapshot and checks health before reporting `rolled-back`. A failed
compensation reports `recovery-required`; it does not report a successful restore.
The generic journal never contains submitted credentials or identity files.

A change the running connection cannot keep is refused with `apply_unavailable`
and nothing changes. The refusal is the result of that request only: it never
becomes the connection's `apply` status, so the status word stays as the check
reports it. The page shows it as one row beside the provider's status with
**Apply anyway** and **Dismiss**; leaving the page or a later passing check also
clears it. Apply anyway confirms in one line (the address drops for about a
minute while it switches; when the page itself comes through that address it
says so) and sends the same request with `replace: true`, which reconnects the
provider instead of updating it in place. Replacing Cloudflare's temporary
address needs no confirmation unless the page comes through it: that address
changes on every restart anyway.

A new connection, a reconnect and a connection restored at boot settle first:
for 3 minutes on DNS-based ways (Cloudflare, the Free link) and 1 minute on the
others, a failed connection check reads `starting` and is retried with backoff
(5, 10, 20, then every 30 s), and health checks do not count failures. The
first passing check reads `ok`; only a failure after the window reads `failed`
with its real code. Early failures never repair or stop a connection.

Failed background repairs, including NetBird login URL timeouts, durably end as
`recovery-required` with their original error code. Their snapshots remain for
restart recovery. A successful replacement connection retires the previous journal.
NetBird's **Check and repair** action verifies an already connected session and
NAS services without stopping it; a failed check keeps the warning and snapshot.
If sign-in is required, retry starts the normal sign-in flow instead. Interrupted
work remains recoverable after restart, and newer explicit settings retire stale
journals rather than replaying them.

On restart, an interrupted journal restores accepted runtime settings before
starting the provider. A newer disabled gate or explicit port withdrawal wins;
stale transactions cannot restore withdrawn mappings. Disconnect and package stop
cancel pending work before stopping the provider. Binary activation refuses while
a change is pending. Repeated updates do not start another staging worker.

## Provider behavior

- **Cloudflare own domain:** a change that keeps the same own domain keeps the
  running connector: Syno Connect Center checks the saved domain, the cloudflared binary and
  DSM's local port, and changes nothing at Cloudflare. Changing
  domain/mode/identity is refused in place while the connection is live and
  goes ahead with `replace`. An apply journal
  left by a version that published TCP services is dropped on restore.
  There is no assumed local config reload protocol or custom cloudflared build.
- **Tailscale:** like NetBird, a change that keeps the auth key and control
  server keeps the logged-in userspace daemon and its App Serve children; a new
  key or server restarts the session. Health requires a running daemon and
  App routes that read back as served.
- **NetBird:** existing identity/management settings can be verified without
  restart; replacement of an active identity refuses. NetBird TCP access remains
  controlled by its management policy. Syno Connect Center creates no local mapping API and
  does not modify management ACLs. Verification uses the existing userspace proxy.
- **IPv6:** Syno Connect Center changes only its accepted inventory, never DSM listeners,
  firewall, router or DNS. It checks fresh address candidates and local listeners.
  An external route remains unverified; local availability is not proof of access
  from another network. Lost prefix/listener/router prerequisites require action
  rather than changing host or router configuration automatically.

Trusted detector withdrawal is a revocation, not a reversible user update. A
removed/changed upstream must not be restored from an old transaction. The port
tracker keeps a still-valid accepted DSM web route during its initial empty
subset; an explicit revoke/disable still removes the owned routes.

## Health and sleep

The daemon samples health every 30 seconds, without a browser. Healthy observations
use cached settings, native sockets and bounded network probes; they do not open
or write a persistent state file. Runtime files remain on the existing private
`/tmp` tmpfs, with no persistent fallback. Only an actual apply, interrupted-apply
recovery or attempted repair writes its journal.

Three failed observations trigger a bounded repair decision. Repairs use the same
journal/readback/health owner, retain the accepted scope and back off, with at most
three attempts before manual recovery is required. A disabled/revoked connection
is never repaired. Expired login asks for reauthorization. Native Cloudflare
repair resumes the retained tunnel/credentials; it never creates a replacement
cloud tunnel or overwrites DNS without a fresh explicit authorization. Quick
Tunnel URLs cannot be recreated unchanged and are not automatically replaced.

Tailscale/NetBird probes from the NAS prove only the NAS-side path, not another
peer's ACL or DNS. Cloudflare's public probe checks DSM discovery and HTML, not a
successful user login or a particular NAS identity. Rollback cannot undo an ISP
failure, revoked credentials, a third-party policy change or power loss.

## Validation boundary

Regression tests exercise production MultiSelection → Selection → Cloudflare for
pending and refused authorization, shared precheck/readback/rollback/cancel and
stale-journal handling, candidate/DNS compensation including a lost reply, Tailscale
App route retention, and health observations that cannot write a failed gate.
All fault injection uses local synthetic fixtures. These are not NAS acceptance,
Cloudflare tenant lifecycle certification or proof of uninterrupted TCP streams.
