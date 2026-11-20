# LAN direct: a trusted HTTPS name on the home network (design)

Status: design and feasibility only. No product code changes. Measurements were
taken on a test NAS (DSM 7.3.2), a test Cloudflare zone and a Linux container;
every record, certificate and rule created for them was removed afterwards.

## 1. Problem

A phone that opens DSM or the phone page (`/m`) through Cloudflare keeps
going through Cloudflare when it is at home. Traffic leaves the LAN, crosses
Cloudflare's edge and comes back down the tunnel. It is slower, uses upload
bandwidth and stops working when the Internet link is down.

Today the only LAN route is DSM's own address (`localRoute` in
`frontend/src/connect/phoneRoutes.ts`), for example `https://192.168.1.18:5001`.
It has three problems:

- No public CA issues a certificate for a private IP address, so DSM serves its
  self-signed certificate. The route carries the `selfSigned` note.
- A browser refuses an untrusted certificate silently in background requests
  (see the comment on `FindResult` in the mobile repo's
  `src/core/profiles/remote.ts`). So a page cannot quietly test a LAN address
  and switch.
- Switching origin loses the DSM session, because DSM's `id` cookie is
  host-only.

Goal: when the phone is on the same LAN, use a LAN path with a certificate the
browser trusts. When it is not, stay on the current route without any visible
error. Do this only where it is feasible and costs little.

## 2. Summary of the decision

