# Changelog

## 0.1.0-0191

- Cloudflare: a new Local network option (off by default) lets a page opened through the own domain, Free link or temporary address offer DSM's local address when this browser is shown to reach it on the local network
- Phone page: Home offers Switch to local network in the same case; you sign in again there
- Cloudflare: the temporary address now says each upload must finish within 100 seconds, so large files may fail; the own domain and Free link keep the 100 MB per file limit
- Cloudflare: on narrow screens the upload limit line no longer starts a line with "·"

## 0.1.0-0190

- Phone page: Photos, Drive, Chat, Calendar and other Apps that open in a page of their own open directly and much faster; Reopen shows only when loading failed
- Phone page: Home shows the DSM version on its own line, with an Update available tag that opens Update & Restore (administrators)
- Phone page: Security Advisor and its Security Baseline, Hyper Backup's task page and DSM grids fit the phone; grids scroll sideways as a whole
- Phone page: the browser tab, the home screen icon and the Add to Home Screen card show DSM's own icon
- Cloudflare: own domain, Free link and temporary address show that uploads are limited to 100 MB per file and downloads have no limit
- Cloudflare: an App address removed before the tunnel was recreated is now cleaned up, so later App address changes no longer fail

## 0.1.0-0186

- Phone page: text fields highlight their whole rounded field on focus, and dashboard cards, card headers and toolbars in DSM Apps fit the phone

## 0.1.0-0185

- Phone page: adding a NAS asks you to trust its certificate when needed; Apps open at once with a launch screen, and Surveillance Station sub-Apps fit the phone
- The Add connection wizard shows every way at once

## 0.1.0-0184

- Updated to the latest UI kit and SDK

## 0.1.0-0183

- Phone page: pull down on Home, Apps and Notifications to refresh
- Phone page: in package Apps with side tabs, the tabs open from the menu button; side panels sit above or below the content and lists fill the screen
- Phone page: Add NAS also adds a NAS without Syno Connect Center and opens its DSM in a new tab; when the browser does not trust the NAS certificate, open DSM to accept it and the page checks again when you come back

## 0.1.0-0182

- Phone page: the address bar keeps showing /m after signing in and after a reload
- Phone page: Apps with their own desktop, such as Surveillance Station, open as a list that leads straight to each feature; Virtual Machine Manager statistics show in full on a narrow screen

## 0.1.0-0181

- Phone page: in Container Manager lists, each label and its value stay on one line
- Phone page: Virtual Machine Manager, Tiering, Synology Chat and the Active Backup for Business Portal fit the screen
- Phone page: the top bar of Synology apps is more compact

## 0.1.0-0180

- Phone page: the Active Backup for Business calendar and the Synology Drive welcome screen are no longer cut off
- In the Scan with your phone section, the left and right columns are centred vertically beside the phone

## 0.1.0-0179

- On a phone, the page background, cards and nested panels look the same as on a computer

## 0.1.0-0178

- Phone page: Synology Drive, Active Backup for Business and its Portal fit the screen, and the Portal opens sooner
- Phone page: short Chinese and Japanese column headers no longer break between characters

## 0.1.0-0177

- Phone page: charts, host menus and chart time labels fit the screen
- On a phone, App URLs, App details and Connect's settings sections fit the screen, and an opened section no longer shows the list behind it

## 0.1.0-0176

- Phone page: Apps such as Connect and Reclaim open instead of loading forever
- Phone page: Package Center, Log Center and tables fit the screen, and wide tables scroll sideways with the first column pinned

## 0.1.0-0175

- Phone section on the Overview in three columns, with a larger QR code and phone preview
- Tap the phone preview to try Security Advisor, resources and notifications

## 0.1.0-0174

- Interface polish: hover over shortened text to see all of it

## 0.1.0-0173

- Opens in your DSM display language

## 0.1.0-0172

- The interface loads slightly faster

## 0.1.0-0171

