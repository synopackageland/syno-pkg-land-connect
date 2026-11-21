# Operate Syno Connect Center with AI / CLI

Use the installed Syno Package Land CLI (`ctl` is available since 0.15.0).
The alias is `connect`; the package ID `SynoPkgConnect` also works. Every
command requires a DSM administrator, just like the UI. Ordinary users cannot
read the command description or execute a method.

```sh
# Enter the password privately; automation can use --password-stdin.
synopkgland host add nas --url https://nas.example:5001 --username admin
synopkgland ctl list --host nas --app SynoPkgConnect --json
# Approve description access when prompted; always select the intended host.
synopkgland ctl connect status --host nas --json
synopkgland ctl connect connection status --host nas --json
synopkgland ctl connect ports list --host nas --json
synopkgland ctl connect binaries status --product tailscale --host nas --json
```

Operation names, schemas, effects, AI classes and command paths are authored
only in [operations.json](../operations.json). Read the generated
[operation reference](../generated/operations.md) and
[tool catalog](../generated/tools.json). Run `npm run ctl:check` to detect drift.

Reads are AI `read` tools. Every exposed state change is a `draft`: the AI tool
calls a read-only preview and returns a redacted plan; only a separate normal
ctl invocation with human authorization and `--yes` can execute the write.
Preview is a proposed request, not a simulation or a promise that the current
provider state will accept it. The existing write handler validates again.

Every command declares a `resultSchema`; a reply that does not match it is
refused by the CLI as `CTL_RESPONSE_INVALID`.

## Choose a method and find the next step

`recommend --goal apps|services-web|services-desktop|all` ranks the same catalog the
Overview's guide and comparison table show (`backend/internal/advisor/catalog.json`) using what the
NAS reports (global IPv6 address, running methods, the detected DSM HTTP port).
The goals go from the narrowest reach to the widest: `apps` is DSM and package
web pages in a browser (Cloudflare temporary address first); `services-web` is a
service such as Drive from its phone app or a browser, which use DSM's HTTPS
address (Cloudflare own domain first, nothing to install); `services-desktop` is
a computer's TCP client such as Drive Client sync on 6690 (Tailscale first:
Cloudflare carries web pages only); `all` is every service
including SMB (Tailscale first, then NetBird).
It reads only and never enables anything. Each option carries its plain-language
security (`who`, `via`, `public`, `pairWith`), its `protection` (`cloudflare`,
`private-network`, `synology-relay`, or `none` when services
face the internet directly, as with direct IPv6), a `suitability`
(`recommended`, `alternative`, `other`, `unavailable` with `reasons`) and `next`:
the first `connection connect` input, or a user step: `open-dsm-quickconnect`,
`open-dsm-ddns` (DSM's DDNS, with port forwarding on the router) or
`open-free-link` (the Free link starts from the Cloudflare page with a
Cloudflare API token; ctl has no command for it). `needs` lists what must be obtained first:
`exposure-consent` (ask the user before sending `confirmExposure:true`),
`browser-login`, `domain-on-cloudflare` (with the Cloudflare Registrar `url`;
a domain bought there needs no nameserver change), `dsm-http-port` or
`router-setup`.

`diagnose` returns, per provider, the state, failure `code`, connector program
(`installed`, `missing`, `busy`, `unknown`), addresses (including Tailscale and
NetBird private addresses) and `next`:

| State | `next` |
| --- | --- |
| `needs-login` | user `open-login-url` with `url`, then `diagnose` again |
| `needs-machine-auth` | user `approve-device` in the network's admin console, then `diagnose` |
| `connecting`, `stopping` | `diagnose` again after a pause |
| `failed` with `binary_required` | `binaries update` for the product, then `connection connect` |
| `failed` Cloudflare with a saved own domain | `connection connect` `{provider:"cloudflare",mode:"domain",confirmExposure:true}` with `needs:["user-consent"]`: reconnects the saved domain and its services without a new sign-in |
| other `failed` | user `resolve-failure`; read `code` |
| `connected` | `connection check` for the provider |

```sh
synopkgland ctl connect recommend --goal all --host nas --json
synopkgland ctl connect diagnose --host nas --json
```

Each `recommend` option also names who provides HTTPS (`https`):
`cloudflare-certificate` (both Cloudflare options: Cloudflare's browser-trusted
certificate, no DSM certificate or open port; encrypted from the browser to
Cloudflare and from Cloudflare through the tunnel to the NAS, and Cloudflare
decrypts in between), `tailscale-certificate` (a ts.net certificate once HTTPS
is on in the tailnet, otherwise HTTP inside the tailnet), `wireguard-http`
(NetBird: HTTP inside its WireGuard network), `dsm-managed` (QuickConnect) and
`own-certificate` (DDNS and IPv6: set up a certificate in DSM yourself).

`gate` names the sign-in in front of the NAS's own: `cloudflare-access` (own
domain: optional Cloudflare Access email sign-in), `dsm-sign-in` (Free link:
optional DSM sign-in before an added app), `network-sign-in` (Tailscale and
NetBird: each device signs in to the network) or `none` (Cloudflare temporary
address, QuickConnect, DDNS, IPv6).