| Case | Supported | How | What the user sees |
| --- | --- | --- | --- |
| Temporary address (Quick Tunnel, `*.trycloudflare.com`) | **No, never attempted** | Connect holds no zone, and the address changes on every restart | Nothing is offered. The Cloudflare page says: "A valid home-network address needs your own domain." |
| Own domain (named tunnel on the user's Cloudflare zone, signed in through `cloudflared login`) | **Yes (main target)** | `<prefix>-lan-<n>.<zone>` DNS-only A records point to the NAS's LAN IPv4 addresses. Connect gets a Let's Encrypt certificate by DNS-01 with the same sign-in, serves it on its own LAN TLS listener and forwards to DSM over loopback. The phone probes all LAN names in parallel with the current route and switches when one answers. | Off by default. One switch: "Use a valid certificate on the home network". Then the phone page opens the LAN address by itself at home. If the phone is already signed in over Cloudflare, it offers the switch (one more sign-in); it never switches mid-session. |
| Own domain in **token mode** (pasted tunnel token, no sign-in) | No, until the user signs in | The token cannot write DNS | The switch is disabled with "Sign in to Cloudflare to use this". |
| Free link (`*.workers.dev`) | **No** | The `workers.dev` zone is Cloudflare's: no A records and no custom certificates. A Package Land domain service is out by decision. The only option that needs no service and no domain from us is DSM's own QuickConnect (see §7.3); Connect may point to it but does not build on it. | "A valid home-network address needs your own domain. DSM's QuickConnect also offers one." |

## 3. Current code → design mapping

| Today | Where | Used by LAN direct |
| --- | --- | --- |
| DSM public name `<label>.<zone>`. The label defaults to the NAS host name (`publicname.NASName`); the user can rename it (`SetDSMName`, first label only). | `backend/internal/cloudflare/dsm_name.go`, `tunnel.go defaultName` | **Prefix** of the LAN names. They follow a rename. |
| App names `<app>.<zone>`, then `<app>-<label>`, `<app>-<label>-2`… (`publicname.Candidate`/`Choose`); collision check `namesFree`/`classifyName` | `app_names.go`, `publicname/name.go` | The same check runs for LAN names. LAN names go into the `occupied` list so that Apps never take them. |
| Ownership tag on records Connect writes: comment `synopkgconnect:tunnel=<tunnel id>` (observed on the live DSM record) | Cloudflare DNS | LAN records get `synopkgconnect:lan=<tunnel id>`. Cleanup deletes only records with that tag and an exact name. |
| Sign-in token: `cert.pem` from `cloudflared login`, zone-scoped, kept private (`readCert`) | `cert.go`, `api.go` (Bearer calls) | It writes A and TXT records in that zone. It is the only credential used. No new token, no new scope. |
| Account API token (Workers, Access) | `docs/cloudflare-access.md` | Not used. It is account-scoped and has no DNS permission on the zone. |
| `phone.addnas`: LAN DSM origins with leaf SHA-256 for self-signed certificates | `backend/internal/phonelink` | Adds the LAN names as `kind: "lan"` origins **without** `sha256`, because the chain is trusted. |
| QR v1 `synopkgland://add-nas#…`, up to 8 origins, LAN first | `phoneLink.ts`, mobile `profiles/qr.ts` | The App needs no format change: the LAN names are ordinary `lan` origins. |
| Phone routes; `bestRoute` prefers public HTTPS | `phoneRoutes.ts` | New route id `lan` (trusted HTTPS, needs the same network), before `local`. |
| Parallel probe; pick by rank once nothing pending can beat it; cancel the rest; 2.5 s grace | mobile `profiles/probe.ts` `NasProber` | The same pattern is used for the LAN switch (§6). |
| NAS list carried across origins in the URL fragment (no credentials) | mobile `profiles/nas-links.ts` | Carries the list from the Cloudflare origin to the LAN origin. |
| Pin proxy: Connect → DSM loopback HTTPS with a pinned leaf, forwarding headers stripped | `backend/internal/apps/pinproxy.go` | Backend of the LAN TLS listener. DSM sees the same loopback client as on the Cloudflare path today. |
| Acting with the calling administrator's DSM session | `backend/internal/dsmexternal` | Only for the optional DSM-native variant (§7.1). The recommended path needs none. |
| `golang.org/x/crypto` already in `backend/go.mod` | | `x/crypto/acme` is in the same module, so no new dependency. |

## 4. Design

### 4.1 Names: `<prefix>-lan-<n>.<zone>`

`<prefix>` is the first label of the DSM public name, the same prefix the user
already sees, for example `home` → `home-lan-1.example.com`. Three options were
compared for `<n>`:

| | A. IP in the name: `home-lan-192-168-1-18` | **B. Stable slot: `home-lan-1`, record follows the IP** | C. Wildcard below a prefix: `192-168-1-18.home-lan.<zone>` with `*.home-lan.<zone>` |
| --- | --- | --- | --- |
| IP change (DHCP) | New name, new record, new certificate. Names saved on phones stop working. | Update the A record (TTL 60 s). Same name, same certificate. | New record, same certificate. Saved names stop working. |
| Several subnets or NICs | One name per address | One slot per (NIC, IPv4 subnet) | One name per address |
| Certificates | One per name, or one SAN certificate reissued on every change | One SAN certificate (≤ 4 names). Reissued only when the set of slots changes. | One wildcard certificate |
| Key scope if the NAS is compromised | The listed names | The listed names | Every name under `home-lan.<zone>` (still this NAS only) |
| Let's Encrypt limits | Every IP change is a new identifier set: 5 per exact set per 7 days, 50 per registered domain per 7 days | Renewals of the same set are exempt from the per-domain limit (and from all limits with ARI). New sets are rare. | Rare reissues |
| Certificate Transparency | Every LAN IP ever used is published permanently | Only `home-lan-1…4` are published. IPs appear only in current DNS. | Only the wildcard is published |
| DNS cleanup | Old records pile up and must be garbage-collected | At most 4 records, overwritten in place | Old records pile up |
| Matches the requested `<prefix>-lan-…` shape | Yes | Yes | No (two labels) |

**Choice: B.** It survives DHCP changes without touching certificates or the
names saved on phones, keeps CT logs free of LAN IPs, and needs a fixed number of
records.

A wildcard on the whole zone (`*.<zone>`) is rejected. A NAS holding that key
could impersonate every name in the user's zone, including other services the
user runs.

Slot rules:

- Key: (hash of the NIC MAC, IPv4 network such as `192.168.1.0/24`). Slots are
  numbered 1–4 (`phonelink.MaxLAN`), the lowest free number first, and the
  mapping is kept in Connect's private state. Interfaces that `phonelink`
  already skips (docker, tun, wt, tailscale, …) and CGNAT `100.64/10` never get
  a slot.
- The same NIC gets a new address in the same subnet: the A record is updated,
  nothing else changes.
- A new subnet gets a new slot: its record is created, then the certificate is
  reissued with the new set.
- A subnet that has been gone for 7 days frees its slot. The record is deleted
  at once. The name leaves the certificate at the next renewal, not
  immediately, so it does not cost a rate-limit unit.
- IPv6: not in the first version. A ULA `AAAA` record on the slot name is a
  later option. Global IPv6 addresses are not LAN-only and are never used.
- Collisions: the name goes through `namesFree`/`classifyName`. If a foreign
  record holds `<prefix>-lan-<n>`, that number is skipped and the next one is
  used. Names are added to the `occupied` list that App naming uses.
- Several NAS on one zone: each has its own DSM prefix, which is already unique
  in the zone, so their LAN names cannot collide.
- A DSM rename moves the LAN names: create the new records, reissue the
  certificate, then delete the old records.

### 4.2 DNS records

- `A <prefix>-lan-<n>.<zone> → <LAN IPv4>`, DNS only (`proxied: false`),
  TTL 60, comment `synopkgconnect:lan=<tunnel id>`.
- Cloudflare zones cache a negative answer for 1800 s (SOA minimum,
  measured). If a phone looks a name up before it exists, it can see "no such
  name" for up to 30 minutes. So Connect creates the record and checks it on
  the authoritative name servers **before** it offers the name to any phone.
- The record is updated from the existing supervise loop when an interface
  address changes, with no new timer of its own. A provider that is not in use
  has zero footprint, as for the other providers.

### 4.3 Certificate: Let's Encrypt DNS-01 by Connect

DSM cannot do this itself. Its Let's Encrypt support uses HTTP-01, which needs
port 80 open from the Internet, and Connect never asks for port forwarding.
Its DNS-based issuance exists only for Synology's own DDNS names (moderate confidence). So Connect
issues the certificate with `x/crypto/acme`:

1. Create `_acme-challenge.<name>` TXT records for all slot names with the
   sign-in token, tagged like the A records.
2. Wait until the authoritative name servers return them.
3. Answer the challenges, finalize with a new ECDSA P-256 key, and delete the
   TXT records (always, also on failure).
4. Store the key and chain in Connect's private directory (0600, the same
   rules as `cert.pem`). They are never logged or returned by any API.