- The phone page has a short address: add /m to your DSM address. QR codes and links now use it
- Requires DSM 7.2 or later

## 0.1.0-0170

- Drop-down lists show the selected name correctly

## 0.1.0-0169

- Deleting an App now also removes its domain and route, and panels display more smoothly on phones

## 0.1.0-0168

- Button labels wrap on narrow screens so the full text stays visible

## 0.1.0-0167

- Rounded corners and spacing are more consistent, and interface animations feel smoother

## 0.1.0-0166

- The Overview shows your NAS's QuickConnect and DDNS addresses
- A new “Use DSM on your phone” section lets you pick a connection, scan its QR code, and add DSM to the Home Screen on iPhone or Android
- On the phone page, Add NAS checks every address at once and explains why a NAS can't be reached
- Numbers on the phone page's home screen now update smoothly
- Typing on an iPhone no longer zooms in the page

## 0.1.0-0162

- On phones, tapping an App icon now opens it reliably, DSM overview pages such as Drive fit the screen, switching screens no longer makes the page wobble sideways, and the keyboard no longer covers form buttons

## 0.1.0-0161

- When opening DSM Apps on a phone, wrapped titles no longer overlap the text below, and the right borders of two-column cards remain fully visible

## 0.1.0-0160

- When opening DSM Apps on a phone, card rows and expandable lists rearrange to fit the screen without being cut off. Side-by-side values no longer overlap on narrow screens

## 0.1.0-0159

- Vendor the phone browser page from mobile 774b6c6: on Home, the CPU and memory lines are one continuous line that scrolls smoothly, a poll period behind the numbers; utilization is read every 5 seconds while Home is visible, and the numbers glide to each new value
- Center the Phone button's icon on its label (#218)

## 0.1.0-0158

- Make the phone in each Phone panel an upright live demo of the phone browser page: Home and Apps tabs, three Apps that open their mobile-fit screen, and a faint tap hint until the phone is tried
- Center the phone and its code in the panel; a long address wraps
- Add scripts/phone-apps.mjs to capture the DSM App screens; phone-preview.mjs writes every screen and the button positions

## 0.1.0-0157

- Pin SDK 0.53.41 (Go module, @synopackageland/sdk and the app pin) and UI ~0.25.17: stale internal socket recovery and tolerant SQLite backups
- Read the Overview's phone.addnas (administrators only) only after the entry gate lets the Home refresh
- Update the Overview test for the Local network card shown while no way runs

## 0.1.0-0156

- Add Phone to each way's card on Connections, with the phone browser page's QR code and address on that way and what the phone needs; Connect your phone keeps only the app's add-NAS code
- Vendor the phone browser page with switching between several NAS
- Phone browser page fixes: the cover's ring spins, DSM's loading masks stay under the cover, App icons fall back to the next DSM size, iPhone menu taps switch on one tap, text fields bring up the keyboard without zooming, and oversized dialogs and tours become centered sheets

## 0.1.0-0141

- Pin CLI 0.52.5, SDK 0.53.4 in Vue and Go, and UI 0.23.6
- Include the shared fix for first-time administrator approval on phones
- Fix extra space at the end of phone pages, sideways panning and sticky offsets through the shared SDK and UI

## 0.1.0-0139

- Register the direct SDK phone entry with the Syno Package Land mobile app, without Application Portal
- Retain administrator-only access and browser language fallback for the mobile entry
- Keep phone page actions in the SDK's single overflow menu so the header fits
- Pin CLI 0.52.2, SDK 0.53.2 and UI 0.23.2

## 0.1.0-0138

- Keep the previous and current DSM HTTPS ports visible when a named Cloudflare tunnel's stale route is withdrawn or restored, so port changes offer Review and reconnect instead of a certificate-change warning
- Preserve the existing explicit Trust flow and refuse stale forwarding until the administrator reviews the new endpoint

## 0.1.0-0137

