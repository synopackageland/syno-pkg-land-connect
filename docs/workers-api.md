# Public without a domain: API contract

All methods use the existing administrator-only CGI and private daemon socket.
Provider credentials and Cloudflare response bodies never appear in responses.
The contract is additive: omitted `provider` retains Cloudflare domain behavior.
Worker activation requires the installed relay runtime.

| Method | Params | Result |
| --- | --- | --- |
| `apps.workersstatus` | `{}` | `{connected, accountId?, subdomain?, code?, dsm?: {provider, url}, dsmCode?}` |
| `apps.workersconnect` | `{token, accountId?, subdomain?}` | Same status |
| `apps.workerspublishdsm` | `{}` | Same status; publishes DSM on the Free link (one-click Add DSM) |
| `apps.cleardsm` | `{scope:"public", provider?}` | `{cleared:true}` after all public App scopes were cleared |
| `apps.workersdisconnect` | `{}` | `{disconnected:true}` after Free link Workers are removed; shared credentials stay while in use |
| `apps.list` | `{}` | Existing fields plus `workers` status and its UI alias `relay`; workers App URLs appear in `app.relay` |
| `apps.inspect` | `{package, scope:"public", provider:"workers"}` | Existing inspection and confirmation token |
| `apps.setpublic` | `{packages, enabled, provider:"workers", approvals?}` | Existing mutation results |
| `apps.inspectdsm` | `{scope:"public", provider:"workers"}` | Existing DSM-only certificate inspection |
| `apps.setdsm` | `{scope:"public", provider:"workers", dsm, token}` | Existing DSM approval result |
| `apps.reenable` | `{package, scope:"public", provider:"workers", approval}` | Existing mutation results |
| `apps.clear` | Existing params | Withdraw the selected scope, regardless of provider |
| `apps.customadd` | `{name, url, appSignIn, insecureTls, acceptRisk?}` | `{id}`; the custom App then opens through the ordinary public approval |
| `apps.customedit` | `{id, name, url, appSignIn, insecureTls, acceptRisk?}` | `{id}`; an approved App keeps its address |
| `apps.custompause` | `{id}` | `{paused:true}`; the public scope pauses with reason `admin_paused` |
| `apps.customremove` | `{id}` | `{removed:true}` after its Worker or DNS record was deleted |
| `apps.customcandidates` | `{}` | `{candidates:[{name, url, source, service?}]}` to pre-fill the add form; `service` (`id`, `title`, `desc`, `titleKey`, `descKey`) is the DSM port definition (services.d section) the port belongs to |

`provider` accepts `workers`, `cloudflare`, or omission. One public approval per
App. Schema 1 has exactly one `dsm.origin`, so Cloudflare domain and workers.dev
public modes are mutually exclusive across the installation. Inspect/enable in
the other mode returns `apps_public_mode_conflict`, even if all remaining App
scopes are paused. First clear all public App scopes (`apps.clear` or a disabled
`apps.setpublic`), then `apps.cleardsm`. The latter returns
`apps_public_apps_remaining` while any public App scope remains. Workers
Disconnect clears workers App/DSM approvals and deletes Free link Workers, even
when their relay is reconnecting. The NetBird Signal bridge and its shared token
are retained while the self-hosted server is enabled. Tokens used by email
restrictions are retained too. The Free link stays off across package restarts
and turns on only through an explicit connect; a cleanup failure retains its
inventory and shows the error in the confirmation dialog for retry.
Cloudflare authorization disconnect retains domain approvals; clear them
explicitly before changing public mode. Private network approvals are independent. Inspection
and confirmation must carry the same provider. Switching providers never copies
an approval. The confirmation binds provider, protection summary, port,
certificate, DSM approval and the global state revision.

