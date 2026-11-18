# Supervised process recovery

Syno Connect Center uses the SDK `supervision` policy for its worker and every provider
process. The only policy defaults are in the SDK: 1–2 seconds before the first
retry, full jitter over a doubling ceiling capped at five minutes, eight
failures in ten minutes, and reset only after a continuous run of at least a
minute and twice the previous delay ceiling. A successful readiness probe does
not clear that history.

The worker adapter owns its existing lock and parent-death pipe. Provider
Restorer owns Tailscale, NetBird and the self-hosted NetBird server for their
whole lifetime; cloudflared's watch owns its existing tunnel job. Health checks
observe and report these children but never create a new retry series.
There is no automatic retry timer after a terminal `failed` state.

## Readiness and triggers

Network clients wait for a default route and DNS resolution of their actual
management endpoint before spending failure attempts. Cloudflare also waits
for DSM's local HTTP listener. Each probe is bounded; the total dependency
wait has no deadline. A daemon that is alive but slow to answer keeps its
process and waits without spending failures.

A package start, configuration change, enable or explicit **Retry** begins one
new bounded series. Retry uses the enabled saved connection and its existing
identity; it accepts no token or new settings. It cannot enable a disabled
provider. Kernel route/link/address and resolver events re-arm only transient
failed enabled connections. Normal health polling never re-arms them.
A protected optional helper keeps its existing administrator authorization:
its exit is terminal, with no root automatic restart or idle polling timer.

Disabled providers start no process, restore loop, network watcher or ticker.
The demand owner stops health, memory-pressure and port polling when every
enabled child is terminal. Only its kernel event watcher remains, without a
timer, to receive a real network trigger. Disabling every connection closes
that watcher too. A terminal provider is never restarted by health polling.
The self-hosted server's token/bridge maintenance ends on terminal failure.

## Permanent failures

| Child | Permanent evidence |
| --- | --- |
| connect-worker | Missing/unexecutable binary, permission/exec-format or process cleanup error |
| cloudflared | Missing binary, invalid token/config, missing/revoked tunnel or authorization; the known tunnel-missing output becomes a fixed code |
| tailscaled and its CLI | Missing binary; invalid settings/management address; login denied/expired or rejected login URL; HTTPS prerequisites requiring administrator action |
| NetBird and its CLI | Missing binary; invalid management/configuration; login denied/expired or rejected origin; credential refusal reported by the child |
| netbird-server | Missing binary; invalid configuration or absent protected saved configuration; process cleanup error |
| protected helper | Any exit; restart requires the existing explicit authenticated lifecycle |

Generic exit code 1 is not classified as fatal: upstream children reuse it for
several causes and publish no stable universal fatal-code contract. Every unrequested exit, including exit zero and signals, spends the same
bounded budget for resident children. Requested stop/disable cancels recovery. Raw account/tunnel
output is never stored in the status API. Repair credentials/configuration
before retrying a permanent refusal.

## Cleanup and diagnostics

The SDK owns each Linux process group. On every exit it kills remaining group
members before reaping the leader, then closes command descriptors. A cleanup
failure blocks a successor. Private provider sockets and ephemeral state are
released by their existing session teardown; persistent identity is retained.
Two kernel socket slots limit concurrent starts across package accounts on the
same host, held through the first second or an earlier exit. There is no shared
root service, writable host directory or second copy of a child.

Existing status APIs include `supervision`: `state`, `attempts`, `nextRetry`,
`lastExit` (exit code, fixed reason, permanent flag), and a relative log pointer.
The connection cards show waiting/backoff/failed, retry time and last exit, with
a Retry action on failure. Ordinary connected/sign-in states retain their
existing meaning. The stopped worker marker also carries its terminal snapshot.
Each policy state transition is logged once. Child output stays private and
bounded; status never returns command arguments, tokens or raw logs.

## Release acceptance

Local policy, process fixtures and UI checks do not establish DSM reboot
acceptance. After upgrading, the deployment owner must verify:

1. All enabled Cloudflare, Tailscale, NetBird and self-hosted server connections
   restore their saved identity after a real reboot; disabled ones create no
   process or provider ticker
2. Delayed route, DNS and DSM readiness display waiting without spending
   failures; service startup moves through starting and running
3. Fast exits show jittered backoff with attempts, next retry and last exit;
   repeated failures stop at failed with no retry timer or duplicate process
4. Retry starts exactly one new bounded series with the same settings and
   identity; a real network change re-arms transient failed connections
5. Invalid/revoked credentials and missing binaries stop immediately; correcting
   the cause and explicitly retrying recovers without a sign-in loop
6. Stop/disable/uninstall during waiting, backoff and startup cancel recovery;
   process groups, private sockets and ports are released before a successor
