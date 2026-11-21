# Syno Connect Center design system

Use `@synopackageland/ui` 0.19.6, its public exports and
`themes/dsm7-embedded.css`. `AppRoot modal-scope="app"` scopes overlays to the
Syno Connect Center window; inside DSM, `dialog-bounds="viewport"` lets a dialog be dragged
across the browser viewport. Each DSM window creates a separate Vue instance and unique `idPrefix`.
Do not reproduce library controls, palettes, typography or spacing in application CSS.

The SDK `AppLayout`, `AppSidebar`, `PageContent width="fill"` and `PageHeader`
provide seven persistent pages: Overview, App URLs, Cloudflare, Tailscale, NetBird,
IPv6 direct and Settings; Settings sits at the bottom of the sidebar in a group
of its own (`placement: 'bottom'`). The page body scrolls inside the bounded DSM window;
navigation resets it to the top. SDK `CardGrid` adapts to available width.
Minimum acceptance viewport: 640×420; normal viewport: 960×640.

Overview is the Home. Which view it shows follows the backend's state alone
(`connection.status` and `apps.workersstatus`), never browser storage: with
nothing set up it is the first visit; once any way is set up, workers.dev
included, it is the returning view. "Compare all ways", the comparison table,
stays folded at the bottom of both. The first visit is one picture (your
devices, a question mark, the NAS: `NoWayPicture`), one title and one primary
"Set up", which opens the setup wizard (below); it never opens by itself.
"Add a way" on the returning view opens the same wizard, and a wizard the user
works in stays open while ways come on, until they close it or select Done.

The returning view starts with Needs attention, shown only while something is
wrong: one Callout per thing with its one action (DSM's certificate review for
Cloudflare or Tailscale, a way that failed or waits for a sign-in or approval,
a workers.dev token that stopped working). Below it, a `ConnectionTopology`
lane for each way that is set up or running: the left link says whether the
address was reached from outside, the right link whether it runs on the NAS.
Each lane lists its addresses (copy, open, QR) and opens its provider page; a
red lane names a failed check under it. A Tailscale or NetBird lane captions
its source with the number of online devices when the daemon reports one, and
shows nothing otherwise. While the Cloudflare lane is a temporary address,
its foot holds an attention-tone inline `Callout` "Changes when the NAS or
Syno Connect Center restarts" with "Switch to Free link", which opens the wizard on the
Free link card. One "Add a way" button follows; it opens the wizard.

DSM's own ways join the same topology, after Syno Connect Center's, once DSM
answers; they never make a first visit a returning one. QuickConnect, while
DSM has it on, is one lane from "Anyone with the ID" through a QuickConnect
node captioned with the ID; its NAS link says whether DSM is connected to
Synology. DDNS records turned on in DSM merge into one lane to a DDNS hub
(captioned with the one hostname, or the count), and a compact table under the
lane lists each hostname, provider, status and IP address. Both lanes list
their addresses like every other lane (Copy, Open, QR code), read from DSM and
never guessed: QuickConnect's `https://<ID>.<domain>` with the domain DSM's
`SYNO.Core.QuickConnect` get names, and each DDNS hostname on DSM's HTTPS port
(the external HTTPS port in `SYNO.Core.Web.DSM.External`, else DSM's own from
`SYNO.Core.Web.DSM` get v2, read only while a DDNS record exists). Without the
domain or the port the lane shows no address row at all. Nobody checks
either from outside, so the reach link stays "Not verified from outside". Both
are read in the administrator's DSM session (`SYNO.Core.QuickConnect` get v2
and status v1, `SYNO.Core.DDNS.Record` list v1) once per Home refresh, with no
timer; a refused or failed read draws nothing, and nothing is said while both
are off. Their lanes open Control Panel > External Access on the QuickConnect
or DDNS tab; outside a DSM window a toast names that place.

workers.dev has a lane of its own, after Cloudflare, once its Cloudflare
account is connected. Its status comes from `apps.workersstatus`, a read of the
NAS's own state file, when the Home shows and after each refresh; the Home
never calls `apps.list`, which scans the Apps. The relay node names the
account's `<subdomain>.workers.dev` but no address, since each App has its own,
and the lane opens the Cloudflare page's Free link tab. A token that stopped
working turns the NAS link to "New token needed"; the addresses already made
keep serving. A backend without the relay shows no lane.