## TCP services go over a private network

Cloudflare publishes web pages only. For Drive Client, SMB or SSH, propose
Tailscale or NetBird: devices join a private network with the whole NAS.
Syno Connect Center versions before this one could publish Cloudflare TCP services; an
upgrade drops them silently, and their `<name>-<service>` DNS records stay in
the user's Cloudflare zone, where the user may delete them.

`recommend` options carry `tier` (an order, not a name: 1 Cloudflare temporary
address, 2 Cloudflare own domain) and `scope`
(`one-web-address`, `address-per-app`, `per-service`, `private-network`,
`synology-services`).

Each option also says what the user decides on:

- `risk`: `own-devices` (only your devices) or `login-page` (anyone with the
  address sees a sign-in page).
- `install`: what each connecting device installs (`none`, `tailscale`,
  `netbird`, `cloudflared`, `synology-apps`), whether computers (`desktop`)
  and phones (`phone`) can use it (`yes`, `no`, `unknown`) and official
  download `links` per platform.
- `pricing`: `limits` (message keys such as `workers-daily-requests`), official `links` and the month they were `checked`.
  Only official pricing and terms pages are used; Tailscale's Personal plan is
  free for up to 6 users, NetBird's cloud free plan for 5 users and 100
  devices and Cloudflare's free plan uploads up to 100 MB per request (all
  checked 2026-10). The upload limit applies to every way through
  Cloudflare's edge: own domain, workers.dev and the temporary address.
- `guide`: the setup steps the Overview opens, as message keys; `guideFor`
  replaces them for one use.
- Tailscale and NetBird `next` steps carry only the provider: tailnet and
  NetBird peers reach Drive Client (6690), SMB (445) and DSM at the NAS's own
  address, so nothing is mapped.
- `protection` `none` (direct IPv6) is never `recommended`, even when it would
  rank first; as an `alternative` its `reasons` hold `no_protection`.

For `services-desktop` Tailscale ranks first, then QuickConnect. For
`services-web` the Cloudflare own domain ranks first, then QuickConnect. For
`all` Tailscale ranks first and NetBird second; SMB has no Cloudflare setup
action. The
`tcpServices[].providers` list controls which provider offers each preset
(Tailscale and NetBird only).
The catalog's top-level `protections` maps each `pairWith` advice to DSM's
or Cloudflare's official help page.

## DSM External Access address

The UI reads and changes this setting directly at DSM's same-origin
`/webapi/entry.cgi`, with the administrator's browser session and SynoToken.
The ctl `external status`, `external set` and `diagnose` paths still relay the
caller's DSM session through loopback. With DSM session source-IP checks enabled,
this can invalidate the session, including for LAN clients. Avoid these ctl paths
with an IP-bound session; use the DSM UI instead. This change does not relax IP
checks or trust forwarded IP headers.

`diagnose` also returns `dsmSecurity`: whether DSM Auto Block (`auto-block`)
and enforced 2-factor sign-in (`dsm-2fa`, on for administrators, everyone or
chosen users) are `on`, `off` or `unknown`, read with the caller's own DSM
session (`SYNO.Core.Security.AutoBlock` and `SYNO.Core.OTP.EnforcePolicy`,
method `get`). Only administrators may read them; any other caller, or any
failure, gets `unknown`, not an error. `next` holds a user step
`turn-on-auto-block` or `turn-on-dsm-2fa` with the official help `url` for each
one that is off. Syno Connect Center never changes them. The UI reads the same two APIs at
DSM's same-origin `/webapi/entry.cgi` with the browser session, only when a
guide step shows them.

