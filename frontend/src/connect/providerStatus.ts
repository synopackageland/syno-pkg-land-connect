// What a provider page's status card shows: the connection's word in the
// shared vocabulary (wayPath's Off, Connecting, On, Turning off, Needs
// attention, Failed, plus an unreadable status), and the links of its path.
// Labels are message keys; the card translates them.
import type {ConnectionStatus} from '../context'
import {settling,type CheckResult} from './checks'
import {laneFor} from './topology'
import {PHASE_TAG,type PathWay} from './wayPath'
import type {LinkState} from '@synopackageland/ui'

export type StatusWord='off'|'connecting'|'turning-off'|'on'|'attention'|'error'|'unknown'
export const STATUS_TAG:Record<StatusWord,{label:string;tone:'neutral'|'info'|'success'|'warning'|'danger'}>={
 ...PHASE_TAG,
 unknown:{label:'skel.state.unknown',tone:'warning'},
}

// A change still running shows as connecting; one that failed needs the user.
// A change that was only refused (an older daemon kept it as the status)
// changed nothing: the request said so, the connection is as it was.
const moving=['applying','repairing','rolling-back','recovering'],settled=['committed','canceled']
export const refusedOnly=(apply:{phase:string;code?:string}|undefined)=>apply?.phase==='precheck-failed'||apply?.phase==='rolled-back'&&apply.code==='apply_unavailable'
export function statusWord(status:ConnectionStatus|undefined,unavailable:boolean,check?:CheckResult):StatusWord {
 if(unavailable||status?.state==='unknown')return 'unknown'
 switch(status?.state??'disconnected'){
  case 'connected':{
   if(settling(check))return 'connecting'
   const phase=refusedOnly(status?.apply)?'committed':status?.apply?.phase??'committed'
   return moving.includes(phase)?'connecting':settled.includes(phase)?'on':'attention'
  }
  case 'waiting':case 'backoff':case 'connecting':return 'connecting'
  case 'disconnecting':case 'stopping':return 'turning-off'
  case 'needs-login':case 'needs-machine-auth':return 'attention'
  case 'failed':return 'error'
  default:return 'off'
 }
}

/** The way a provider's path draws: Cloudflare's temporary address is its own way. */
export function pathWay(provider:string,status:ConnectionStatus|undefined):PathWay {
 if(provider==='cloudflare')return status?.mode==='quick'?'cloudflare-quick':'cloudflare-domain'
 return provider as PathWay
}

/** The path's links: the Overview lane once running (its reach check included), moving while connecting, idle before set up. */
export function pathLinks(provider:string,status:ConnectionStatus|undefined,check:CheckResult|undefined,unavailable:boolean):[LinkState,LinkState] {
 if(status&&['connecting','waiting','backoff'].includes(status.state))return ['pending','pending']
 const lane=laneFor(provider,status,check,unavailable)
 return lane?[lane.links[0].state,lane.links[1].state]:['idle','idle']
}