Every page draws a way with the same picture: `WayPath` (one lane: who
connects, what carries it, the NAS or one App) and `WayStatus` (Off,
Connecting, On, Turning off, Needs attention, Failed), both from
`connect/wayPath.ts`.
Connecting flows along the links and holds still under prefers-reduced-motion;
the words are DOM labels, never text in the drawing.

### Phone

Address cards under the lanes have Copy, Open and QR code only; there is no
Phone button per address, since the phone page looks the same on every way.
Instead one collapsible **Use DSM on your phone** section sits under the topology (and under
the first visit's Local network lane), above "Compare all ways"
(`components/phone/PhoneSection.vue`). It holds:

- A two-sentence lead: this link puts all of DSM on the phone, working
  exactly as DSM does, only laid out to be easier to tap and read; scan the
  QR code or open the link on the phone to start (no "right of" or "below",
  since the code moves when narrow). Then two small groups of points, each
  with one icon, not repeating the lead: **DSM Apps, laid out for phones**
  (wide tables become lists and cards stack to scroll; sign in with the usual
  DSM account and permissions) and
  **Only on the phone page** (live CPU, memory, network and drives on its home
  page, paused when you leave; DSM notifications filtered by errors, warnings
  and info). Only what the vendored phone page build (`vendor/mobile-web`)
  ships is named.
- The panel (`components/phone/PhonePanel.vue`): on the left the larger phone
  (`PhoneFrame`, 220px wide, 200px narrow; a near-black bezel, leaning a little
  and rising in once, still under prefers-reduced-motion) with the live demo
  of the phone page and its tap hint (below); on the right a **Connect through** menu of every way that
  has an address, then that route's QR code, its full address wrapped with
  Copy, at most one line on what the route needs (`routeNeed` in
  `connect/phoneRoutes.ts`: the NetBird or Tailscale app signed in, same
  network only, a certificate warning, or a temporary address, in that order),
  and **Add it to your Home Screen**: iPhone and Android as equal groups,
  each a bold name over three icon steps (iPhone: open in Safari, Share, Add
  to Home Screen; Android: open in Chrome, the top-right menu, Add to Home
  screen or Install app, in Chrome's own words). Chrome's ⋮ is drawn as an
  icon inside the step's text (`{menu}` in the string), never as a
  character. The groups sit side by side when there is room and stack when
  narrow; both always show, an Android browser's own steps first. The result
  they share (full screen, its own icon) follows once. No Android App is
  mentioned. Narrow, the phone sits above the column.

The demo's tap hint (`PhoneFrame`, from the upright live demo) is a faint
accent dot and ring over the next thing to press, the Apps tab and then an
App, so the picture reads as interactive. It plays only while the phone is on
screen (an IntersectionObserver, at 90% of the phone so its tab bar shows;
a folded or scrolled-away section plays nothing), three soft plays with pauses, again whenever the phone comes back
into view, and retires for good in this browser after the first tap other
than the suggested one (`synopkgconnect.phoneTried`). None under
prefers-reduced-motion.

The menu lists Connect's running ways, then DSM's own QuickConnect and DDNS
addresses, then DSM's own address (the one this browser is on, else a LAN
address from `phone.addnas`): Cloudflare, workers.dev, IPv6, QuickConnect,
DDNS, NetBird, Tailscale, Local network. It starts on the first public HTTPS
route (Cloudflare first) and keeps the user's pick while that route lasts.
The section starts open; folding it is kept in localStorage
(`connect/phoneHome.ts`), after which it starts folded and opens only when
asked. The header's "Connect your phone" (and its phone overflow-menu
entry) opens the section and scrolls to it; there is no separate phone
dialog. Codes are drawn locally (`uqr`) and stay black on white in both
themes. `tests/phone-card-screens.mjs` captures them on mocked answers and
`tests/phone-qr-decode.py` decodes the PNGs.

