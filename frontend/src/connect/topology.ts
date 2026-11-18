// The Overview's lanes: one per connection method that is set up or running,
// and one for the workers.dev relay once its Cloudflare account is connected.
// The left link is the reach from outside, the right link is the NAS side.
// Labels are message keys; the page translates them.
import type {LinkState} from '@synopackageland/ui'
import type {ConnectionStatus} from '../context'
import {addressesOf} from './addresses'
import {settling,type CheckResult} from './checks'

export const METHODS=['cloudflare','tailscale','netbird','ipv6'] as const
export type Method=typeof METHODS[number]
export interface LinkSpec {state:LinkState;label:string;icon?:'key'}
export interface LaneSpec {provider:string;links:[LinkSpec,LinkSpec];addresses:string[];peersOnline?:number}

const unknown:LinkSpec={state:'unknown',label:'setup.unknown'}
// Who can reach a running method when nothing outside verified it.
const unverified:Record<string,string>={cloudflare:'overview.reach.notChecked',workers:'overview.reach.notChecked',ipv6:'overview.reach.unverified'}

// A settling connection's reach flows as pending, never red, until its check passes or the window ends.
function connectedLinks(provider:string,check:CheckResult|undefined):[LinkSpec,LinkSpec] {
 const running:LinkSpec={state:'active',label:'overview.nas.running'}
 if(settling(check))return [{state:'pending',label:'overview.reach.settingUp'},running]
 if(check?.state==='ok'&&check.externalVerified)return [{state:'active',label:'overview.reach.external'},running]
 if((provider==='cloudflare'||provider==='workers')&&check?.state==='failed')return [{state:'error',label:'overview.reach.unreachable'},running]
 const reach:LinkSpec={state:'unknown',label:unverified[provider]??'overview.reach.private'}
 return [reach,check?.state==='failed'?{state:'error',label:'overview.nas.checkFailed'}:running]
}

function nasSide(state:ConnectionStatus['state']):LinkSpec {
 switch(state){
  case 'connecting':case 'disconnecting':case 'stopping':return {state:'pending',label:`ux.state.${state}`}
  case 'needs-login':return {state:'attention',label:'ux.state.needs-login',icon:'key'}
  case 'needs-machine-auth':return {state:'attention',label:'overview.nas.approval'}
  case 'failed':return {state:'error',label:'ux.state.failed'}
  default:return unknown
 }
}

// Saved addresses and identities do not make a turned-off way active.
const setUp=(s:ConnectionStatus)=>s.state!=='disconnected'

export function laneFor(provider:string,status:ConnectionStatus|undefined,check:CheckResult|undefined,unavailable:boolean):LaneSpec|undefined {
 if(!status||!setUp(status))return undefined
 // An address the daemon cannot vouch for right now is not offered.
 if(unavailable||status.state==='unknown')return {provider,links:[unknown,unknown],addresses:[]}
 const addresses=addressesOf(status)
 if(status.state==='connected')return {provider,links:connectedLinks(provider,check),addresses,peersOnline:status.peersOnline}
 return {provider,links:[{state:'idle',label:'overview.reach.waiting'},nasSide(status.state)],addresses}
}

export function overviewLanes(connections:Record<string,ConnectionStatus>,checks:Record<string,CheckResult|undefined>,unavailable:boolean):LaneSpec[] {
 return METHODS.flatMap(id=>laneFor(id,connections[id],checks[id],unavailable)??[])
}

// workers.dev uses the same check as the Cloudflare page, with DSM's address on its row.
export const RELAY='workers'
export interface RelayStatus {dsm?:{provider:string;url:string};usage?:{public:boolean;relay:boolean};connected:boolean;relayState?:'connected'|'connecting'|'reconnecting'|'disconnected';invalid?:boolean;subdomain?:string;code?:string}
export const relayAvailable=(r:RelayStatus|undefined)=>!!r&&r.code!=='workers_unavailable'
export function relayLane(r:RelayStatus|undefined,unavailable:boolean,check?:CheckResult):LaneSpec|undefined {
 if(!r?.connected&&(!r?.invalid||r.usage?.relay===false))return undefined
 if(unavailable)return {provider:RELAY,links:[unknown,unknown],addresses:[]}
 // Addresses already made keep serving when the token stops working.
 const nas:LinkSpec=r.invalid?{state:'attention',label:'overview.relay.invalid',icon:'key'}:{state:'active',label:'overview.nas.running'}
 const links=connectedLinks(RELAY,check)
 if(r.relayState!=='connected'){
  const waiting=r.relayState==='connecting'
  links[0]={state:waiting?'pending':'error',label:waiting?'overview.reach.settingUp':'overview.reach.unreachable'}
  links[1]={state:'pending',label:waiting?'ux.state.connecting':'overview.relay.reconnecting'}
 }
 return {provider:RELAY,links:[links[0],r.invalid?nas:links[1]],addresses:r.dsm?.provider==='workers'?[r.dsm.url]:[]}
}
