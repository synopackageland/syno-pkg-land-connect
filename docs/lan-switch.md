# Switch to the local network

The Cloudflare page has a **Local network** option, off by default. When it is
on, a page opened through Cloudflare (own domain, Free link on workers.dev or
temporary address) checks from the visitor's own browser whether that browser
reaches DSM directly on the local network. Only when the check succeeds does
the page offer DSM's local address. It never moves the visitor on its own and
never offers an address the browser was not just seen to reach.

No domain name or certificate is issued for this. The local address is DSM's
own: plain HTTP on DSM's HTTP port, or HTTPS on DSM's HTTPS port with DSM's
own (usually self-signed) certificate when DSM redirects HTTP to HTTPS.

## Where it shows

- Connect's own page: a notice above the page, with a link that opens DSM's
  local address in a new tab.
- The phone browser page (`/m`): a card on Home. Switching opens the same page
  on DSM's local address and hands the NAS list over like any NAS switch
  (the mobile repository's `src/core/lan-switch`).
- DSM's desktop is not covered: a package cannot add code to DSM's own pages.

The visitor signs in again on the local address, because a browser keeps the
DSM session per address. With HTTPS, the browser first warns about DSM's own
certificate; the notice says so.

## How the check works

`phone.lanprobe` (administrators only) returns DSM's LAN IPv4 addresses on
which DSM's port answers, the scheme DSM uses, and one-use ICE credentials
(8-character ufrag, 26-character password, valid for two minutes). The page
then opens one WebRTC peer connection per address, in parallel, with a
synthetic answer that names a single passive ICE-TCP candidate (RFC 6544) at
that address on Connect's check port. Connect's responder answers only STUN
Binding requests signed with an issued password, so a connected check proves
both that this browser reached that address and that it is this NAS. The
first address to connect wins and every other check stops. Anything uncertain
offers nothing: no WebRTC, no answer within 5 seconds, a rejected candidate,
any error.

This avoids what a fetch from an HTTPS page would hit: a fetch to
`http://<LAN IP>` is mixed content, a fetch to `https://<LAN IP>` fails on the
self-signed certificate (and a fast failure cannot be told apart from a
reachable host), and Chrome 142 and later ask for local network access before
such a fetch. An ICE check is none of these, and Chrome does not ask.

Only an HTTPS page on a DNS name checks. A page already on an IP address, a
single-label or `.local` name, or plain HTTP is local or on a VPN already.

## Port and firewall

The responder is the manifest's `lan_probe` TCP listener, port 45001 (IPv4,
all interfaces). It runs only while the option is on. If another app holds
the port, the option shows that it is unavailable and nothing is offered. If
the DSM firewall is on, allow TCP 45001 from the local network. No router port
is opened; the check never leaves the local network.

## Methods

| Method | Permission | Effect |
| --- | --- | --- |
| `lanswitch.get` | administrators | read the option, the check port and the LAN addresses |
| `lanswitch.set` | administrators | turn the option on or off (`{"enabled": true}`) |
| `phone.lanprobe` | administrators | the probe for one page; empty when the option is off |
