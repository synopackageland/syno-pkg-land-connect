# Self-hosted NetBird server

The NetBird page has a **Self-host server** tab in every connection state. It
runs NetBird's combined server (management, signal, relay and the embedded
identity provider) on the NAS as the package user, reached at a name on the
user's own domain through the Cloudflare tunnel. Off, the page's other tab is
**Join** (the connect form); connected, it is **Apps**.

## Prerequisites

The tab reads `netbirdserver.status`. `prerequisite` is set, and setup is not
offered, until Cloudflare is connected on an own domain whose saved origin
certificate is readable (`cloudflare_not_connected`,
`cloudflare_own_domain_required`), a Cloudflare Workers token is saved
(`netbird_server_workers_required`) and that token can manage Worker routes
on the domain (`workers_routes_permission`, `workers_zone_account`,
`workers_token_invalid`). The tab names what is missing in a closable note
and keeps the fix on the page below it: a button that opens the Cloudflare
page, or the token field. A failed or waiting step works the same way: the
note says what happened, and the token field and the step's buttons stay on
the page when the note is closed. The server needs glibc
2.34, so DSM 7.2 or later.

Syno Connect Center keeps one Cloudflare API token, shared by the Free link and the
server; the own domain itself is authorized in the browser and needs no
token. The token needs **Account → Workers Scripts → Edit** and **Zone →
Workers Routes → Edit**; the token link on both the Free link and this tab
pre-fills exactly these two (Cloudflare's user-token template URL,
`permissionGroupKeys` with `workers_scripts` and `workers_routes`, all
accounts and zones), and the instructions say to keep both. A token with
only these two was checked against Cloudflare: account discovery, zone lookup
by name, script upload, route creation and removal all passed. The browser
authorization's `cert.pem` cannot replace the token: it reads Worker scripts
but is refused Worker routes and the workers.dev subdomain.

The tab sends a pasted token as `apps.relayconnect` with `noDsm: true`: it
is checked and saved in place of the Free link's token, with the same owner,
but DSM is not published and no workers.dev name is claimed. Turning the Free
link on later reuses it and only asks for the name; publishing DSM stays the
Free link's own action. A different Cloudflare account than the saved one is
refused (`workers_disconnect_required`).

Whether the saved token can carry the bridge is checked without changing
anything: Syno Connect Center finds the zone and reads its Worker routes. The status
keeps a pass for 10 minutes and a refusal for 15 seconds, and a new token is
checked at once. A missing permission is named before setup starts, with a
link to Cloudflare's token list: adding **Zone → Workers Routes → Edit** to
the token Syno Connect Center already has keeps its value, so "Check again" is enough.
Pasting a token with both permissions replaces it instead. The same field
comes back when the bridge step itself is refused, and saving continues
setup.

### Why a Worker

Cloudflare holds a gRPC stream's response headers until the request ends.
Single gRPC calls are unaffected, but Signal's two-way stream, which every
device keeps open to receive connection offers, never starts. A Worker
routed on `<name>/signalexchange.SignalExchange/ConnectStream*` only answers
that stream at once and carries it over a WebSocket to the server's own
`/ws-proxy/signal` on the same name, which the tunnel serves. Everything
else, including the WebSocket relay, goes straight through the tunnel. The
script (`backend/internal/signalbridge/bridge.js`) speaks the small part of
HTTP/2 that one stream needs, with flow control both ways, and closes the
server side when the device goes away. A Worker cannot send trailers, so
single calls must not reach it.

## Setup

**Set up server** (`netbirdserver.setup`) starts one job in connectd and returns
at once; the page polls status (every 2 s while working or starting, otherwise
every 15 s, and only while the tab is open). Leaving or reloading the page does
not stop the job. The steps, each `pending`, `running`, `done` or `failed` with
a code:

1. `prerequisites`: Cloudflare as above.
2. `binary`: the `netbird-server` product is downloaded and verified like the
   other binaries (see [binary-management.md](binary-management.md)).
3. `config`: `var/netbird-server/` holds `config.yaml`, a fresh store key,
   auth secret and owner password in `secrets.json` (0600), and a self-signed
   ECDSA certificate for the hostname only. The server listens on 33073 on all
   interfaces (the tunnel reaches it there); the health check uses a free
   loopback port, and the metrics port, a free port, is bound by NetBird on all
   interfaces. STUN is external
   (`stun.cloudflare.com:3478`) because Cloudflare does not carry UDP.
   Geolocation downloads and anonymous metrics are off.
4. `route`: a free name, `netbird.<zone>` first, is added as its own tunnel
   ingress rule with an HTTP/2 origin pinned to that certificate, and the
   tunnel's edge protocol becomes `http2` while the rule exists. The name is
   recorded in `domain.json` `routed`, so removal, restore and the uninstall
   reminder cover it. An App or DSM can never take the same name.
