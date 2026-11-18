# Cloudflare email restrictions

The Cloudflare own-domain tab can apply one email list to the DSM and App web
hostnames managed by this Syno Connect Center installation. An empty list removes only
Syno Connect Center's Access apps; DSM/App sign-ins and any independently managed Cloudflare
restrictions still apply. The former TCP Access settings are not reused.

This increment supports own-domain web hostnames. Workers free links and Quick
Tunnel addresses must be turned off before enabling the list, and cannot be
started while it is enabled. Workers relay authentication and Quick Tunnel PIN
integration remain separate increments. NetBird machine protocol endpoints are
outside the web email list.

Native Photos, DS file, Drive and similar clients cannot complete this browser
email gate. Signing in in a browser does not authorize their separate HTTP
clients. The editor shows this warning beside the switch and in the confirmation
before saving. Use a browser, LAN or another connection for those clients.

## Token and activation

Workers, the Signal bridge and Access share one private account credential.
Existing Workers secrets migrate into that store on first use. Setting a token
from the Access section does not choose a workers.dev subdomain, publish DSM or
start a relay. Replacements check the capabilities already in use before changing
the stored credential. An account change is refused while old resources remain.

The shared create-token link includes Workers Scripts Edit, Workers Routes Edit,
Access: Apps and Policies Edit, and Access: Organizations, Identity Providers,
and Groups Edit. Scope the token to the same single account as the tunnel.
Insufficient tokens are replaced through that link; there is no permission
upgrade flow. Features that do not use Access keep their existing permission
floor.

Access is optional. Zero Trust Free activation is a one-time Cloudflare dashboard
step, costs $0 to activate, and requires a payment method; excess usage may be
charged. Syno Connect Center only links to onboarding. It does not create organizations or
subscriptions, enter billing data, or stop ordinary connections for a failed
preflight.

`tokens/verify` proves token activity, not scopes. Syno Connect Center attempts read-only
self-introspection without requesting extra API Tokens permissions. Only when
that response proves both account-scoped Access Write groups does an organization
403 become the inferred `access_needs_zero_trust` state. Without that evidence,
403 remains a permission refusal. Successful reads alone do not prove Write;
a later refused mutation preserves the protection intent and cleanup ledger.

## Synchronization and refusal

After organization and inventory preflight succeeds, Syno Connect Center persists the new
revision and closes current public proxy connections before changing cloud rules.
The apply API acknowledges an owner job; its `working` status is polled until
cloud readback completes. A local save alone never reports protection complete.

Each hostname has one self-hosted app with an app-local email allow policy,
explicit OTP provider selection and a configurable session (30 days by default). Empty lists delete these
apps instead of adding Everyone or bypass rules. Removing emails updates policies
and durably retries app session revocation before marking the revision synced.

The private ledger pins the account, random installation marker, creation nonce,
app/policy IDs, audience, OTP ownership, desired revision and pending operations.
A lost create reply is reconciled against the exact journaled name and hostname
before another create. If an ambiguous create has no visible receipt, it stays
refused for inspection rather than replaying POST or claiming cleanup complete.
Full paginated inventories detect foreign exact,
wildcard and child-path apps. Foreign objects and changed ownership are refused,
never adopted or overwritten. Owned policy drift is repaired on sync. Borrowed
OTP providers are never deleted; created ones need a complete reference scan and
verified absence before their ledger entry is dropped.

The daemon uses a demand-driven, non-overlapping reconcile loop, normally every
30 seconds, with bounded calls and backoff for temporary failures. No Access
loop runs without desired restrictions or cleanup work. App publication prepares
protection before routing; removal keeps the gate until the route owner has
withdrawn its hostname. Stale hostname inventories cannot mark coverage synced.
Failures retain private intent and resource IDs for retry, including during
empty-list cleanup. Uninstall keeps remote resources as before; clear the list
and finish synchronization before discarding private state.

