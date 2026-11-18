# Tailscale

The page matches NetBird's: **Join** while Tailscale is off and **Apps** once
it is connected. There is no service list, port mapping, TCP forwarding or
Funnel, and Syno Connect Center runs no Tailscale control server.

## Join

**Sign in to Tailscale** starts the official client without an auth key and
opens the sign-in URL. Advanced holds a one-time auth key, a Headscale server
(the login server) and a separate trusted login origin. The request carries
only `provider`, `token`, `loginServer` and `loginOrigin`; any mapping, Funnel,
hostname, mode, service or exposure field is refused.

The auth key goes to a private 0600 file read by the CLI and removed on every
outcome; it is never in argv or saved settings. `up` is an owned foreground
child. Socket startup is bounded to 10 seconds and URL acquisition to 60
seconds. A valid challenge starts a 10-minute App wait (not the URL expiry),
which machine approval shares; on expiry `machine_approval_wait_timeout` keeps
the approval reason. Cloud login accepts only
`https://login.tailscale.com/a/<bounded-alphanumeric>`; Headscale accepts the
saved exact HTTPS origin or the saved login origin.

## Reaching the NAS

Tailnet devices reach DSM and any other NAS service at the NAS's Tailscale
address and the service's own port, as with NetBird. Status lists
`http://<Tailscale IPv4>:<DSM HTTP port>/` and the MagicDNS name on that port;
the port comes from the DSM detector and is never assumed.

Evidence (Tailscale v1.102.4): with `--tun=userspace-networking`, tailscaled
sets `ProcessLocalIPs`, and netstack's `acceptTCP` dials `127.0.0.1:<port>` for
a connection to the node's own Tailscale IP when no Serve handler claims that
port. Confidence: high from source; unknown on a NAS, which this change did not
test. Tailnet access rules decide which devices connect; NAS sign-in and
permissions still apply.

## Apps

The Apps tab is the shared App list (`AppList way="private"`), with the same
rows and the same App dialog as every other way. An App turned on for Tailscale
is the only thing Syno Connect Center serves: `tailscale serve --https=<port>
unix:<pin-proxy socket>` per approved App, in a foreground child, so the App
opens at `https://<MagicDNS name>:<port>/` with a valid certificate. This needs
MagicDNS, HTTPS certificates and Tailscale 1.104.1 or later; without HTTPS the
connection reports `serve_prerequisite_missing` and DSM stays reachable by
address. Serve readback accepts only HTTPS routes to the approved sockets and
refuses any `AllowFunnel` entry. With no App on, Syno Connect Center withdraws its routes.

## Checks and supervision

`connection.check` requests DSM's API on loopback at the detected HTTP port,
without redirects, with a 64 KiB body bound, and requires a `SYNO.API.Auth`
descriptor. The health check watches the daemon state and the App routes. The
SOCKS dialer reaches peers only, so the NAS cannot test its own tailnet
address; `externalVerified` stays false. Settings changes that keep the key and
server apply without a restart; a new key or server restarts the session. Turning Tailscale off stops the owned children and
keeps the node identity; no files or processes remain beyond the private state
directory. While off with a kept key the page says the NAS is still a device in
the tailnet, links to the admin console and offers **Forget on this NAS**
(`connection.forget`), which deletes `tailscaled.state` locally only.