Renewal: use ARI (`renewalInfo`) when available, otherwise renew when one third
of the lifetime is left. Staging issued a 64-day certificate (measured), and
lifetimes are going down, so renewal must run without anyone present. It runs
from the supervise loop. On failure it backs off and respects "5 failed
authorizations per identifier per hour". An expired certificate is a safe
failure: phones fail the TLS check and stay on the current route.

Measured: the TXT record was visible on the authoritative servers after 7 s,
and the whole issuance took 9 s.

### 4.4 Serving: a Connect LAN TLS listener in front of DSM

The certificate must be served without changing what DSM serves anywhere
else. Measured on DSM 7.3.2:

| Attempt | Result |
| --- | --- |
| Import the certificate and bind it to no service | Not served. SNI for the LAN name still gets DSM's default certificate. Also, `SYNO.Core.Certificate import` with `as_default=false` (multipart) **still made it the default certificate**. It had to be reset with `SYNO.Core.Certificate.CRT set` (JSON-encoded `id`). |
| Add it to "DSM Desktop Service" (`multiple_cert: true`) next to the user's certificate | The LAN SNI gets the new certificate, but **so do other SNI values and connections without SNI**. That would change what IP-address users see and would break Connect's own pin to DSM. Reverted at once. |
| DSM reverse-proxy rule `https://<lan name>:443 → http://localhost:5000` with the certificate bound only to that rule | Isolated. Only `<lan name>:443` gets the new certificate; 5001, other SNI and no SNI keep DSM's own. DSM's API, login page, sign-in (`id` cookie) and `/m` all work through it. |

The reverse-proxy rule works, but every import (creation **and** each renewal)
needs an administrator's DSM session, because the certificate files are owned
by root. With certificates of 45–64 days, nobody can be expected to open
Connect in time.

**Choice:** Connect runs its own TLS listener for the LAN names:

- It runs as the package user with no root and no DSM certificate changes.
  Renewal is fully automatic.
- It binds only to the slot IPv4 addresses (never `0.0.0.0`), so a NAS with a
  public interface does not expose it.
- It uses one port fixed per installation (default 5443 when free, otherwise
  the next free port). The address is `https://home-lan-1.example.com:5443`.
- It accepts only SNI values equal to a current slot name and closes anything
  else. It is not a general proxy.
- Its backend is the existing pin proxy to DSM's loopback HTTPS. DSM sees the
  same loopback client as for Cloudflare traffic today, so no new trust model is
  added. `/m` and the phone page work as on the DSM origin.
- DSM firewall: if the firewall blocks the port, the probe fails and phones
  stay on the current route. The Cloudflare page shows the port when the switch
  is turned on.