`diagnose` also returns `dsmExternal`: DSM's Control Panel > External Access >
Advanced address (`current`, ports `null` when DSM has none), the stable
address Syno Connect Center runs (`suggested`: a running Cloudflare own domain or token
address on HTTPS 443) and `state` (`matches`, `differs`, `not-set`,
`no-stable-address` with `reason` `temporary-address`/`private-address`/`none`,
or `unknown`). Only for `differs` or `not-set` does `next` hold
`external set` with the suggested input and `needs:["user-consent"]`: show the
user the current value and ask before running it. `external status` returns
the same comparison alone.

`external set` changes only the address and ports DSM advertises for share
links and QuickConnect, never DSM's listening ports, using the calling
administrator's own DSM session (no root, no helper). Only given fields change;
the same values again return `changed:false` without writing, and `previous`
holds the value before, for restoring it. Errors: `external_invalid` (DSM 3510),
`external_port_conflict` (3511, equal HTTP and HTTPS ports),
`external_denied`, `external_unavailable`, `external_unconfirmed` (DSM did not
read the new value back).

```json
{"hostname":"nas.example.com","httpsPort":443}
```

```sh
synopkgland ctl connect external status --host nas --json
synopkgland ctl connect external set --input external.json --host nas --dry-run
synopkgland ctl connect external set --input external.json --host nas --yes
```

## Connect or change provider settings

`connection connect` configures/reconnects `cloudflare`, `tailscale`, `netbird`
or `ipv6` using the same validation, lock and daemon as the UI. It accepts JSON
from a local file or stdin only, keeping tokens and private origins out of argv.
For example, save this non-secret request as `connect.json`:

```json
{"provider":"cloudflare","mode":"quick","confirmExposure":true}
```

```sh
synopkgland ctl connect connection connect --input connect.json --host nas --dry-run
# Confirm the planned public exposure before executing the same request.
synopkgland ctl connect connection connect --input connect.json --host nas --yes
synopkgland ctl connect connection status --host nas --json
```

Use the discovered schema for the other existing provider settings:

- Cloudflare: `mode` (`quick`, `domain`) and optional `subdomain`; `token` is no
  longer accepted. Every start requires `confirmExposure:true`.
- Tailscale / Headscale: optional auth `token`, `loginServer` and `loginOrigin`.
  `mappings` and `confirmExposure` are refused: peers reach NAS ports at its
  Tailscale address, and Serve carries only Apps approved on the Apps page.
- NetBird: optional setup `token`, `managementUrl` and trusted `loginOrigins`.
  A setup key with self-hosted management requires `confirmManagementPolicy:true`.
  Its management server owns network policy; ctl does not introduce local policy.
- IPv6: `confirmExposure:true` and optional `serviceIds` from `ports list`.
  Ports are resolved by the backend from approved service settings. No arbitrary
  address, hostname, mappings, token or firewall changes are accepted.

Tokens, private hostnames/origins and service names are schema `writeOnly`.
The CLI masks sensitive request fields in plans. Operation results and errors,
including ctl and MCP responses, retain their exact keys and values; logs mask
only exactly named secret fields. Keep private input and output local rather
than in chat, shell arguments, schema defaults or examples.

## Query asynchronous work

Syno Connect Center already performs slow connect/disconnect, connection checks and binary
operations in its daemon. Connection ctl calls return the existing acceptance/current
status without waiting for the long work or introducing another job protocol.
The CGI-to-daemon requests have an eight-second transport timeout; the daemon
normally accepts connect/disconnect and probes within four seconds.

After a connection request, call `connection status` again with a pause between
queries (for example, five seconds). `connecting`, `needs-login` and `stopping`
are pending states. A pending sign-in is reported as `needs-login`. Its time-limited login URL,
device code and deadline are available to the administrator in the App;
ctl returns the handler’s pending-login URL and code unchanged. Finish the browser ceremony there,
then query `diagnose` again. Only HTTPS links without embedded credentials
are accepted by the handler, and it drops them after sign-in. If `connection
connect` finds the connector program missing, it returns
`state:"connector-required"`, `code:"binary_required"` and `next`
(`binaries update`, then `connection connect` again) instead of an error, since
the CLI replaces App error hints. Once connected, use
`connection check --provider <provider>`; `checking` is pending, so query the same
check again later. `starting` means a newly created Cloudflare hostname is still
within its three-minute DNS grace window; recheck after 30 seconds without
restarting the tunnel. `externalVerified:false` never proves outside reachability.