`subdomain` is the desired account workers.dev label only when the account has
none. An existing account subdomain is reused; Syno Connect Center never renames it. When
the account has none and no `subdomain` is sent, nothing is saved and the reply
is `connected:false, code:"workers_subdomain_required"`, so the page asks for a
name and sends the token again with it. Cloudflare refusing the name (another
account holds it, or it is reserved) returns `workers_subdomain_taken`; another
name can be sent with the same token. Renaming the subdomain in Cloudflare
changes every workers.dev address on the account. Syno Connect Center follows it: every
`apps.workersstatus`, `apps.list` and token paste re-reads
`GET /accounts/{id}/workers/subdomain` (on request only, no timer), saves the
new name and moves the DSM address, every App address, the projection and the
launch map to `<worker>.<new>.workers.dev`. Nothing is redeployed and no
Disconnect is needed. Only a name typed for an account that has lost its own
while Workers exist returns `workers_disconnect_required`.

Turning the Free link on (the first successful connect) publishes DSM on its
own Worker at once. DSM signs people in itself, so no Syno Connect Center sign-in stands in
front of it, and the trust is the NAS's certificate recorded on first use, as
on the own domain, not a review. DSM is public on one way at a time: when the
own domain serves DSM or holds public Apps, the Free link still turns on, DSM
stays where it is, and the reply carries `dsmCode:"apps_dsm_public_elsewhere"`.
`dsm` names the provider (`workers` or `cloudflare`) and address serving public
DSM. A Free link that was on before this, or whose DSM was cleared, is never
published again by a status read; `apps.workerspublishdsm` does it in one
click. A changed DSM certificate still returns `pin_proxy_required` and goes
through the existing review. Tokens
are sent once in the HTTPS request body, stored privately, and never returned.
Status does not deploy, create a subdomain, or test reachability. The
reachability check is `connection.check {provider:"workers"}`: from the NAS it
opens DSM's public API list at the DSM address on the Free link (out through
Cloudflare's edge and back down the relay), and reads not connected while DSM
is not public there. A new DSM address on the Free link (turned on, Add DSM, a
followed rename) settles like a new own domain: the engine resets the check
(`Config.WorkersAddress` → `Checker.Reset("workers")`), so for
`connector.DNSSettle` (3 minutes) a failing probe reads `starting` ("Setting
up") and is retried, while the open page re-checks every 5 s; only a failure
after that window reads failed.

The UI must disclose public DSM sign-in, recommend 2FA, explain Cloudflare's
account-wide daily quotas/reset, and warn about media and upload limits before
confirmation. Workers are opt-in for every App. Quota metering is not required.
Use the existing per-App and DSM confirmation dialogs; media apps remain off
until an explicit confirmation. Display `code` through localized UI strings.

Errors retain the standard CGI/control envelope. Provider errors use stable
`workers_*` codes with fixed, secret-free messages. A cleanup error keeps the
token and inventory so Disconnect can be retried. Its safe status remains
`connected:true, invalid:true, code:"workers_cleanup_required"` while credentials
remain; forwarding stays disabled. Replacing the token for the same account
preserves this withdrawal intent and permits retrying Disconnect. No Cloudflare global token
revocation is performed; users may revoke their API token in Cloudflare.


## Shipped page compatibility

The App URLs page uses `apps.relayconnect`, `apps.relaydisconnect` and
`apps.setrelay`. These are aliases for workers connect/disconnect and public
activation with `provider:"workers"`. Inspect/reenable/DSM requests may use
`scope:"relay"` as an alias for `scope:"public", provider:"workers"`.
They persist the same public approval, never a second relay scope. `apps.list`
returns workers scope views in `app.relay`; domain scopes stay in `app.public`.
The `workers` and `relay` top-level statuses contain the same safe credential
summary. Media policy marks Jellyfin high and Audiobookshelf medium; neither
is enabled implicitly.

Token-only onboarding selects an account only when `/accounts` returns exactly
one accessible account. Multiple/zero accounts return `apps_relay_accounts`;
create a token limited to one account, or use `apps.workersconnect` with an
explicit accountId. Alias errors for invalid token, missing scope and network
failure use the existing localized `apps_relay_*` codes. The public mode
conflict and App-withdrawal errors have localized strings in all nine locales.

`connected` means a validated credential was saved, not that the NAS connector
or a public URL is reachable now. A failed subdomain creation stays pending,
with `connected:false`; retry Syno Connect Center to complete it. Existing account-wide
subdomains are never renamed or removed by Disconnect.


## Clear, disable and token permissions

Uninstalling Syno Connect Center itself does not delete remote resources. The DSM uninstall
wizard links to the [Cloudflare dashboard](https://dash.cloudflare.com/) and
lists saved Worker names so the administrator can remove unneeded Workers
manually. This reminder does not call Cloudflare or change the existing
Clear/disable, Disconnect or managed App-removal behavior below.

Both disabling a workers App and Clear withdraw approval and close its local
streams before deleting that App's Syno Connect Center-owned Worker. The shared DSM Worker
stays until Disconnect. If cloud deletion fails, a blocked cleanup row and its
private inventory remain for explicit retry. Confirmed App uninstall also deletes
its owned Worker: two consecutive checker polls must find both INFO and the
package directory absent, so transient upgrade metadata gaps do not qualify.
Uninstall cleanup failures retry on the 30-second checker and after daemon
restart. The row stays removed and reinstall needs fresh approval. Other safety
pauses do not delete Workers. Clear removes private/domain routes too; its domain
DNS records stay. Confirmed uninstall deletes the App's own-domain DNS record
only after fresh exact tunnel ownership checks; foreign records stay.
Disconnect removes all owned App and DSM Workers before deleting credentials.

Syno Connect Center asks for one Cloudflare API token, shared by the Free link and the
self-hosted NetBird server's Signal bridge (see
[netbird-server.md](netbird-server.md)). The token link pre-fills
**Account → Workers Scripts → Edit** and **Zone → Workers Routes → Edit**,
and the instructions say to keep both. The Free link itself uses only
Workers Scripts Edit, including token-only account discovery: a fresh
free-account test with that permission alone returned HTTP 200 from
`GET /accounts?per_page=50` and completed first-time subdomain registration,
deployment, relay traffic and owned-Worker cleanup. See `relay/PERMISSIONS.md`
for the reproducible steps. Explicit `accountId` remains available when
discovery returns zero or several accounts and validates through the
workers.dev subdomain API. No Account Settings Read, DNS, tunnel or
account-write permission is requested. A saved token without Workers Routes
Edit keeps the Free link working; the NetBird tab names the missing permission
(`workers_routes_permission`) before its setup. The bridge uploads nothing
until the zone's routes are readable, and its removal deletes the Worker
even when routes are refused, since deleting a Worker deletes its routes.
Disconnect deletes the bridge's route and Worker with the others. `noDsm:
true` on connect saves the token without publishing DSM and without a
workers.dev name (saved state then has an empty `subdomain` with
`pending_subdomain`, and no Free link Worker runs until a name is set);
status reports `token: true` while a usable token is saved. Connecting with
another token for the same account replaces the saved one. A malformed
or invalid token rejected with HTTP 400/401 by the token-verification endpoint
is reported as `apps_relay_token_invalid` by onboarding, with localized copy.

## DSM menu destinations

The existing projection writer also writes `spl/launch.json` beside
`external.json`. A public workers DSM endpoint produces a route with
`provider:"workers"`, `dsm:"https://<dsm-worker>.<account>.workers.dev"` and an
`apps` map keyed by package ID. Each approved workers App supplies its own
workers.dev `origin`, local gateway `port`, and `active` or `paused` state.
Cloudflare domain Apps and private Apps are excluded from that workers route;
private DSM endpoints have their own route. A DSM-only approval has an empty
App map, so the menu cannot borrow an App URL from another provider.

Activation, explicit reenable, E1 pause, disable, Clear and Disconnect rewrite
the map through the same projection path. Startup rebuilds a missing map from
saved approvals. Clear/disable removes the App entry while retaining the shared
DSM endpoint; clearing DSM or Disconnect removes its route. The SDK menu
launcher uses this map only to choose a destination, never as sign-in authority.
`external.json` remains strict schema 1 with its existing fields unchanged.

## Default addresses

New App Workers use the App's short name (`SynoPkgJellyfin` becomes `jellyfin`);
DSM starts at `dsm`. Collisions with another local App or foreign Worker try
`<short-name>-<NAS-name>`, then append `-2`, `-3`, and so on. The NAS hostname is
lowercased and sanitised to a DNS label, falling back to `dsm`. Names fit one DNS
label and contain no generated installation code. Existing saved names remain
unchanged, and administrator-edited names retain their collision checks.