The DSM reverse-proxy variant stays documented in §7.1 for anyone who wants
port 443.

### 4.5 Router DNS-rebinding protection

Some resolvers drop public answers that point to private addresses. Measured:

| Resolver | Result for `A 192.168.x`, `A 10.x`, `AAAA fd..` |
| --- | --- |
| Cloudflare 1.1.1.1 and 1.1.1.3 (family), Google 8.8.8.8, Quad9 9.9.9.9, OpenDNS, AdGuard, NextDNS (no profile), Control D (plain DNS) | Answered (passes) |
| AdGuard DoH | Answered |
| dnsmasq 2.92 `--stop-dns-rebind` (OpenWrt default, many consumer routers) | Dropped: NOERROR with an empty answer, logged "possible DNS-rebind attack detected". IPv6 ULA is dropped too. `--rebind-domain-ok=/<zone>/` allows it again. |
| Unbound 1.25 with `private-address` (pfSense/OPNsense rebind protection) | Dropped: NOERROR, empty answer |

Not measured here (high confidence from vendor documentation unless marked):
Fritz!Box has rebind protection on by default with a per-domain exception list.
OpenWrt sets `rebind_protection 1` by default. NextDNS offers "DNS rebinding
protection" in a profile (whether it is on by default: low confidence). The
DoH/DoT front ends of the public resolvers above share their back ends
(moderate). Chrome Secure DNS and Android Private DNS use those providers and
add no filter of their own (moderate).

Effects:

- A blocked name fails quickly ("no address") instead of timing out. The
  phone stays on the current route and shows nothing.
- Connect can detect blocking: after creating the records, it resolves one of
  them through the NAS's own system resolver, which is usually the router. If
  the authoritative servers have it but the router returns nothing, the
  Cloudflare page shows once: "Your router blocks home addresses in public DNS
  (DNS rebind protection). Add <zone> to its exceptions to use this."

### 4.6 Hand-off of the session

| Option | Verdict |
| --- | --- |
| **One more sign-in on the LAN name, before sign-in where possible** | **Chosen.** If the page loads signed out on the Cloudflare origin and a LAN name answers, it moves before the user signs in, so there is no second sign-in. If the user is already signed in, the page offers "Switch to home network (sign in again)" and never moves a working session. DSM's own "Stay signed in" and 2FA "trust this device" then keep the LAN name signed in, so this happens once per phone. Browsers offer saved passwords across subdomains of the same registered domain (moderate). |
| One-time hand-off token minted by the NAS | Rejected. DSM has no supported API that creates a session for a user without credentials. Building one needs root or a stored credential, would bypass DSM's 2FA, auto-block and account policy, and puts a credential in a URL (history, Referer, logs). The single-use, seconds-TTL, device-bound design does not remove those problems. |
| Copy the `sid` to the new origin | Rejected. It is a credential in a URL, and DSM binds sessions to the client IP unless the administrator turned that check off. |
| DSM SSO Server / OIDC | Rejected. It needs an extra package, SSO setup and a domain-wide trust decision. That is too heavy for a speed optimization. |

The NAS list that the browser build keeps per origin goes along in the URL
fragment, as `nas-links.ts` already does. It never carries credentials.

## 5. Security review

- **Credentials.** Only the existing zone-scoped `cert.pem` token is used. It
  can already write DNS in that zone, so nothing is widened. The ACME account
  key and the certificate key are private files like `cert.pem`. They never
  enter logs, API replies or QR codes.
- **Name ownership.** Only this NAS holds the key for its LAN names. A device
  on a foreign network that happens to use the same private IP cannot present
  a valid certificate, so the phone rejects it. The certificate is the
  identity check: the phone does not switch on reachability alone.
- **DNS writes.** Writes cover exact names only, tagged, after the same
  collision check as App names. Deletions require both the tag and the exact
  name. Foreign records are never changed.
- **Exposure.** The listener is LAN-only (bound to private addresses), accepts
  only SNI for its own names, and is DSM behind the same pin proxy as the
  Cloudflare path. Users are never asked to forward a router port.
- **Privacy.** Public DNS reveals the NAS's current private IPv4 addresses (low
  risk: internal addressing only). CT logs permanently list `<prefix>-lan-1…4`.
  The prefix is already public through the DSM name, so if the user chose a
  neutral DSM name the LAN names inherit it. No MAC address, serial number or
  installation ID appears in a name.