```sh
synopkgland ctl connect connection check --provider tailscale --host nas --json
synopkgland ctl connect binaries check --product tailscale --host nas --yes --json
synopkgland ctl connect binaries update --product tailscale --host nas --dry-run
synopkgland ctl connect binaries update --product tailscale --host nas --yes
synopkgland ctl connect binaries status --product tailscale --host nas --json
```

For binary operations, poll `binaries status` for the same `product` until
`busy:false`, then inspect `phase`, `error`, `errorCode`, `current` and
`runtimeVersion`. `busy:true` / `queued` is acceptance, not a completed update.
`rollback` uses the retained previous binary and refuses when none exists.
Products are `cloudflared`, `tailscale`, `netbird`. There is no existing per-update
ID, history/list or cancel API; ctl does not invent them. Connection status is
also current provider state, not retained per-request job history. After a timeout
or lost reply, query state before deciding whether to retry a write.

## Manage services and disconnect

Add/edit custom services with `ports upsert`. Omit `id` to add; use the returned
ID from `ports list` to edit. Save the complete request in a local JSON file:

```json
{"name":"Example service","protocol":"https","port":7443,"enabled":true,"confirmExposure":true}
```

```sh
synopkgland ctl connect ports upsert --input service.json --host nas --dry-run
synopkgland ctl connect ports upsert --input service.json --host nas --yes
synopkgland ctl connect ports list --host nas --json
synopkgland ctl connect ports dsmset --id dsm-https --enabled true --confirm-exposure true --host nas --dry-run
# Use a real custom ID from ports list; built-in DSM entries cannot be deleted.
synopkgland ctl connect ports delete --id custom-00000000000000000000000000000000 --host nas --dry-run
synopkgland ctl connect connection disconnect --provider tailscale --host nas --dry-run
synopkgland ctl connect connection disconnect --provider tailscale --host nas --yes
synopkgland ctl connect connection status --host nas --json
# Only after the provider is disconnected; existing provider support still applies.
synopkgland ctl connect connection forget --provider ipv6 --host nas --dry-run
```

`ports dsmset` changes Syno Connect Center's mapping, not DSM's actual ports. Exposure,
conflict and detection checks are reused from the UI. Control port results omit
arbitrary names and internal plans while retaining IDs and diagnostic state.
`connection disconnect` accepts one provider, or omitted / `all` for every
provider, and retains settings. `connection forget` is destructive: it clears
saved service bindings for a disconnected provider where the existing runtime
supports forgetting. It does not delete cloud resources or upstream devices.
IPv6 diagnostics return counts and state without NAS addresses or endpoint URLs.
`integration status` only reads the existing opt-in helper readiness; privileged
activation remains in the existing approval flow. No command bypasses it or
runs as root.

## Safe updates

`connection connect` retains the existing provider while a configuration change is
checked and staged. Read `connection status`'s `apply.phase` and `apply.code` for
precheck refusal, applying, verified commit, verified rollback or required recovery;
`addedPorts`/`removedPorts` describe the proposed change. Do not interpret a
`connected` old provider as proof that its new service was committed. An identical
pending request must be observed rather than resubmitted. See [safe-apply](safe-apply.md)
for provider limits and the distinction between local and external health evidence.

## Registry migration and asynchronous results

Binary check/update/rollback use the existing per-product daemon queue and a
bounded job flow. `--no-wait` returns `{accepted: true, job: <status>}`; the
product remains the lookup key. Default waiting polls `binaries status` until
`phase` is `idle` or `ready`, and fails on `failed`. `busy: true` is pending.
No new job identity, history or cancellation API is introduced. A lost reply
or timeout does not authorize another start. `binaries check` writes cached
version state, so it also requires preview and human confirmation.

Connection requests keep their immediate acceptance API. There is no durable
per-attempt job ID, and an interactive browser sign-in cannot be completed by a
fixed job flow. `connecting`, `needs-login`, `needs-machine-auth` and `stopping`
remain intermediate states. Read status/diagnose after the human finishes the
browser ceremony; never treat acceptance as connected. Pending login links and device codes are returned unchanged; complete sign-in
in the App. The handler exposes them only while sign-in is pending.
