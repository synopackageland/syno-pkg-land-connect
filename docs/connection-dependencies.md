# Connection dependencies

The `internal/dependency` model owns the dependency graph, prerequisite order,
upstream-removal guard and failure attribution. Both pages consume the same
`netbirdserver.status.dependencies` report. Free link activation has its own
persisted state; it is never inferred from a shared token being present.

| Dependent | Upstream | What stops when it is unavailable |
| --- | --- | --- |
| Self-hosted NetBird server | Cloudflare Tunnel connection | Management, login, relay and the Signal Worker's WebSocket upstream cannot reach the NAS |
| Self-hosted NetBird server | Own domain | Devices cannot resolve/reach the server's management and Signal hostname |
| Self-hosted NetBird server | Domain authorization certificate | Name/route management and recovery cannot proceed; the existing tunnel can still serve |
| Self-hosted NetBird server | Signal bridge Worker | Devices cannot register the two-way Signal stream, even if management gRPC works |
| Signal bridge Worker | Cloudflare Tunnel connection | The Worker's WebSocket upstream cannot reach the NAS Signal service |
| Signal bridge Worker | Own domain | The bridge routes and its WebSocket upstream cannot reach the intended server |
| Signal bridge Worker | Shared Cloudflare Workers token | Deployment, permission checks and repair cannot proceed; an existing Worker may still serve |
| Free link relay Workers | Shared Cloudflare Workers token | Worker creation, changes and cleanup cannot proceed; existing relays may still serve |

Free link relay Workers have no dependency on the NetBird server, its Signal
bridge, or the own-domain tunnel. Turning off the Free link retains the bridge
and any shared credential still used by NetBird or email restrictions.

## Change order

- A configured/enabled NetBird server blocks explicit Cloudflare disconnect
  before either disabled gate is written. Removing domain authorization or
  replacing the own domain/connection mode is refused too. The confirmation
  dialog names the impact and offers **Open NetBird server**: turn off the
  NetBird server first, then Cloudflare. Direct API and ctl calls obey the
  same backend policy; a server enabled after the dialog opened is refused inline.
- Turning off NetBird does not disable the shared Cloudflare connection, revoke
  its account token, stop DSM/App publication or alter Free link activation.
  It removes only the server's own hostname/ingress and Signal bridge through
  their existing owned-resource cleanup. Failed cleanup remains named for retry.
- Starting the NetBird server checks the tunnel, own domain, authorization and
  Signal-token permissions before scheduling or persisting a setup job. It
  returns the first missing prerequisite. Setup creates its bridge, so a bridge
  is not required to exist before the first setup.
- Reconnecting the same saved own domain uses Stop/Start and retains its domain
  authorization certificate. Explicit disable still revokes it. Package stop
  and upgrade pause processes without changing the saved enabled intent.

## Broken state

The model checks the connected own-domain tunnel first, then its domain
certificate, the Signal token, the owned bridge script plus both expected
routes, and finally management gRPC and the open Signal stream. A failed
upstream prevents downstream probes from replacing the root cause. Network
observations are bounded and shared for 15 seconds; local connection state
is read fresh each time. Worker observation is read-only and does not repair
or create a route.

| Prerequisite | Piece and recovery action |
| --- | --- |
| `cloudflare_not_connected` | Tunnel disconnected: connect Cloudflare first |
| `cloudflare_own_domain_required` | Own domain missing: connect an own domain |
| `cloudflare_reconnect_required` | Domain authorization missing: authorize Cloudflare |
| `workers_not_connected` | Signal token missing: add the Signal token |
| `workers_token_invalid`, `workers_routes_permission`, `workers_permission_denied`, `workers_zone_account` | Signal token rejected or incorrectly scoped: replace it |
| `workers_unavailable`, `workers_api_refused`, `workers_rate_limited`, `workers_state_corrupt` | Signal credential check or storage unavailable: check the named token problem again |
| `netbird_server_bridge_missing` | Signal Worker or an expected route missing: repair the bridge |
| `workers_route_conflict`, `workers_name_conflict` | A foreign Worker/route owns the name: review it in Cloudflare; repair never overwrites foreign resources |
| `netbird_server_signal_blocked` | Signal stream unavailable: repair the bridge and run its stream check |
| `netbird_server_grpc_blocked` | Management gRPC is not carried: open Cloudflare's gRPC settings |
| `netbird_server_unreachable`, `cloudflare_tunnel_missing` | Management route/tunnel unavailable: check the Cloudflare route or reauthorize the saved tunnel |

While the dependency is healthy, neither page shows a notice; the turn-off,
disconnect and authorization-removal dialogs explain it when the user acts.
When there is a problem, both pages name the affected piece and offer one
recovery action. The user can close that warning; it returns when the problem
code changes, or when the problem clears and comes back. A credential
problem is a maintenance warning, not proof that every existing device has
disconnected. Topology draws a lost tunnel at the NAS-to-Cloudflare hop for
both management and Signal; a missing Worker affects only the Signal lane.
The Cloudflare page's own way draws that hop from the same report through
`dependencyPathLinks`, and only while the way is up and the server is using
Cloudflare: a way the user turned off is drawn turned off ("Turned off"),
never as a failure, and a report nothing depends on marks nothing.
Errors remain in the dialog or inline, including unknown error codes.

An HTTP 200 from the bridge is insufficient evidence of Signal health: its
response can carry gRPC status 14. Acceptance requires management gRPC OK and
an open Signal stream's registration header, plus the NAS client's Connected
state. A saved `joined:true` is historical intent, not this acceptance.