- **Failure modes are silent and safe.** Rebind filters, firewalls, an expired
  certificate, being away from home or a denied browser permission all mean the
  phone stays on the current route.
- **Cleanup.** Turning the switch off, disconnecting Cloudflare or deleting the
  domain removes the tagged A and TXT records, stops the listener and deletes
  the keys. Uninstall keeps data by project decision, so records can remain
  after an uninstall without turning the switch off first. They are tagged
  and point only to private IPs. The page says this.
- **Dependencies.** "Disconnect Cloudflare sign-in" already checks dependents
  (`CheckRemoval`). LAN direct registers as one: without the sign-in, records
  cannot follow IP changes and certificates cannot be renewed.

## 6. Phones: how the LAN name is found and chosen

Sources of LAN names (non-secret: they are public DNS):

- the QR code (`kind: "lan"` origins, no `sha256`),
- `phone.addnas` for the Phone section,
- the phone page, which reads them from Connect once when it opens.

Probe: on page load, on `online` and when the page becomes visible again,
never on a timer.

1. Candidates are all LAN names plus the current origin. All start at once.
2. Preference: any LAN name beats the current route. Among LAN names, the
   first one to answer wins (the fastest path). The last winner is listed
   first but still probed in parallel.
3. The first LAN success cancels all other probes (`AbortController`). If no
   LAN name answers within the 2.5 s grace (as in `NasProber`), the page stays
   on the current route and stops probing.
4. A LAN name counts only if TLS validates for that name and DSM's API info
   answers there.

Browser (phone page):

- The request is `fetch('https://<name>:<port>/webapi/query.cgi?…SYNO.API.Info…', {mode: 'no-cors', targetAddressSpace: 'local', signal})`.
  An opaque success means the name resolved, TLS validated and the server
  answered. No CORS change is needed in DSM.
- Chrome 142+ Local Network Access: a public page that requests a private
  address (also through a public name) triggers a one-time permission prompt
  per site (high confidence for desktop; Android Chrome moderate). So:
  - Permission `granted`: probe silently on every visit.
  - `prompt`: do not probe on load. Show a small chip "At home? Use the faster
    home address" (only when LAN names exist). Tapping it asks for the
    permission and probes.
  - `denied`: show nothing.
  - Browsers without LNA (Safari, as far as known): probe silently.
- On success while signed out: navigate to `https://<name>:<port>/m`, with the
  NAS list in the fragment. While signed in: offer the switch (§4.6).

Android App:

- It uses the native `ProbeFn` (TLS details: `trusted && hostnameMatch`), with
  no browser permission involved.
- Benefit, moderate: the App can already trust a self-signed LAN address by
  pinning, but a pin breaks when DSM's self-signed certificate changes
  (`certState` → `changed`) and an IP-address origin breaks on a DHCP change.
  A slot name with a trusted chain needs no pin and survives both.
- Cost: none for import (QR v1 already carries it). Automatically picking among
  a profile group's origins at connect time is a separate App change, done with
  the same probe rules.

## 7. Alternatives considered

### 7.1 DSM reverse-proxy rule with an imported certificate (measured, not chosen)

This variant gives port 443 and keeps DSM's nginx in front. It was rejected for
the first version because creation and every renewal need an administrator's
DSM session, and with 45–64-day certificates renewal cannot depend on someone
opening Connect. It also has a pitfall: import marks the certificate as default
despite `as_default=false`, so it would need explicit repair. It could come
back if DSM offers an unattended import for packages, or with an opt-in root
helper (the NetBird helper pattern), at extra review cost.

### 7.2 Public certificate for the bare private IP

Not possible: public CAs do not issue certificates for private IP addresses.

### 7.3 DSM QuickConnect's own LAN names

Synology already runs a plex.direct-style service.
`<a-b-c-d>.<alias>.direct.quickconnect.to` resolves to `a.b.c.d` (measured),
and DSM keeps a Let's Encrypt wildcard for `*.<alias>.direct.quickconnect.to`.
The App already prefers these "smart LAN" names. This is the only option for
free-link users that needs no service and no domain from us. It requires the
user to turn on QuickConnect with a Synology account, which is DSM's feature
and the user's choice, so Connect only mentions it.

Not verified: which certificate DSM serves on 5001 for those names while
QuickConnect is on (QuickConnect was off on the test NAS, and turning it on was
out of scope).

### 7.4 A Package Land domain that issues names and certificates to users

Out by decision: it would be a service to run, with abuse handling, a shared
rate-limit pool and key custody.

