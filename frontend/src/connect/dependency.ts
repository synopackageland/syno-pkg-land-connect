// The backend owns dependency order and failure attribution. This module only
// renders its one recovery action and maps that attribution to the diagram hop.
import type {LinkSpec} from './topology'
export interface DependencyProblem {code:string;piece:string;action:string;page:'cloudflare'|'netbird';hop:'tunnel'|'bridge'|'signal';impact:'connection'|'maintenance'}
export interface DependencyReport {enabled:boolean;checking?:boolean;zone?:string;problem?:DependencyProblem}
export function dependencyText(code:string,t:(key:string)=>string):string{
 const key='dependency.code.'+code
 if(t(key)!==key)return t(key)
 for(const prefix of ['nbserver.code.','cloudflare.code.','cfpage.code.','apps.code.']){const old=prefix+code;if(t(old)!==old)return t(old)}
 return t('dependency.code.unavailable')
}
export function dependencyLinks(report:DependencyReport|undefined,way:'management'|'signal'):[LinkSpec,LinkSpec]{
 const active:LinkSpec={state:'active',label:''}
 const unknown:LinkSpec={state:'unknown',label:''}
 if(!report||report.checking)return [unknown,unknown]
 const p=report.problem
 if(!p)return [active,active]
 const issue:LinkSpec={state:p.code==='cloudflare_connecting'?'pending':p.impact==='maintenance'?'attention':'error',label:'dependency.code.'+p.code}
 // Both management and the Worker's WebSocket upstream traverse the tunnel.
 if(p.hop==='tunnel')return [unknown,issue]
 if(way==='signal')return p.hop==='bridge'?[issue,unknown]:[active,issue]
 return [active,active]
}
/**
 * The tunnel hop of a way's own picture (the Cloudflare page), or nothing.
 * Only while that way is meant to be up and something really depends on it: a
 * way the user turned off is off, not broken (`wayPath.ts` owns that word), and
 * a dependency nothing uses is not this way's problem.
 */
export function dependencyPathLinks(report:DependencyReport|undefined,running:boolean):[LinkSpec,LinkSpec]|undefined{
 if(!running||!report?.enabled||report.problem?.hop!=='tunnel')return undefined
 return dependencyLinks(report,'management')
}
