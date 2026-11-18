# NetBird

The page opens with **Connection**, followed by **Join** while disconnected or
failed (**Apps** once connected), and **Self-host server**, which runs a NetBird server on this NAS
([netbird-server.md](netbird-server.md)).

The Join tab has no fields: **Sign in to NetBird** starts the official client
without a setup key. Syno Connect Center preopens a blank tab within the click and navigates
only after a validated challenge. A blocked popup leaves an explicit login
button, URL/QR, and separately copyable user code. Cloud accepts only
`https://login.netbird.io/activate?user_code=AAAA-AAAA`, or the verification URI
with a matching challenge code. Management (`api.netbird.io`) is not the login
origin. Syno Connect Center uses the official, unmodified client selected in Settings.

The login CLI remains alive while WaitSSOLogin waits. Socket readiness is bounded
to a three-minute readiness window with 30-second probes, URL acquisition to 60 seconds, and the App login wait to 10 minutes
starting when the challenge arrives. NetBird Cloud's observed device code window
is 15 minutes. The App timeout is not a claim that the provider code expired.
Cancel joins the owned CLI and daemon; no broad process kill or arbitrary PID.
A repeated SSO request with unchanged settings reuses its active attempt.
Settings changes stop the old session; its output cannot update the new attempt.

Advanced settings provide a transient setup key, HTTPS management URL, and an
explicit list of trusted HTTPS login origins. The management URL's HTTPS origin
is trusted for its own login page; an external IdP requires confirmation. An unknown login origin reports `login_origin_required` without
a clickable URL. A custom management server plus key requires one combined
confirmation. Keys use a private 0600 file, never argv or saved settings, and the
file is removed on every outcome. SSO challenges exist in memory only and API
responses use the SDK's `Cache-Control: no-store` policy.

Only `Connected` with management and signal connected plus a valid CIDR identity
sets `networkReady`. `NeedsLogin`, `LoginFailed`, `SessionExpired`, `Idle`,
`Connecting`, unknown/missing fields and control failures remain distinct. Cloud
expects an address in 100.64.0.0/10; explicitly selected custom management may use
its own network. Zero online peers is not a failure. When connected, status
carries `peersOnline` from the daemon's `peers.connected`; the Overview lane shows
it as the number of devices online. A status without that count omits the field
rather than guessing. The actual DSM HTTP port is
read from the existing trusted detector, never assumed to be 5000.

The IP URL is `http://<NetBird IP>:<detected HTTP port>/`. FQDN is taken from daemon
status and displayed as a URL only after the owned SOCKS connection resolves it
and confirms the DSM API at that same service. `connection.check` requires a
ready NetBird identity and checks the detected DSM HTTP port on host loopback,
with connect/total/body bounds of 5s/10s/64KiB. The userspace SOCKS stack cannot
hairpin to its own overlay IP; that refusal does not mean peer access is broken.
The background health check uses the same local API check plus the DSM web entry.
A generic HTTP 200 or redirect is refused by API discovery; the check requires
`SYNO.API.Info` evidence for `SYNO.API.Auth`.
It does not follow redirects, weaken TLS, or accept a browser-selected target.
NAS service readiness and external access are separate; external verification
stays false. Test the address/QR from another NetBird device.

The package-account daemon uses `NB_STATE_DIR` in a private tmpfs runtime directory,
while `client/default.json` retains the persistent identity/configuration.
Turning NetBird off keeps that identity, like Tailscale, so turning it on again
resumes the same peer without a sign-in. Syno Connect Center keeps one identity per
management server: `identity.json` names the server of `client/`, and switching
servers parks it under `identities/<hash>` and restores the new server's one.
`connection.forget` deletes them while off; the old peer stays in the
management dashboard until removed there. The daemon runs with
userspace netstack, local forwarding, and a controlled dynamic SOCKS port bound
to 127.0.0.1. It disables host routes/DNS, IPv6, built-in SSH and remote jobs.
It does not install a system service, create TUN or run a privileged helper.
NetBird management policies determine NAS port access; Syno Connect Center's service list
is not a port allowlist. HTTP service traffic runs inside encrypted WireGuard.

## Validation boundary

Go command/session/stream/SOCKS fixtures and Vue tests verify the local contract.
They do not prove account registration, policy enforcement, real peer access,
DSM compatibility or ARM login completion.

With an authorized test SPK, the shortest account test is: start SSO, finish the
Cloud device-code page, observe Connected and the IP URL, run the NAS check,
then open IP/FQDN from another NetBird device. Test cancel/retry, a restricted
setup key, and a separate management/IdP configuration; verify no key is retained.

## Login and network start

Syno Connect Center runs the official client's `login` first and `up` only after it
succeeds. An existing identity may log in at once without returning a login URL. The
URL and browser-login deadlines apply only to the login stage; waiting for the
management and Signal connections is handled by the network state and is no longer
reported as "no login URL". This does not guarantee management or external access.