- Keep only the two newest NetBird corrupt-state backups and remove failed JSON temporary files older than five minutes
- Prune the oldest Tailscale certificate pairs and DNS cache entries within fixed byte and entry budgets
- Evict safe cache and backup data before stopping for the 8 MiB / 256-entry tmpfs budget; name the cause and offer one-click Restart with the saved identity
- Run the tmpfs-only sweep while the provider daemon runs; stop its timer on daemon exit or provider stop

## 0.1.0-0136

- Keep temporary Cloudflare access working when DSM redirects HTTP to HTTPS by using the existing pinned HTTPS proxy
- Report DSM HTTPS port changes with the old and new ports and an inline review/reconnect action
- Bound Tailscale/NetBird files to 1 MiB, disable Taildrop and network-map disk caching, and clear transient provider data on stop

## 0.1.0-0135

- Add Connect your phone on the Home: scan its QR code with the Syno Package Land mobile app to add this NAS, or copy the same link
- Include LAN addresses first, then connected external addresses, with certificate fingerprints for untrusted HTTPS LAN addresses; omit temporary Cloudflare addresses and credentials
- Refresh frontend regression coverage for the renamed App and DSM's read-only QuickConnect and DDNS queries, isolate stale-credential cases and budget the Tailscale lifecycle test for each awaited phase
- Preserve NetBird's final login refusal when login completes during a status read

## 0.1.0-0134

- The Home draws DSM's own QuickConnect and DDNS beside your ways when DSM has them on: one QuickConnect lane with its ID, and one DDNS lane whose hub lists each hostname, provider, status and IP address. Both are read once with your DSM session and open Control Panel > External Access
- Compare all ways is current again: new Free link (workers.dev) and DDNS rows, a Sign-in in front column (Cloudflare Access email sign-in on your own domain), DSM and each app on its own address for Cloudflare, every NAS service for Tailscale, and QuickConnect's shared quickconnect.to domain

## 0.1.0-0133

- The App is now called Syno Connect Center; nothing else changed

## 0.1.0-0129

- Pin SDK and App 0.50.15, UI 0.20.12 and CLI 0.48.21
- Use the installed CLI directly for unmasked ctl status responses
- Include installed App names and the main-feature, wizard and DSM readiness regressions

## 0.1.0-0128

- Pin UI 0.20.9
- Offer DSM and installed Synology apps in From this NAS using Login Portal aliases and custom ports, with native names and icons
- Forward only supported portal suggestion fields so extra DSM metadata does not block the installed picker
- Support a start path in custom URLs while preserving DSM resource and sign-in API paths
- Keep the picker visible while loading, empty or failed, with inline progress, manual URL entry and Retry

## 0.1.0-0127

- Pin UI 0.20.6 and SDK 0.50.12, retaining CLI 0.48.18
- Fix phone navigation click handling, short header titles and segmented-control labels through the shared UI
- Refresh shared borders and lifecycle dialog descriptions
- Regenerate session retry metadata
- Use the UI full-page root for standalone launches, including safe areas and keyboard resizing

## 0.1.0-0126

- Pin UI 0.20.3
- App URLs: drag any column edge (or use the arrow keys) to resize; the address is the widest column and widths are kept after a reload
- The Cloudflare page's Apps tab resizes and remembers its widths the same way

## 0.1.0-0125

- Pin SDK 0.50.3, UI 0.20.1, CLI 0.48.9 and templates 0.40.1
- Keep SDK response content intact for AI/ctl consumers while redacting exact credential fields in logs
- Follow the UI default full-width callouts without a local fill override

## 0.1.0-0124

- Pin SDK 0.50.0, UI 0.20.0 and CLI 0.48.4
- Use the SDK default without large page headings, retaining page actions in the toolbar
- Rebuild all three architectures with the SDK shared 300-second operation budget

## 0.1.0-0121

- Name missing Workers authorization precisely while existing devices may remain connected

## 0.1.0-0120

