// One connect form per provider, shared by the provider pages (while the
// provider is off) and the setup wizard's Connect step. API: docs/connect-form.md.
import type {Component} from 'vue'
import CloudflareConnectForm from './CloudflareConnectForm.vue'
import Ipv6ConnectForm from './Ipv6ConnectForm.vue'
import NetBirdConnectForm from './NetBirdConnectForm.vue'
import TailscaleConnectForm from './TailscaleConnectForm.vue'

export {CloudflareConnectForm,Ipv6ConnectForm,NetBirdConnectForm,TailscaleConnectForm}
export type ConnectFormProvider='cloudflare'|'tailscale'|'netbird'|'ipv6'
export const CONNECT_FORMS:Record<ConnectFormProvider,Component>={cloudflare:CloudflareConnectForm,tailscale:TailscaleConnectForm,netbird:NetBirdConnectForm,ipv6:Ipv6ConnectForm}