5. `bridge`: the bridge Worker `connect-nbsignal-<hash of the name>` is
   uploaded with this installation's owner tag and a tag naming the bundled
   script's version, workers.dev off, and the route above is added on the
   zone. A script or route that is already there and current is left alone.
   The Workers state records them first, so removal always finds them.
   Missing permission is `workers_routes_permission`, a zone in another
   account `workers_zone_account`, a route held by another Worker
   `workers_route_conflict`; nothing of another owner is changed.
6. `start`: the server starts under the shared tolerant supervisor. The step
   waits up to 10 minutes until the server answers `/api/instance` over
   loopback TLS and fails only after it exits twice (NetBird's `/health`
   counts only standalone relay listeners, so the combined server always
   reports it unhealthy); a running server that exits is restarted, and the status reads
   `starting` meanwhile.
7. `account`: the owner (`admin@<hostname>`) is created once through
   `/api/setup` with a personal access token valid for 365 days, kept in
   `secrets.json` with its id and expiry. While the server runs, Syno Connect Center
   creates a new token 30 days before expiry, saves it, then deletes the old
   one (retried hourly on failure). Renewal runs only while the server runs:
   a token that expired while the server was off fails with
   `netbird_server_token_expired`; turn the server off and set it up again.
8. `setupkey`: a one-off setup key valid for one hour, skipped when this NAS
   already uses this server.
9. `check`: a gRPC request through the public name must return an HTTP/2
   gRPC answer, and Signal's two-way stream, opened with its request kept
   open as a client does, must return the registration header within 15 s;
   retried for up to 3 minutes. A held stream is
   `netbird_server_signal_blocked`: the bridge did not open it, so devices
   could never connect, though single calls pass. A page, 403 or HTTP/1
   answer is `netbird_server_grpc_blocked`: Cloudflare answers gRPC with 403
   when the zone's **Network > gRPC** setting is off (Cloudflare Access in
   front of the name looks the same); 520-530 or no answer is
   `netbird_server_unreachable`. The gRPC setting has no API, so on
   `netbird_server_grpc_blocked` the step stays running with that code, the
   page shows one closable instruction and, below it, a button to the zone's Network settings
   (`https://dash.cloudflare.com/?to=/:account/<zone>/network`), and the
   check repeats every 5 s for up to 20 minutes, continuing by itself once
   the setting is on.
10. `join`: the NAS's NetBird connection switches to this management URL with
   the key, replacing any previous NetBird connection, and waits up to
   10 minutes for Connected.

A failed step keeps the earlier ones; **Continue setup** resumes from it. A
taken name is chosen again. Every failure names its next action.

## Running

Status shows the management URL, server version, devices online of total,
uptime, resident memory and data size (1024-based units), and the owner's email.
`netbirdserver.credentials` returns the owner password only when asked; the page
forgets it when the tab closes. Other devices install NetBird, enter the
management URL and sign in as the owner. Opening that URL in a browser shows
a small device-connection guide. NetBird's web dashboard is not included; see
[Optional web dashboard](#optional-web-dashboard) for administrator-managed deployment.

While the server runs, Syno Connect Center deploys the bridge again when it starts and
then daily: a Syno Connect Center update with a new script replaces the Worker (its
version tag differs), and a route removed in Cloudflare comes back. A failure
is logged and retried after 10 minutes. Turning the Free link off deletes only
its relay Workers, retaining the Signal bridge and shared token while the
server is on. A running server whose token is gone (removed by an older
version, or revoked in Cloudflare) shows **Devices cannot connect**
with the token field; the first status read after a token is saved deploys
the bridge again at once.

The `netbird-server` binary appears with the others under Settings > Binaries
and is updated there. A binary update restarts the server and waits for health; a failed start
restores the previous binary.

## Dependencies

The server uses the own-domain tunnel and its own Signal Worker, independently
of the Free link. When the dependency has a problem, both pages show the same
closable warning from the dependency report; a healthy dependency shows none.
Cloudflare disconnect, authorization removal and domain changes are refused
while this server is enabled: turn off the NetBird server first, then Cloudflare.
See [Connection dependencies](connection-dependencies.md) for every pair, failure
impact, recovery action and topology hop.

## Turn off

`netbirdserver.disable` first disconnects this NAS's NetBird when it uses
this server, then stops the job and the server, removes the Cloudflare
ingress rule and DNS name it created, then the bridge's route and Worker (each
tried even when the other fails), and
with `removeData` deletes
`var/netbird-server/`. Kept data lets **Set up server** reuse the same owner.
A name or bridge that Cloudflare could not remove stays in `cleanupHostname`,
shown with **Remove name**, and the data is kept until it is gone. When the NAS's NetBird cannot be disconnected, the server is
still turned off and the call fails with `netbird_server_leave_failed`.

Uninstalling Syno Connect Center saves the server as off without calling Cloudflare; a
reinstall that kept data starts with the server off, and its name and bridge
stay recorded so **Set up server** or turning off can still remove them.