Every Cloudflare-specific DSM, SDK App and custom App origin proxy also checks
`Cf-Access-Jwt-Assertion`: RS256 signature, a trusted organization issuer, this
hostname's audience, expiry/not-before and membership in the current saved list.
JWKS retrieval uses only the verified team endpoint, bounded responses and a
bounded key cache. Missing/forged JWTs, unknown audiences, removed emails,
unsynced state or unavailable trusted keys refuse forwarding. The check does
not affect LAN or private-network bindings and does not map email to a DSM user.
Dashboard gate deletion therefore does not open the Syno Connect Center origin between
polls. Editing the list closes existing streams; no Cloudflare revoke response
is presented as proof of immediate global disconnection.

Connection checks report that email sign-in is needed and keep external origin
verification false. Local connector readiness and existing pinned-origin checks
continue independently; an expected Access challenge does not restart the tunnel
or trigger a new Cloudflare login.

## Session length

The email-list form offers 15 minutes, 30 minutes, 1 hour, 6 hours, 12 hours,
1 day, 1 week, 30 days and Cloudflare's one-month preset (730 hours). The default
is exactly 30 days (`720h`). Cloudflare's API accepts duration strings using
`ns`, `us`/`µs`, `ms`, `s`, `m` and `h`, rather than a `30d` literal; its
application session documentation allows up to one month. See the
[API contract](https://developers.cloudflare.com/api/resources/zero_trust/subresources/access/subresources/applications/methods/update/)
and [session management](https://developers.cloudflare.com/cloudflare-one/access-controls/access-settings/session-management/).

An explicit form change sends `sessionDuration` with the existing confirmed,
revision-checked apply request. The ledger journals that value per existing app;
the next reconciliation updates those apps with PUT, retaining IDs, policies and
audiences. A failed update/readback leaves the pending value for retry after
restart. Once readback confirms it, the pending intent is cleared. A duration-only
change does not revoke tokens or close public streams. Existing issued tokens
retain their lifecycle; the new duration governs newly issued tokens. Changing
emails or repairing security-policy drift keeps the existing revoke behavior.

Ordinary synchronization never enforces session length. It preserves each app's
current value, including non-preset custom durations, when repairing include rules
or other owned policy drift. When apps disagree, only the DSM hostname app's
value supplies the displayed setting and creation default; the other apps are
left alone. The primary hostname is carried explicitly, not inferred from sorted
inventory order. Only an explicit duration change in Syno Connect Center sets all owned apps.
An email-only apply omits the duration so it cannot overwrite dashboard edits.

Session observations are persisted in the existing private ledger. Missing,
malformed or unreadable duration fields fall back to that app's last observation,
then 30 days, without blocking policy synchronization. The displayed value also
survives offline/refused reads and daemon restart. No separate remote read is
added to the status/UI path. Mandatory ownership and policy read failures still
follow the existing fail-closed protection rules; a duration fallback never
substitutes for security evidence. Refresh/sync shows the next cached DSM value
without discarding an unsaved email or duration draft. With Access off, the
creation default can be saved locally without a Cloudflare credential.

## Administrator API and ctl

The administrator-only APIs are `cloudflare.access.get`, `.set`, `.sync`, and
`.token`. Set requires the current revision and an explicit confirmation. The
editor clears entered credentials immediately and never reads them back.

The ctl commands `cloudflare access status`, `set`, and `sync` use the same
administrator policy. Status and mutation replies contain counts and sync state,
never emails, hostnames, credentials or remote object IDs. Supply replacement
lists in private JSON input, with `emails`, `revision`, and `confirm: true`; the
confirmation acknowledges the native-client limitation and stream interruption.

## Evidence boundary

Confidence is high for mocked API refusals/pagination, exact ownership, lost-reply
recovery, drift repair, session-revoke ordering, revision checks and origin JWT
refusal behavior. A live fixture using only the local `cf` CLI's own OAuth account
also verified self-hosted app-local CRUD, OTP binding, update readback, revoke API
acceptance and cleanup. The full apps/policies/IdP/organization inventory content
matched before and after. Cloudflare's derived `destinations` value is treated as
equivalent only for the exact public destination matching `domain`.

Actual OTP mail delivery, signed-in browser sessions, live edge enforcement,
WebSocket/download interruption, individual native client versions and NAS
lifecycle acceptance remain unknown. API fixtures and local browser rendering do
not establish those results. No NAS installation or release is part of this change.