### Setup wizard

`components/wizard/SetupWizard.vue` is the one place a way is set up. It is
always the ui `WizardDialog`, mounted once in `App.vue` and opened through
`openWizard(way?)` in `connect/state.ts`: from the Overview's "Set up", "Add a
way" and "Switch to Free link", and from App URLs' "Set up a way". It does the
whole job in four steps, with "Step n of N" and the step's name in the step
band (three steps for NetBird, IPv6 and the temporary address, which open no
Apps). The dialog fits the window: at 960×600, 1280×800 and 640×600 the band,
the footer and its one primary button stay in view and only the step body
scrolls; the page itself never scrolls for it.

1. Choose how: every way is a card, shown at once with nothing to unfold:
   Free link (workers.dev, preselected, a star with "Recommended"), My own
   domain and Only my devices (Tailscale), then NetBird and IPv6. Three sit
   side by side from the default window, the last two on a second row (one
   column under 615px of step width, where each card puts its path beside
   its words). The cards are one radio group (`WizardWay`): a click anywhere
   on a card or the arrow keys (which cycle through all five) select it (a
   primary border and a filled mark on the title row), and the wizard
   footer's one primary button goes on with it ("Use Free link", or
   "Continue with …" for a way already on). At 960×600, 1280×800 and
   640×600 the preselected card and that button show whole without
   scrolling, in every language; a second-row card selected by click or by
   the keys is brought into view by the step body's scroll while the footer
   stays put (`tests/wizard-screens.mjs` asserts both). Each card has
   its `WayPath` (one row of nodes, two lines under each), the way's one line
   (from 320px of card width; narrower, as in the dialog, it is the path's
   tooltip and the card's description) and the same five facts (cost, who can open it,
   install, video, safety), each one line: the fact's icon (its name as
   tooltip), a `StatusDot` (good, watch out or risk) and words that wrap. The
   badge sits on the title row and drops to its icon when the row is short;
   every title row, across both rows, takes the tallest one's height, so the paths
   and the text below them line up. Under the cards, one quiet row
   (not a card: no star, no colour) reads "Just trying it? Get a temporary
   address" with a secondary "Get one"; it shows while Cloudflare is free for
   it (nothing set up there, or a temporary address already), one scroll
   down of the step body in the default window. It opens the
   temporary address's Connect step. IPv6 selected (and not yet on) shows
   its risk checkbox right under the cards, and the footer button waits for
   it. "Compare all ways"
   opens the folded comparison. A way already on says "Already set up" and
   continues to its Apps. Nothing is written in this step.
2. Connect: the way's own connect form, the same component its page shows
   while off (`FreeLink` for the Free link, `CONNECT_FORMS` otherwise), with
   Back beside its one primary button. The temporary address has no form: its
   step is `TemporaryFacts` (an example address whose arrow turns once, still
   under reduced motion, then "Changes when the NAS or Syno Connect Center restarts" and
   "Anyone with the link reaches the DSM sign-in") and the footer's Start,
   which starts a quick tunnel (`connection.connect`, mode `quick`).
3. Choose what opens with it: the Apps as checkboxes (video Apps never start
   ticked on the Free link), DSM on top, and on a public way DSM's own
   protections read in the background. DSM goes with the first App, or alone
   when no App is ticked; Continue reads every target once and shows one
   `AppApproval` for all of them.
