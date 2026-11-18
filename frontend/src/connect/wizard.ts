// The setup wizard on the Overview: choose a way, connect it, choose the
// Apps it opens, then get the addresses and test them. Where it starts comes
// only from what the backend reports (connection.status and
// apps.workersstatus), never from the browser, so it resumes after a sign-in
// tab or a reload. Nothing is written before the user selects a way's own
// connect button.
import type {ConnectionStatus} from '../context'
import type {ConnectFormProvider} from '../components/connect-forms'
import type {Scope} from './appUrls'
import type {CheckResult} from './checks'
import type {RelayStatus} from './topology'
import type {PathWay,WayPhase} from './wayPath'

export type WizardWay='workers'|'cloudflare-domain'|'tailscale'|'netbird'|'ipv6'|'temporary'
/** A way with a card on Choose; the temporary address has a quiet row instead. */
export type CardWay=Exclude<WizardWay,'temporary'>
export type WizardStep='choose'|'connect'|'apps'|'done'
export const STEPS:readonly WizardStep[]=['choose','connect','apps','done']
// The three ways most users need; the others wait behind "More ways".
export const MAIN_WAYS=['workers','cloudflare-domain','tailscale'] as const
export const MORE_WAYS=['netbird','ipv6'] as const

// The connect form a way uses; the Free link has its own, on the Cloudflare page.
export const FORM:Record<Exclude<CardWay,'workers'>,ConnectFormProvider>={'cloudflare-domain':'cloudflare',tailscale:'tailscale',netbird:'netbird',ipv6:'ipv6'}
// The App URL scope a way opens Apps on; NetBird, IPv6 and the temporary
// address open none.
export const APP_SCOPE:Partial<Record<WizardWay,Scope>>={workers:'relay','cloudflare-domain':'public',tailscale:'private'}

// The same five facts on every card, each a message key and a tone.
export type Fact='cost'|'who'|'install'|'video'|'safety'
export const FACTS:readonly Fact[]=['cost','who','install','video','safety']
export type Tone='success'|'warning'|'danger'|'neutral'
export const FACT_TONES:Record<CardWay,Record<Fact,Tone>>={
 workers:{cost:'success',who:'warning',install:'success',video:'danger',safety:'success'},
 'cloudflare-domain':{cost:'warning',who:'warning',install:'success',video:'warning',safety:'success'},
 tailscale:{cost:'success',who:'success',install:'warning',video:'success',safety:'success'},
 netbird:{cost:'success',who:'success',install:'warning',video:'success',safety:'success'},
 ipv6:{cost:'success',who:'danger',install:'success',video:'success',safety:'danger'},
}

export type WayState='off'|'started'|'on'
const started=new Set<ConnectionStatus['state']>(['connecting','needs-login','needs-machine-auth'])

// Whether a way is off, started and waiting (a sign-in, a name), or on.
export function wayState(way:WizardWay,connections:Record<string,ConnectionStatus>,relay:RelayStatus|undefined):WayState {
 if(way==='workers'){
  if(relay?.connected&&!relay.invalid)return 'on'
  return relay?.code==='workers_subdomain_required'?'started':'off'
 }
 const c=connections[providerOf(way)]
 // Cloudflare's temporary address and own domain are two ways of one connection.
 if(way==='cloudflare-domain'&&c?.mode==='quick')return 'off'
 if(way==='temporary'&&c?.mode!=='quick')return 'off'
 if(c?.state==='connected')return 'on'
 return c&&started.has(c.state)?'started':'off'
}

// The connection a way runs on.
export const providerOf=(way:Exclude<WizardWay,'workers'>)=>way==='cloudflare-domain'||way==='temporary'?'cloudflare':way
// The temporary address replaces whatever Cloudflare runs, so it is offered
// only while Cloudflare is off, failed or already temporary.
export function temporaryAvailable(connections:Record<string,ConnectionStatus>):boolean {
 const c=connections.cloudflare
 return !c||c.mode==='quick'||c.state==='disconnected'||c.state==='failed'
}
// Starts the temporary address: the wizard's Start and the Cloudflare page's
// Temporary tab. Its one consent is the facts shown beside the button.
export const startTemporary=(s:{connectProvider(params:Record<string,unknown>):Promise<void>})=>s.connectProvider({provider:'cloudflare',mode:'quick',confirmExposure:true})
// The shared path a way draws.
export const pathWay=(way:WizardWay):PathWay=>way==='temporary'?'cloudflare-quick':way

// The shared path's phase: a started way flows while it waits.
export const PHASE:Record<WayState,WayPhase>={off:'off',started:'connecting',on:'on'}

// Connect moves on by itself once the way works: the Free link when it is on,
// any other way when its connection check passes. A new connection reads as
// setting up until then; a check that failed after the settling window
// leaves the choice to the user.
export function connectReady(way:WizardWay,state:WayState,check:CheckResult|undefined):boolean {
 return state==='on'&&(way==='workers'||check?.state==='ok')
}
export const connectFailed=(way:WizardWay,state:WayState,check:CheckResult|undefined)=>state==='on'&&way!=='workers'&&check?.state==='failed'
// The hero's phase: a way that runs but has not passed its check still flows.
export function connectPhase(way:WizardWay,state:WayState,check:CheckResult|undefined):WayPhase {
 if(state!=='on'||way==='workers'||check?.state==='ok')return PHASE[state]
 return check?.state==='failed'?'error':'connecting'
}

export const stepAfterConnect=(way:WizardWay):WizardStep=>APP_SCOPE[way]?'apps':'done'
export const stepFor=(way:WizardWay,state:WayState):WizardStep=>state==='on'?stepAfterConnect(way):'connect'

// Where the wizard opens: a way already started resumes at Connect;
// otherwise it starts by choosing, with Free link selected.
export function resume(states:Record<WizardWay,WayState>):{way:WizardWay;step:WizardStep} {
 const way=([...MAIN_WAYS,...MORE_WAYS,'temporary'] as WizardWay[]).find(w=>states[w]==='started')
 return way?{way,step:'connect'}:{way:'workers',step:'choose'}
}

// "Test it yourself": steps the user takes on a phone. IPv6 first checks
// that the phone has IPv6 and can also be checked from outside; Connect
// itself never calls these sites, it only opens them in a new tab.
export const TEST_SITES={phoneIpv6:'https://test-ipv6.com/',outsideIpv6:'https://tools.keycdn.com/ipv6-ping'} as const
export type SelfTestStep='phone-ipv6'|'phone-app'|'open'|'outside'
export function selfTestSteps(way:WizardWay):SelfTestStep[] {
 if(way==='ipv6')return ['phone-ipv6','open','outside']
 if(way==='tailscale'||way==='netbird')return ['phone-app','open']
 return ['open']
}
