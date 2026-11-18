# Syno Connect Center forms

One connect form per provider. The provider page shows it while the
provider is off or failed; the setup wizard's Connect step embeds the same
component. Code: `frontend/src/components/connect-forms/`.

```ts
import {CONNECT_FORMS, type ConnectFormProvider} from './components/connect-forms'
// or one of: CloudflareConnectForm, TailscaleConnectForm, NetBirdConnectForm, Ipv6ConnectForm
```

```vue
<component :is="CONNECT_FORMS[way]" :service-ids="ids" @submitted="busy=false" @connected="next()">
  <template #actions><Button @click="back">Back</Button></template>
</component>
```

## Contract

| | |
| --- | --- |
| Mount | Anywhere under `provideConnectState` (the Syno Connect Center app root). The form reads and writes the shared state, so the provider page's status card and the wizard see the same start. |
| `serviceIds?: string[]` | IPv6 only: the port-list IDs to open. Omitted, IPv6 opens DSM's web service (HTTPS when DSM serves it). |
| `submitted` | The form's start returned (`connection.connect` answered or failed). Errors show where they always do: the status card and the app's error toast. |
| `connected` | The provider reached `connected` after this form's start. A start that ends `disconnected` emits nothing. |
| `#actions` | Secondary buttons placed before the form's one primary button, e.g. the wizard's Back. |
| `footer?: boolean` | The setup wizard sets it: the form hides its primary button and exposes `primary` (`{label, disabled, loading, run()}`, `FormPrimary` in `connect/connectForm.ts`); the wizard draws it as the footer's main action beside Back and stays busy until `run()` resolves. Provider pages leave it off and keep the inline button. The Free link form (`FreeLink`) follows the same contract. |

Each form has exactly one primary button and nothing else that writes:

| Form | Primary | Folded under "Advanced settings" | Consent |
| --- | --- | --- | --- |
| Cloudflare (My own domain) | Sign in to Cloudflare (new name) or Reconnect own domain (saved name; "Use another name" switches) | none | DSM sign-in becomes public |
| Tailscale | Sign in to Tailscale | auth key, Headscale server, login origin | none |
| NetBird | Sign in with SSO (Connect with setup key once a key is typed) | setup key, management server, login origins | management-policy dialog for a key on a self-hosted server |
| IPv6 | none (diagnostics only; the wizard footer shows Back alone) | none | none |

Not in a form: workers.dev (Free link) and the Cloudflare temporary address
belong to the Cloudflare page and the setup wizard (the temporary address
starts from the wizard's row under its cards or the Cloudflare page's
Temporary tab, both through `startTemporary`).

The consent (and a name conflict or the error a start came back with) is a
label-less row that belongs to the field above it: `FormItem inset` starts it
on the value column, level with the field, the button (`FormActions inset`) and
the note (`FormNote inset`), and back at the form's edge when a narrow window
stacks the labels. The rule is the UI kit's (`@synopackageland/ui` 0.19.6), not
this app's.

## Provider page skeleton

`ProviderLayout` (`frontend/src/components/ProviderLayout.vue`):

```
[Status card]  state tag: Off · Connecting · On · Needs attention · Failed
  path picture  who connects → the way → NAS (the shared WayPath)
  address (copy, open, QR) · reach check · one closable callout when the user must act (controls stay outside it)
  footer: page actions · Turn off
Off or failed:  slot connect (the connect form), slot advanced folded below
Connected:      tabs = page tabs (most used first) + Advanced last
Learn more ↗    the provider's documentation
```

Slots: `status`, `actions`, `connect`, `tab-<value>` (with the `tabs` prop),
`advanced`. While connecting, signing in or turning off, only the status card
shows. There is no Help tab: a field explains itself with an InfoTip, a risk
with `RiskNote` (its "Learn more" opens the page's documentation link).

| Page | Connected tabs |
| --- | --- |
| Cloudflare | Own domain, Free link, Temporary, Apps (always shown; no Advanced) |
| Tailscale | Apps (Join while off, as on NetBird; see [Tailscale](tailscale.md)) |
| NetBird | none: access is the NetBird policy |
| IPv6 direct | Services (apply in place), Checklist |
