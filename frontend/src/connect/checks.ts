// connection.check: the daemon rate-limits and caches checks, so asking again
// is always safe; a call that fails reads as a failed check.
import type {Context} from '@synopackageland/sdk'
import {translate} from '../i18n'

export interface CheckResult {provider:string;state:'not-checked'|'checking'|'starting'|'ok'|'failed';code?:string;externalVerified?:boolean;checkedAt?:string;attempts?:number}

export async function checkConnection(ctx:Context,provider:string):Promise<CheckResult> {
 try{return await ctx.call<CheckResult>('connection.check',{provider})}
 catch{return {provider,state:'failed',code:'check_failed'}}
}

// Why a check failed, in words.
export function checkReason(locale:string,code=''):string {
 const key=`cloudflare.code.${code}`
 if(code.startsWith('cloudflare_')&&translate(locale,key)!==key)return translate(locale,key)
 return translate(locale,['not_connected','check_timeout'].includes(code)?`connect.check.code.${code}`:'connect.check.code.check_failed')
}

// A connection still settling: the daemon retries its check until it passes
// or the settling window ends, and only then reports a failure.
export const settling=(result:CheckResult|undefined)=>result?.state==='starting'

// In-flight work is polled quickly; a settling connection every 5 s, so it
// reads as working as soon as one of the daemon's retries passes.
export function checkPollDelay(result:CheckResult):number|undefined {
 return result.state==='checking'?2000:result.state==='starting'?5000:30000
}