- Show exact cloud object conflicts inline with dashboard location, copy, retry and safe alternative Worker names in nine locales
- Refuse unproven DNS takeovers and retain exact ownership receipts across retained-data reinstalls, mode changes and Forget
- Reclaim inactive owned DNS leftovers after fresh identity and activity checks
- Track Signal route IDs and retain external routes, including unexpected references to an owned Worker
- Poll pending dependency evidence before its cache expires, avoiding perpetual Checking
- Return setup progress immediately and refresh dependency observations without blocking status requests
- Preserve Worker upload permission failures until the user repairs authorization
- Recover missing Workers credentials without discarding cloud ownership inventory

## 0.1.0-0117

- Remove page subtitles throughout the application
- Continue self-hosted NetBird setup while its own ingress update reconnects the Cloudflare tunnel
- Follow an active setup job on repeated clicks; release job ownership after failure or panic
- Offer setup resume after a package restart, and show the active setup step
- Share one dependency result and poll owner across Cloudflare and NetBird
- Distinguish reconnecting tunnels from disconnected ones and refresh stale dependency checks after retry or repair

## 0.1.0-0115

- Keep provider identities when turned off; reconnect without signing in again
- Add Forget for disconnected providers to clear their saved identity
- Keep every connection off after reinstalling with retained data
- Automatically recreate a deleted own-domain Cloudflare tunnel using saved authorization
- Repair only DNS records owned by the exact previous tunnel and resume interrupted recovery
- Show a translated notice when tunnel recovery completes
- Rebuild all native binaries and the frontend from source

## 0.1.0-0114

- Keep Cloudflare sign-in refusals visible in the setup wizard and sign-in tab
- Explain why an enabled self-hosted NetBird server protects the Cloudflare domain and connection
- Open NetBird server settings from the wizard recovery action
- UI-only release: retain the native binaries and API contract from 0.1.0-0113

## 0.1.0-0113

- Align Cloudflare form consent, conflicts and errors with their fields and actions
- Show a connection deliberately turned off as grey “Turned off” instead of a failure
- UI-only release: retain the native binaries and API contract from 0.1.0-0112

## 0.1.0-0112

- Preserve websocket connections during startup message bursts; bounded relay queues apply backpressure instead of disconnecting slow consumers
- Keep the 16 MiB relay payload cap, including active local writes; allow heartbeat pongs to wait behind backpressure


## 0.1.0-0111

- The first Overview shows a larger picture of your devices, the way still to set up and your NAS, centred with its heading and Set up
- The setup wizard's connect step puts its main action (such as Check and connect) in the footer beside Back
- DSM is listed once in the wizard's app and address steps

## 0.1.0-0110

- Healthy self-hosted NetBird no longer shows a permanent notice on the Cloudflare and NetBird pages; a dependency problem shows a warning that can be closed
- Page notices can be closed and come back only when their condition changes; setup forms and buttons stay on the page
- Packages can request one approved WebSocket listener on the own domain through Connect
- Segmented controls slide between options (UI 0.19.4)

## 0.1.0-0109

- Updated the interface components (UI 0.19.1)

## 0.1.0-0108

- Smoother transitions for dialogs, menus, panels and notifications (UI 0.19.0)

## 0.1.0-0107

- Add a configurable Cloudflare Access session length beside the email allow list, defaulting to 30 days
- Update owned apps in place without revoking sessions for session-length-only changes
- Preserve dashboard session edits and display the DSM hostname app’s last known value

## 0.1.0-0106

- Pin Go and frontend SDK 0.40.1 for resident child recovery after clean exits
- Restart enabled Cloudflare, NetBird, Tailscale and self-hosted NetBird server children with bounded backoff after exit zero
- Keep worker clean exits in the same restart budget; preserve requested stop and disable
- Retain the actual self-hosted server exit outcome in supervision diagnostics

## 0.1.0-0105

