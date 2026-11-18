// Use the browser's DSM origin so DSM sees the session's original source IP.
// Never relay browser cookies through the package's loopback HTTP client.
import type {Context} from '@synopackageland/sdk'
import type {ExternalSettings} from '../context'

async function call(ctx:Context,method:'get'|'set',fields:Record<string,string>={}):Promise<unknown>{
 const token=ctx.files?.token?.()
 if(!token)throw new Error('DSM session token unavailable')
 const body=new URLSearchParams({api:'SYNO.Core.Web.DSM.External',version:'1',method,SynoToken:token,...fields})
 const response=await fetch('/webapi/entry.cgi',{
  method:'POST',credentials:'same-origin',mode:'same-origin',redirect:'error',cache:'no-store',
  headers:{'Content-Type':'application/x-www-form-urlencoded','X-SYNO-TOKEN':token},body,
 })
 if(!response.ok)throw new Error('DSM external settings unavailable')
 const reply=await response.json()
 if(reply?.success!==true)throw new Error('DSM refused external settings')
 return reply.data
}

export async function readDsmExternal(ctx:Context):Promise<ExternalSettings>{
 const value=await call(ctx,'get') as ExternalSettings|undefined
 if(!value||typeof value.hostname!=='string')throw new Error('Invalid DSM external settings')
 const port=(p:unknown):number|null=>{
  if(p==null||p===0)return null
  if(typeof p!=='number'||!Number.isInteger(p)||p<1||p>65535)throw new Error('Invalid DSM external port')
  return p
 }
 return {hostname:value.hostname,http_port:port(value.http_port),https_port:port(value.https_port)}
}

export function externalMatches(current:ExternalSettings,hostname:string):boolean{
 const host=(h:string)=>h.trim().toLowerCase().replace(/\.$/,'')
 return host(current.hostname)===host(hostname)&&current.https_port===443
}

// The prompt changes only its advertised hostname and HTTPS port. DSM remains
// the authority for administrator permission; a success needs a fresh readback.
export async function applyDsmExternal(ctx:Context,hostname:string):Promise<ExternalSettings>{
 await call(ctx,'set',{hostname:JSON.stringify(hostname),https_port:'443'})
 const current=await readDsmExternal(ctx)
 if(!externalMatches(current,hostname))throw new Error('DSM external settings unconfirmed')
 return current
}
