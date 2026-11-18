# App access composition

`Engine` supplies the daemon's `apps.Runtime`, `apps.Approver`,
`apps.ProviderAccess` and Cloudflare `AppTargets`. It uses the shared approval
store and PinRegistry; it never infers authorization from a provider mapping,
DNS record or socket file.

Inspection uses the verifier's fresh `Propose` or `PrepareReenable`. The service
compares the displayed App port, certificate, protection digest and revision
again on confirmation. The writer transaction also checks the expected global
revision, so an intervening refusal cannot be overwritten. W never downgrades
to S; a new S key or W name set requires the displayed confirmation.

`TrustDSMFirstUse` is wired only to an administrator's Cloudflare own-domain
or Tailscale connect request. It probes the detected loopback DSM HTTPS endpoint
and records the first certificate, issuer and expiry. A retained DSM pin is
never replaced by connect, restore, polling or health repair. `BindTrustedDSM`
creates a scope/authority endpoint only while that retained trust verifies; it
calls no provider while the provider holds its lock. The pin proxy checks an
exact DSM leaf on each handshake, including renewals with an unchanged key.

`InspectDSM`/`OpenDSM` (`apps.inspectdsm`/`apps.setdsm`) bind the displayed new
certificate and previous details to the global revision. Trust refreshes the
proxies and reconciles both configured ways. A valid-but-stale inspection also
repairs the proxy/provider and the UI refreshes status. Other scope authorities
are retained; disconnected providers stay off. App-carried DSM approvals use
the same trust record. Legacy records without a leaf digest retain their old
verification policy until a new explicit trust decision records a leaf.

Private activation starts App and DSM pin sockets and refreshes the existing
Tailscale owner, which serves exactly the App mappings; Tailscale has no other
service mappings and no Funnel. A disconnected provider is never enabled by a
refresh. Fresh daemon evidence checks the minimum Unix-target
version, HTTPS availability and MagicDNS authority.

Public activation delegates the complete selection to `Tunnel.SetPublicApps`.
That owner checks all names, creates DNS, calls `ActivatePublic` to persist the
successful subset and start its pin sockets, then replaces ingress once. Its
callbacks never call back into the tunnel while it owns its operation lock.
Batch withdrawal also persists/blocks every selected App before one ingress
reconciliation. Reenable uses existing DNS and does not require authorization.
Cloudflare connect/disconnect delegate only explicit browser authorization;
disconnecting authorization does not revoke existing App approvals.

Custom Apps (`custom.<name>` packages) are public-only. `Inspect` proposes a
scope whose pin mode is `C`: there is no certificate, and the binding summary is
the definition digest (URL, sign-in and TLS settings; the display name is
excluded). Their proxies are not in PinRegistry; every request re-reads the
approval and every dial passes `TargetGuard`. `EditCustom` keeps the address and
socket and replaces a running proxy, which also signs every visitor out.
`RemoveCustom` blocks the route, rebuilds the projection, then deletes the
Worker or DNS record; a failed cloud delete keeps the paused `removed` scope with
`Cleanup`, and `Check` retries it before dropping the definition.

`Lifecycle.Recover` runs after provider restore and before accepting control
mutations. It starts refusal-enforcing proxies, restores only validated active
App routes and each independently published DSM endpoint. Provider reconciliation
runs after releasing the approval writer lock, so retained DSM binding callbacks
can re-enter the store; the projection is then rebuilt from fresh state. The 30-second checker invokes
PinRegistry checks and retries failed cleanup. Waiting active proxies remain
alive so their bounded pin-mismatch timer survives; paused/revoked scopes never
resume automatically. Provider reconciliation is skipped when the owned route
set has not changed, avoiding periodic cloudflared restarts and disk writes.

State writers never hold Engine's map mutex during network or provider calls.
The App service serializes mutations. Public batch callbacks acquire the state
writer only after the Cloudflare owner acquired its operation lock; those
callbacks perform state/proxy work only. Refusal callbacks release the writer
before requesting provider cleanup.

Tests use the real control socket, approval store, verifier, TLS pin proxies and
Unix sockets with local TLS peers. The Cloudflare integration test uses the real
batch owner/ingress renderer with fake DNS HTTP, metrics and process launchers.
The provider test verifies that ordinary tracker replacements retain App routes.
No test contacts a NAS or a real network provider. These fixtures do not prove
DSM ACLs, provider control-plane behavior, reboot or external reachability.
