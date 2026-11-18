# Port mapping and App access contract

`backend/internal/ports` resolves enabled services without changing the provider
interface. Mappings carry stable service IDs, protocol, local port and a fixed
loopback target. These are existing upstream services, not new listeners; a bind
test cannot establish whether such a port conflicts. Provider owners determine
external names, listener ports and network scope.

Only the serialized provider owner changes live configuration. A replacement
removes old targets before applying new ones. Detection failure withdraws owned
mappings; it never falls back to stale resolved ports. Cleanup failure is visible
as cleanup-required. Successful local reconciliation does not prove Internet
reachability. Tailscale Serve carries only approved private Apps and Syno Connect Center
offers no Funnel. No operation silently widens scope.

## TLS verification and exception E2

Ordinary HTTPS targets retain certificate verification. Syno Connect Center never silently
adds cloudflared `noTLSVerify` or Tailscale `https+insecure` for an untrusted
certificate.

**E2 applies only to approved Package Land App and DSM targets.** Providers use
HTTP over Syno Connect Center-owned Unix sockets in a private 0700 `pinproxy/` directory.
The pin proxy connects to the approved loopback HTTPS port. It replaces ordinary
upstream certificate verification with the administrator-approved W or S pin:

- **W:** a certificate valid under the bundled public roots, covering every
  administrator-confirmed name, and using the same key as the current DSM HTTPS
  reference endpoint. The reference port comes from `DatastoreDetector`, never
  a hardcoded port. Names are TOFU at approval; they do not change in the
  background. A DSM target is its own reference, so its same-key comparison
  supplies no independent protection.
- **S:** the listener must present the SPKI key fingerprint the administrator
  confirmed. A key change requires confirmation. Issuer text alone proves
  nothing. The initial fingerprint is TOFU.

A failed pin refuses the connection before user data is sent. W never silently
falls back to S. Each new upstream TLS connection verifies its certificate;
App connections then perform a matching `/_spl/ping` diagnostic on that same
connection. Discovery's anonymous loopback ping is a capability check only. It
sends no session or application data and supplies no certificate trust.

Each socket binds one target, provider and canonical external authority.
Tailscale's rewritten Host is reconstructed from that binding. Cloudflare
requests must match its bound authority or receive 421 without an upstream
request. Forwarded host/protocol headers are discarded. Tailscale App targets
require HTTPS Serve and never Funnel or raw TCP. The DSM HTTPS mapping follows
the same approval and pin requirements.

## Approval scope

The administrator approves the displayed **App name and port** together with its
protection summary and certificate. Syno Connect Center forwards only to that approved
port under the pin rules above. It does not promise that all content comes from
that App. Other URLs on the same listener can be handled by another service and
will also be exposed. `/_spl/ping` catches routing mistakes; its fixed JSON is
copyable and is not an identity proof.

An installed malicious or compromised App is outside this guarantee. It can
change its writable `gateway.json`, answer a matching ping or proxy other
services. Root or the Syno Connect Center account being compromised, a listener holding a
copy of the approved private key, and the initial TOFU decision are residual
risks. Same-name removal and reinstallation between checks can be indistinguishable
from an upgrade. Syno Connect Center does not claim zero misdelivery.

Discovery reads bounded, non-symlink, non-group/other-writable `gateway.json`
files, requires native ingress and a matching package directory, and excludes
DSM ports and duplicate App ports. Missing `external` means optional integration,
but does not enable public features. Ping support identifies the private-access
capability (SDK 0.28.0); the `external` object identifies public support (SDK
0.29.0). An unreachable target is waiting; an incompatible ping needs an App
update. Neither condition grants approval.

## Persistence and lifecycle

`apps-state.json` in the persistent package DataDir is the sole approval source.
It is 0600 and written by temporary file, file fsync, rename, then parent-directory
fsync. Revisions remain globally increasing after scope deletion. Provider routes
and the 0644 `spl/external.json` projection never grant approval. The projection
contains public App ports, revisions and origins, plus the DSM public origin;
it contains no private URLs, pins or runtime details.
The menu-only `spl/launch.json`, rewritten with the projection, maps each DSM
endpoint (public and Tailscale) to the App origins, ports and active or paused
states on the same provider. It includes Tailscale authorities, which are
already public in Certificate Transparency logs, so the DSM menu can open an App
on the provider in use. Sign-in never reads it, and it never grants approval.

Opening public access creates DNS first, persists intent second, activates the
proxy/provider third, and writes the projection last. An activation failure
persists a pause and removes partial configuration. Reenable reuses existing DNS.
Pausing or revoking persists the decision before blocking and closing proxy
connections, writing the projection and removing provider routes. Cleanup may be
retried while forwarding remains blocked. Startup enforces state, restores only
valid active targets, prunes orphan routes and rebuilds the projection. Corrupt
state means no approvals and requires recovery; provider mappings cannot restore
it. A corrupt or missing projection is absent to the gateway.

## Existing service mappings

Tailscale keeps no service bindings: tailnet devices reach NAS ports at the
NAS's Tailscale address directly ([Tailscale](tailscale.md)). Binary activation,
restore and runtime replacement use the same owner lock and trusted current
source.

IPv6 supplies diagnostic candidates on the NAS's actual service ports. Clearing
candidates does not claim to withdraw a user's manual firewall or DNS rules.
Cloudflare and NetBird are not managed by the ordinary port tracker. Their
connection intent and credentials are not changed by a port reconciliation.

The ordinary port observation is a disposable tmpfs snapshot, not persistent
history: a package-private 0700 directory, 0600 file and a current settings
revision. Missing, stale, corrupt or unreadable observations do not establish an
applied mapping. Service-following is opt-in; it reads declared TCP services,
never scans listeners or automatically enables a new public scope.