4. Ready: each address (DSM first) with copy, open and QR, then "Test it
   yourself": steps on a phone off the home Wi-Fi. IPv6 first checks the phone
   has IPv6 (test-ipv6.com) and offers an outside check (KeyCDN's IPv6 ping);
   Tailscale and NetBird first install their app. Syno Connect Center only opens these
   sites in a new tab and never calls them. A temporary address adds an
   inline note "For an address that stays the same, use Free link" whose
   button returns to the cards with the Free link selected.

Where it opens comes from the backend alone (`connect/wizard.ts`): a way that
waits for a sign-in or a workers.dev name resumes at Connect, anything else
starts at Choose. When the way comes on, Syno Connect Center moves to the next step by
itself, so a sign-in in another tab or a reload continues where it stopped.
`tests/wizard-screens.mjs` drives the dialog from the Overview and from App
URLs on mocked answers, captures each step, and asserts the fit above in every
language, the temporary row included.

The comparison table lists every option of
`backend/internal/advisor/catalog.json` (DSM's own QuickConnect included) with
plain-language security: who can connect, where data passes, whether it is
public and what to pair it with. A method without protection (direct IPv6)
shows "No protection" in its Protection column, which comes from the catalog's
`protection` field (`cloudflare`, `cloudflare-access`, `private-network`,
`synology-relay`, `none`). That file is the one source for the table, the
steps, costs, installs and risks, and ctl `recommend`.

On a public way the wizard's Apps step reads DSM Auto Block and enforced
2-factor sign-in in the background with the user's own DSM session
(`ProtectionChecks`): one that is off says so with DSM's official help, one
DSM does not let the user read says it cannot be checked, and one that is on
needs nothing. Nothing is enabled automatically.

The user can leave everything off, use one method or combine methods.

## Information architecture

The sidebar keeps seven pages: Overview, App URLs, Cloudflare, Tailscale, NetBird, IPv6
direct and Settings. Each page answers one question first and keeps the rest
one click away:

| Page | First question | Holds |
| --- | --- | --- |
| Overview | What runs, and does anything need me? | first visit: a picture and Set up (the wizard); returning: Needs attention, lanes (workers.dev included), Add a way (the wizard); the comparison (closed) |
| Cloudflare, Tailscale, NetBird, IPv6 | Is it on, and what is its address? | a Connection tab, then the page's tabs |
| App URLs | Which apps open outside, and at what address? | one row per app with its ways; the App dialog; a picture and Set up a way (the wizard) while nothing is set up |
| Settings | Are the connectors current? | Connectors (one card per connector, help links) and GitHub tabs |

Text rules, on every page:

- A status is one tag and at most one line; reasons and limits go to an
  InfoTip or the provider's documentation.
- One primary button per page view, for the step that unblocks the user (start
  or sign in); everything else is secondary.
- A section has a title and at most one line of description; a longer
  explanation moves to an InfoTip, a field's `help` tooltip or a closed
  section.
- Notices follow ui `docs/callout.md`, never Syno Connect Center's own alert styles:
  - a passing result (the change was applied, DSM External Access was
    updated, a start is still running) is a `Toast` and leaves nothing behind;
  - something the user must handle (sign in, approve the NAS, an unreachable
    control server, HTTPS off, a failed connection, DSM External Access not
    set) is a `Callout` with a one-line title, at
    most one line below it, one `size="sm"` action in `#actions` and a
    machine code in `code`;
  - a risk or a limit (a token tunnel, a public Funnel, opened ports, Funnel's three services, follow limits) is
    `Callout variant="inline"`: one sentence, with "Learn more" in `#actions`
    opening the provider's documentation;
  - Callouts span their container by default; do not set `fill` or add a
    local width override.
- One word per state, the same on every page and lane: Off, Connecting, On,
  Turning off, Needs attention (sign-in, approval), Failed. The action is
  Turn off. Disconnect is kept for removing an
  account's sign-in (Cloudflare, workers.dev).
- The same thing looks the same everywhere: state `Tag`, address `UrlCard`,
  connection check row, consent `Checkbox` right above the button it unlocks.
- A form row without a label that belongs to the field above it - the exposure
  consent, a name conflict, an error a start came back with - starts on the
  value column with the field, the button (`FormActions inset`) and the note
  (`FormNote inset`): give the row `FormItem inset`, which steps back when the
  form stacks its labels. Never leave it under the label column.
- A way the user turned off is off, not broken: the shared off word and the
  path's "Turned off" link, never a danger colour. Only a way that should be
  up shows a failure (its own `failed` state, or a dependency's tunnel hop
  through `dependencyPathLinks`).

### Provider page skeleton

Every tabbed page is a DSM settings page: the `SectionTabs` strip on top, one
`SettingsPage` per tab whose body alone scrolls, and a tab's Reset and Apply
pinned bottom right in its footer. A tab that is a list is the `DataGrid`
itself, filling the tab. The strip sits in `PageContent height="fill"` with
the panels below it, because the kit's `SectionTabs fill` mounts only the
open panel.

```
[Connection tab]       state tag: Off · Connecting · On · Turning off · Needs attention · Failed
  path picture: who connects → the way → NAS (the shared WayPath)
  address (copy, open, QR)
  Reach   Reachable from outside · checked 10:42 · Check again
  one Callout when the user must act (sign in, approve)
  footer: the page's own action · Turn off
  Off or failed: the provider's connect form
  Learn more ↗
[ <page tabs, most used first> ]  once connected, or while off when they are the connect forms
[ Advanced ]                      Reset · Apply in its footer
```

The state tag says the shared word from `connect/wayPath.ts`, or Status
unavailable; the raw state stays in `data-connection-state`. The path picture is
`WayPath` with the links of the Overview's lane for the provider (idle before
set up, moving while connecting); its labels are DOM
text and ui stops the flow under `prefers-reduced-motion`. While the provider
is off or failed the Connection tab holds its connect form, the same component
the setup wizard embeds (`docs/connect-form.md`), with the page's only primary
button; the page's own tabs wait for the connection, so no tab is ever full of
disabled fields. Once connected, the page's own tabs follow Connection, the
most used first, then Advanced. A page whose tabs are its connect forms
(Cloudflare, NetBird) keeps them while off and opens on the first.
Tabs are navigation only and every panel stays mounted, shown with `v-show`:
a half-typed name or a ticked port survive a tab switch, and
consent and keys still clear on leaving the page. While connecting, signing
in or turning off, only Connection shows, and the selected tab returns after. There is no Help tab: a field
explains itself with an InfoTip, a risk's "Learn more" and the link at the
foot of the page open the provider's documentation.

| Page | While off | Tabs while connected |
| --- | --- | --- |
| Cloudflare | the same tabs | Own domain, Free link, Temporary, Apps |
| Tailscale | Sign in to Tailscale | Services, Apps, Advanced |
| NetBird | Sign in with SSO | Apps |
| IPv6 direct | risk, services, consent, Connect; Checklist folded | Services, Checklist |

Login pages open in a tab reserved on the click; if the browser blocks it, a
button, the URL and a QR replace it. A missing connector is downloaded first,
with progress shown in place. Only public exposure needs a consent checkbox.
Buttons follow the UI button policy (docs/button-policy.md in the UI repo):
default size, secondary tool buttons in sections and card footers, a capsule
only in a `SettingsPage` footer (Reset, then Apply), a section's `FormActions`
or a dialog footer, with Cancel before the one primary.
Syno Connect Center adds one rule on every provider page, following DSM and the UI kit's
button policy: Turn off, Stop, Disconnect and Pause are grey (`secondary`)
tool buttons at the start of the way's action row (the status card footer,
or the end of a Cloudflare tab), never under an empty label; only the button
of the dialog that confirms them is red (`danger`). Sign in, Connect and
Reconnect are the section's one blue tool button.
A provider error puts its machine code in `Callout`'s `code`.

- Cloudflare
  - Upload limit: the own domain and Free link refuse an upload over 100 MB
    per file (Free and Pro plans) with Cloudflare's own error 413, before the
    NAS sees it. The temporary address has no size limit, but one upload must
    finish within 100 seconds, so a large file on a slow line fails (at about
    5 MB/s, 300 MB does). Downloads have no limit on any way. In a web
    browser DSM's pages send large files in pieces on every Cloudflare way
    (docs/large-uploads.md), so the limit holds only for other apps (desktop
    and mobile apps, WebDAV). One line (`UploadLimit`, from the catalog's
    `upload-100mb` or `upload-100s`) says so with a leading InfoTip (browser
    uploads in pieces, iPhone and iPad included; why other apps keep the
    limit, the paid plans' 200 MB and 500 MB or the 100-second cut, and the
    private network or LAN for large files there), an upload icon with
    "Uploads: large files work in a web browser; other apps up to 100 MB per
    file (Cloudflare Free/Pro)" or "Uploads: large files work in a web
    browser; in other apps each must finish within 100 seconds", and a
    download icon with "Downloads: no limit". The "·" ends the
    upload half, so a narrow screen wraps after it, never before. It is
    permanent, never a dismissible Callout: a File size row on the Connection
    tab (the temporary address's line while it runs, beside the Free link's
    when that is on too; otherwise the 100 MB line) and in the Own domain and
    Free link layout (on or off), and under the temporary address's facts
    (the wizard's temporary step too). A public or Free link App approval
    lists the same words among its notes.
  - Off: one `RadioGroup` (own domain, temporary address; the own domain is
    chosen when one is saved) and the chosen start. The own domain is the
    Cloudflare connect form: a saved name reconnects after the consent, "Use
    another name" or a new name asks only for the name and signs in to pick
    its domain.
  - Temporary: the tab after Free link shows the wizard's `TemporaryFacts`
    and one secondary "Get a temporary address" (the wizard's
    `startTemporary`, no checkbox: the facts are the consent). Running, it
    shows the address in a `UrlCard` and Stop; while an own domain or token
    runs it says in one line to turn that off first. Running, the Own domain
    tab also says "Changes when the NAS or Syno Connect Center restarts" and offers an
    own domain.
  - Address (connected): a running own domain offers "Change address", which signs in again with the name prefilled. While an own
    domain or token address runs and DSM's External Access address is unset
    or different, a `Callout` titled "Set DSM External Access" offers "Apply"
    (HTTPS port 443); the manual Control Panel path sits under "Do it
    yourself". It never overwrites unasked and says nothing once DSM matches;
    applying is confirmed by a toast. A temporary address is never offered.
  - Own domain, last: one muted line "Drive Client, file sharing or SSH?"
    with a ghost "Use Only my devices" that opens the setup wizard on
    Tailscale. Cloudflare carries web pages only. While Cloudflare is off, the
    tunnel token (with its one-sentence risk) folds under one "Tunnel token"
    line below the connect form.
- Tailscale: off, one "Sign in to Tailscale" opens DSM (or the use's
  service); the auth key and a Headscale server are folded. Connected,
  Services holds the services to share and Apply; Advanced holds manual
  mappings, Funnel (its risk and consent show only when it is on), the key
  and server, and Apply. Both apply in place. Apps lists the Apps page's rows
  and App dialog (`AppList` with `way`), showing only the Tailscale way.
- NetBird: off, one SSO button; the setup key, a self-hosted management
  server and login origins are folded. Connected, the status card and one
  Apps tab, the Apps page's rows and dialog showing only the NetBird way;
  access is governed by NetBird policies.
- IPv6 direct: off, its one-sentence risk (nothing protects the opened
  ports), the services it opens (the list folded), the consent and the
  button, with the Checklist folded below. Connected, Services holds the
  service picker, Apply behind the consent once it differs, and the shared
  catalogue; Checklist holds one `ListItem` per check (address, firewall,
  router, DDNS, external reach) with its state badge and the DSM setting to
  open. A ready connection says "Ready (unverified)" under its On tag until
  an external check passes.

### App URLs

One page without tabs lists the Package Land apps Syno Connect Center found (`apps.list`),
read only when the page opens or Refresh is pressed. Account controls are not
here: the Cloudflare sign-in and the free link's token live on the Cloudflare
page, and a broken one shows one `Callout` that opens it. DSM certificate
reviews live in the Home's Needs attention.

- Nothing set up (no way ready, no provider connected, no App with a way): the
  page shows the shared connection picture (your devices and the NAS, no way
  between), one title, the App names, and "Set up a way", which opens the
  setup wizard. No table of disabled rows.
- Otherwise one `DataGrid` row per App: its main-menu icon (from its
  ui/config) and name with at most one line for what needs the user; "Opens
  with" chips, one per way (Free link, Own domain, Tailscale, NetBird), each
  carrying its status when it is not simply on ("Tailscale · Paused");
  the address with Copy; and a chevron that opens the App dialog. The address
  is the widest column; the user drags a header edge (or presses the arrows
  on it) to widen a column, and the grid's `state-key` keeps the widths after
  a reload. The pick box and the chevron stay narrow and fixed. The
  Cloudflare page's Apps tab is the same kind of grid. A
  single-origin App shows its private address only, its one sign-in address.
- The App dialog: "Only my devices" holds the Tailscale switch (with Turn on
  again for a paused way and the reason) and the NetBird address with its
  certificate note. "Public" is one `RadioGroup`: Off, Free link
  (workers.dev) or My own domain, each described by its address or "Not set
  up"; an App has at most one public way, so switching turns the old one off
  first. A way that is not set up offers "Set up": a private one opens the
  Tailscale page, a public one the Cloudflare page (the free link its tab).
  Details (port, protection, the app's settings) stay closed. Save turns off
  what goes after a confirmation of what is removed (the DNS record stays),
  then shows the approval of each way that comes inside the same dialog. A
  removed App offers Clear.
- Several Apps can be made public together: rows that can take a ready public
  way get a checkbox, and "Make public with the free link" or "Make public on
  my own domain" opens one approval for all of them (one cloudflared restart,
  one batch of Workers). A row whose DNS record or Worker failed says so.
- Statuses: Off, On, Waiting (unreachable right now, resumes on its own),
  Paused with the reason the backend stored (port or protection changed,
  certificate mismatch, app route mismatch, port taken by another app, DSM
  port or private address changed, route failed, turning on failed; any other
  reason shows as a plain Paused), Removed, Needs cleanup and Needs update.
- Turning on reads the target first (`apps.inspect`); an app that is not
  ready gets a toast, never an approval. The approval shows each app's name,
  port, protection and certificate: names, issuer and expiry (W) or the
  SHA-256 fingerprint (S), the values approved before when they changed, and
  DSM's certificate when the first private app also routes DSM. Below it: the
  comparison hint with DSM Control Panel > Security > Certificate, the
  first-use disclosure, that Syno Connect Center forwards only to the approved app name
  and port without guaranteeing the content comes only from that app, that
  other addresses on the same port go out too, and the private-key warning. A
  public approval adds the editable hostname, the DSM sign-in line, the
  provider's media limit, Cloudflare's upload limit in the Cloudflare page's
  words and that Cloudflare sees the traffic. A
  free link is named after the App (`jellyfin.<name>.workers.dev`), with a
  short suffix only when that name is taken.

`tests/app-urls-screens.mjs` captures each state from mocked answers.

### Settings

Two tabs. Connectors has one card per connector: the version in use as a tag, the latest version, the
one action that applies (Download, Update to, or nothing) beside Check and a
named "Roll back to"; verification details stay closed. A card that is busy
does not block the others. Help lists each provider's documentation. GitHub
holds the optional token, with Reset and Apply in its footer.
The shared service catalogue is described in one line: enabling an entry makes
it selectable, each connection keeps its own scope, and disabling an entry
withdraws it from every connection.

Use SDK `ConnectionTopology`, `Card`, `CardGrid`, `SettingsSection`, `ListItem`,
`Callout`, `Toast`, `SectionTabs`, `SettingsPage`, `FormNote`, `InfoTip`, `Tag`, `Badge`, `KeyValueRow`, `Form`, `FormItem`, `Input`, `Select`,
`Checkbox`, `Switch`, `RepeatRows`, `FieldCell`, `CopyCutField`, `Link`,
`DataGrid` and `DataTable` with `CellText` and `CellStatus`, `Dialog`, `Space` and
`formatDuration`, `formatFileSize`. Labels share the theme's label column; no section passes its
own `labelWidth`. Application styles are limited to host sizing,
scroll containment, icon sizing and ordered-list markers, using theme tokens.

Admin-only panels never mount for ordinary users. Transient credentials and
exposure consent clear when leaving a page; unmount ignores in-flight results.
Status loading and mutation errors retain the other connections and provide
refresh/repair access. A local running tunnel is not proof of external access.
Local browser fixtures never establish DSM, external-network or mobile acceptance.