With the feature off nothing runs, no port is open and the tunnel
configuration is unchanged. DSM's firewall, when enabled, decides whether LAN
devices can reach ports 33073 and the metrics port directly.

## License

`netbird-server` is AGPL-3.0. Syno Connect Center downloads the official build unmodified
from NetBird's container registry; it is not shipped in the SPK.

## Validation boundary

Go tests cover configuration, certificates, the step state machine, retry,
supervision, update and revert, turn off, the control socket, Cloudflare
ingress rendering and name handling with fakes, the gRPC check against
local servers, the bridge's deployment, version update, route, conflicts and
removal against a fake Cloudflare API, and the wait for the gRPC setting.
`tests/signal-bridge.test.mjs` runs the bridge script against a fake server
WebSocket: registration, PING, flow control both ways, and closing when the
device leaves. Vue tests cover the tab and the gRPC guide.

On a DSM 7.4 x86_64 NAS with 0.81.5, every step through `setupkey` ran for
real, as did token renewal and turn off. Through a named tunnel on an own
zone with gRPC off, Cloudflare answered gRPC with 403. With gRPC on, single
gRPC calls, sign-in with a setup key and the WebSocket relay passed, but
Signal's two-way stream got no response headers while its request stayed
open (the origin sends them within a second), from inside and outside the
office network alike, so the NAS could not connect. Cloudflare documents
gRPC through a tunnel public hostname as unsupported.

A prototype of the bridge (the same design, before flow control) on an own
zone with gRPC on carried Signal for a NAS and an outside Linux peer: the
check passed, both peers connected over the WebSocket relay and a page was
fetched across NetBird. Over a three-hour run with a NAS and an outside
Linux peer, checked every minute, every fetch across NetBird in both
directions succeeded and both peers stayed connected. Cloudflare resets each
Signal stream at 600 s, and the client reconnects in about 1.5 s; Workers
document no duration limit for a request while the client stays connected,
and none of Cloudflare's documented connection limits is 600 s. The Worker
used 6 to 12 ms of CPU per 600 s stream. The product script, with flow
control, then carried Signal for 25 minutes the same way, including a peer
joining. Through the tunnel itself, the Management stream is reset every few
minutes and reconnects at once, and NetBird's job stream gets a 524, so jobs
sent to a device from the dashboard may not arrive. The product deployment
through the API has run only against fakes.

## Optional web dashboard

The management URL is an endpoint for NetBird apps, not a web dashboard.
Its `/` page explains how to install NetBird, enter this URL as the management
server and sign in with the administrator shown by Syno Connect Center. The page reuses
the existing Signal Worker, with one exact `/` route; it adds no NAS process
or container. Turning the server off removes both Worker routes and its script.
The API, OIDC, gRPC and WebSocket paths continue to reach the original server.

Administrators who need peer or policy management in a browser can deploy the
upstream [`netbirdio/dashboard`](https://github.com/netbirdio/dashboard)
container themselves, for example in Container Manager. Syno Connect Center does not install,
start, stop, upgrade or remove that container. Choose an upstream stable dashboard
tag compatible with server 0.82.0 and pin its image digest; the dashboard and
server have independent version numbers. Follow the upstream
[combined-server deployment and dashboard environment](https://github.com/netbirdio/netbird/blob/v0.82.0/infrastructure_files/getting-started.sh).
For management URL `https://netbird.example.com`, its embedded-IdP settings are:

```dotenv
NETBIRD_MGMT_API_ENDPOINT=https://netbird.example.com
NETBIRD_MGMT_GRPC_API_ENDPOINT=https://netbird.example.com
AUTH_AUTHORITY=https://netbird.example.com/oauth2
AUTH_CLIENT_ID=netbird-dashboard
AUTH_AUDIENCE=netbird-dashboard
AUTH_CLIENT_SECRET=
USE_AUTH0=false
AUTH_SUPPORTED_SCOPES=openid profile email groups
AUTH_REDIRECT_URI=/nb-auth
AUTH_SILENT_REDIRECT_URI=/nb-silent-auth
LETSENCRYPT_DOMAIN=none
```

Serve the dashboard over HTTPS with your own reverse proxy and use the
administrator account/password shown in Syno Connect Center; DSM credentials do not sign
in to NetBird. Publishing a dashboard port alone is insufficient: its browser
origin, OIDC callback URLs and the server's registered redirect URIs must agree.
Syno Connect Center currently registers `https://<management-host>/nb-auth` and
`https://<management-host>/nb-silent-auth`. It does not expose an additional
dashboard-origin setting, and rewrites its generated `config.yaml` on startup.
A separate dashboard hostname therefore needs additional server/proxy integration;
simply pasting these environment variables into a container will not enable
cross-origin sign-in. Do not overwrite Syno Connect Center's managed Signal or landing
routes, or edit its generated configuration as a persistent setup. Use the
upstream self-hosting instructions for a fully operator-managed dashboard/server
routing arrangement. This is deployment guidance, not a tested or managed
Syno Connect Center dashboard feature.
