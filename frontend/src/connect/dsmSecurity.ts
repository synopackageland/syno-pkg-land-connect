// Whether DSM's Auto Block and enforced 2-factor sign-in are on, read at DSM's
// same-origin /webapi/entry.cgi with the user's own browser session, like
// dsmExternal. Connect only reads them; a user DSM does not let read them, or
// any failure, is unknown, never an error.
import type {Context} from '@synopackageland/sdk'

export type Check='on'|'off'|'unknown'
export interface DsmSecurity {'auto-block':Check;'dsm-2fa':Check}
export const PROTECTIONS=['auto-block','dsm-2fa'] as const

/** One read of a DSM WebAPI in the user's session; undefined on any failure. */
export async function readDsm(ctx:Context,api:string,method='get'):Promise<Record<string,unknown>|undefined>{
 const token=ctx.files?.token?.()
 if(!token)return undefined
 try{
  const response=await fetch('/webapi/entry.cgi',{
   method:'POST',credentials:'same-origin',mode:'same-origin',redirect:'error',cache:'no-store',
   headers:{'Content-Type':'application/x-www-form-urlencoded','X-SYNO-TOKEN':token},
   body:new URLSearchParams({api,version:'1',method,SynoToken:token}),
  })
  const reply=response.ok?await response.json():undefined
  return reply?.success===true&&reply.data&&typeof reply.data==='object'?reply.data:undefined
 }catch{return undefined}
}

export async function readDsmSecurity(ctx:Context):Promise<DsmSecurity>{
 const [block,otp]=await Promise.all([readDsm(ctx,'SYNO.Core.Security.AutoBlock'),readDsm(ctx,'SYNO.Core.OTP.EnforcePolicy')])
 // DSM's choices: nobody, administrators, everyone, chosen users and groups.
 const option=otp?.otp_enforce_option
 return {
  'auto-block':typeof block?.enable==='boolean'?(block.enable?'on':'off'):'unknown',
  'dsm-2fa':option==='none'?'off':option==='admin'||option==='user'||option==='custom'?'on':'unknown',
 }
}

// One read per app session, shared by every place that shows it.
const reads=new WeakMap<Context,Promise<DsmSecurity>>()
export function dsmSecurity(ctx:Context):Promise<DsmSecurity>{
 let read=reads.get(ctx)
 if(!read){read=readDsmSecurity(ctx);reads.set(ctx,read)}
 return read
}