### 7.5 Server-side "same network" hint from `Cf-Connecting-IP`

Comparing the client's public IP with the NAS's own could decide whether to
show the chip. This is deferred: the pin proxy strips forwarding headers today,
and the LNA permission state already avoids prompting on every visit.

## 8. Cost and complexity

| Part | Size (estimate) | Runs when |
| --- | --- | --- |
| Slot allocation, A record sync, tagged cleanup (reusing `namesFree`, `classifyName` and the API client) | M (~400 lines Go + tests) | Supervise loop, only when enabled |
| ACME DNS-01 issue and renewal (`x/crypto/acme`, ARI) | M (~350 lines + tests) | About every 40–60 days |
| LAN TLS listener (SNI allow list, LAN-only bind) in front of the pin proxy | S–M (~250 lines) | While enabled |
| Router rebind check and Cloudflare page switch and states | S | On enable and on status |
| `phone.addnas` / phone routes: `lan` route, QR origins | S | — |
| Phone page probe, LNA permission handling, signed-out redirect, fragment hand-off (mobile repo) | M | Page load and visibility change |
| App group auto-pick (mobile repo) | M (separate) | App connect |

Cloudflare cost: free (DNS records on the user's zone). Let's Encrypt: free;
one SAN certificate per NAS, renewals exempt from the per-domain limit. Our
operating cost: none (no service).

## 9. Measurements

| What | Result | Confidence |
| --- | --- | --- |
| `cloudflared login` token: active, sees one zone, reads tunnels | Yes | High |
| Same token may write DNS (POST A with an invalid IP → `400 / 9005` validation error, not 403; nothing created) | Yes | High (Connect already writes CNAMEs with it) |
| Same token reads SSL certificate packs | No (403); not needed | High |
| DNS-01 issuance with that token (staging) | TXT visible on authoritative servers after 7 s, issued after 9 s, TXT removed | High |
| Staging certificate lifetime | 64 days | High (measured) |
| Negative-cache TTL on Cloudflare zones | 1800 s; observed an early lookup caching NXDOMAIN | High |
| Public resolvers pass private answers | 9 of 9 (plain DNS) + AdGuard DoH | High |
| dnsmasq `stop-dns-rebind`, Unbound `private-address` | Drop A and ULA AAAA (empty NOERROR) | High |
| DSM import without binding: served by SNI? | No | High (DSM 7.3.2) |
| DSM import `as_default=false` | Became default anyway | High (DSM 7.3.2) |
| Extra certificate on DSM Desktop Service | Also becomes the fallback for other SNI and no SNI | High (DSM 7.3.2) |
| Reverse-proxy rule with bound certificate | Isolated; API, sign-in and `/m` work | High (DSM 7.3.2); 7.1 and 7.4 not tested |
| `direct.quickconnect.to` encodes the IP in the name | Yes | High |
| Chrome LNA prompt for public → private requests | Chrome 142+ | High desktop, moderate Android |

DoH to the large providers could not be measured from the test network, which
blocks it. Plain DNS to the same resolvers was measured instead.

## 10. Phased plan

1. **Backend (Connect):** LAN slots and records, DNS-01 issue and renew, the
   LAN listener, the rebind check, the switch on the own-domain page with
   states (off / setting up / ready / router blocks / needs sign-in / expired).
   Gate: unit tests with a fake ACME server and fake DNS API. One real-zone run
   on a test NAS with the staging CA.
2. **Phone page (mobile repo, vendored):** the LAN probe, LNA handling,
   signed-out redirect, the switch offer when signed in. `lan` route in the
   Phone section. Gate: a browser test against a test NAS on the LAN and off
   it.
3. **QR and App:** LAN names in `phone.addnas` and the QR. App group auto-pick
   with the same probe.
4. **Later, optional:** ULA `AAAA`; the `Cf-Connecting-IP` hint; DSM
   reverse-proxy variant on 443 if DSM offers unattended import; a pointer to
   QuickConnect LAN names on the free-link page.

## 11. Open points

- Default listener port, and whether the port may be changed in the UI.
- Whether Android Chrome enforces LNA prompts today, and how it behaves inside
  the App's WebView (not used for probing, but used for pages).
- DSM 7.1 and 7.4 behaviour of the optional reverse-proxy variant (only 7.3.2
  measured).
- Which certificate DSM serves for QuickConnect LAN names while QuickConnect
  is on.