- Use published Go and frontend SDK 0.40.0 for one shared bounded provider restart policy
- Wait for network, DNS and DSM readiness; expose waiting/backoff/failed, attempts and last exit
- Retry enabled providers with saved identity; recover transient failures on real network changes
- Clean process groups before restarting and stop polling when all providers are terminal
- Device acceptance must check waiting/backoff/failed, Retry, every enabled provider after boot, disabled-provider footprint and stop cleanup

## 0.1.0-0104

- Rebuild with frontend and Go SDK 0.38.5; retain UI 0.17.14 within the SDK peer range
- Use the shared AppShell account row, icon sign-out, main clipping and empty-slot fill fixes
- Remove the redundant root-container overflow clipping

## 0.1.0-0103

- Turn Free link off independently while retaining the Signal bridge and shared credential used by the self-hosted NetBird server
- Keep Free link OFF across restart; saving a Signal token alone does not enable it
- Centralize Cloudflare dependencies, block upstream removal while the server uses it, and explain the shutdown order with page navigation
- Show the first failed prerequisite and its recovery action on both pages, including the correct topology hop, in all nine locales
- Keep shutdown errors inside the confirmation dialog with retry controls
- Preserve domain authorization when reconnecting the same saved own domain
- Keep dependency notices and tabs usable in narrow DSM windows

## 0.1.0-0100

- Self-hosted NetBird server: turning the Free link off is refused while the server is on, because it deleted the Signal bridge and the Workers token every device needs to connect
- Self-hosted NetBird server: a running server without its Workers token says devices cannot connect and takes a token there; saving it redeploys the Signal bridge at once instead of after 10 minutes

## 0.1.0-0099

- NetBird: treat a daemon that answers but cannot reach its management or Signal server as a connection failure, instead of logging "health check incomplete" every minute forever
- NetBird: check that a restarted daemon is running without waiting for its connection, so a restore no longer stalls on "daemon_start_timeout" and the saved sign-in is used
- NetBird: when a restarted daemon is already reconnecting its saved identity, leave it alone instead of signing in again, which cancelled that connection and asked for a new SSO sign-in
- Clear "Recovering an interrupted configuration change" and "Recovery could not be verified" once the connection passes its checks again; a repair that changed nothing is no longer reported as a failed change
- README: the website reads the package source live; there is no snapshot to rebuild

## 0.1.0-0097

- Keep tab drafts with the UI kit's SectionTabs keep-mounted instead of an app-side workaround
- Sign in to NetBird before starting its network, so a slow management or Signal connection is no longer reported as "No login address was obtained"; give the daemon a three-minute readiness window
- Update to UI 0.17.14 and CLI 0.41.11; SDK stays 0.38.2 and templates 0.35.5

## 0.1.0-0096

- Rebuild every page on UI 0.17.13 as a DSM settings page: tabs on top, a Connection tab first, Reset and Apply pinned bottom right
- Keep each tab mounted so a typed draft survives a tab switch, and fill tabs that are lists with their table
- Open a failed provider on its Connection tab, where the failure is explained
- Add the A/N/X app-open smoke e2e (`npm run e2e:smoke`)
- Close informational notices with ×; notices that block or hold the only action stay
- Update to SDK 0.38.2, CLI 0.41.9 and templates 0.35.5

## 0.1.0-0089

- Set or remove a GitHub token in Settings to avoid anonymous connector download limits
- Apply the shared button policy to page actions and confirmation dialogs
- Update to SDK 0.36.0 and move private control, proxy and helper transports to SDK Unix boundaries
- Preserve approved targets, certificate pins and immediate WebSocket/stream withdrawal
- Stop and reap the helper child before closing admission, acknowledging shutdown and exiting

## 0.1.0-0088

- Self-host a NetBird server on the NAS with one click, reached through Cloudflare on your own domain
- Carry NetBird's Signal stream through a Cloudflare Worker so devices connect through the tunnel
- Share one Cloudflare API token between the Free link and the NetBird server, naming a missing permission before setup
- Renew the NetBird server's owner token, disconnect this NAS on turn off and update its binary in Settings
