// DSM's own ways in from outside, QuickConnect and DDNS, as the Home draws
// them beside Syno Connect Center's. Read at DSM's same-origin
// /webapi/entry.cgi with the administrator's own browser session, like
// dsmSecurity: these are DSM administrator APIs the package account cannot
// call. Read only, once when the Home shows and after each refresh; no
// polling, no timer, nothing changes in DSM. Any failure is no lane.
//
// Each lane also lists the address DSM itself gives that way, read from DSM,
// never guessed: QuickConnect's https://<ID>.<DSM's QuickConnect domain>, and
// for each DDNS hostname DSM's HTTPS port (the external port DSM was told the
// router forwards, else DSM's own). Without the domain or the port, no address.
import {onUnmounted,ref,watch,type Ref} from 'vue'
import type {Context} from '@synopackageland/sdk'
import type {LaneSpec,LinkSpec} from './topology'

/** QuickConnect while DSM has it turned on; id is the QuickConnect ID, url DSM's QuickConnect address when DSM names its domain. */
export interface QuickConnect {id:string;status:'connected'|'not-connected'|'unknown';url?:string}
/** One DDNS record DSM keeps up to date (turned on in DSM); url is DSM on that hostname once DSM's port was read. */
export interface DdnsRecord {hostname:string;provider:string;status:DdnsStatus;ip:string;url?:string}
/** DSM's DDNS states, as Control Panel shows them. */
export type DdnsStatus='normal'|'updating'|'failed'
export interface DsmAccess {quickconnect?:QuickConnect;ddns:DdnsRecord[]}

export const QUICKCONNECT='quickconnect'
export const DDNS='ddns'

/** One read of a DSM WebAPI in the user's session; undefined on any failure. No timeout: the Home never waits for it. */
async function read(ctx:Context,api:string,method:string,version:number):Promise<Record<string,unknown>|undefined>{
 const token=ctx.files?.token?.()
 if(!token)return undefined
 try{
  const response=await fetch('/webapi/entry.cgi',{
   method:'POST',credentials:'same-origin',mode:'same-origin',redirect:'error',cache:'no-store',
   headers:{'Content-Type':'application/x-www-form-urlencoded','X-SYNO-TOKEN':token},
   body:new URLSearchParams({api,version:String(version),method,SynoToken:token}),
  })
  const reply=response.ok?await response.json():undefined
  return reply?.success===true&&reply.data&&typeof reply.data==='object'?reply.data:undefined
 }catch{return undefined}
}

const text=(v:unknown)=>typeof v==='string'?v.trim():''
const LABEL=/^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$/
/** A DNS name, lower case, without a trailing dot; '' when it is not one. */
function hostName(v:unknown):string {
 const h=text(v).toLowerCase().replace(/\.$/,'')
 return h.length<=253&&h.split('.').every(l=>LABEL.test(l))?h:''
}
const validPort=(p:unknown):p is number=>typeof p==='number'&&Number.isInteger(p)&&p>0&&p<65536

/** SYNO.Core.QuickConnect get v2 and status v1: undefined unless DSM says it is on. */
export function quickConnectOf(config:Record<string,unknown>|undefined,status:Record<string,unknown>|undefined):QuickConnect|undefined {
 if(config?.enabled!==true)return undefined
 const state=text(status?.status)
 const id=text(config.server_alias)
 // DSM's own QuickConnect address: the ID under the domain DSM names (quickconnect.to, or a regional one).
 const alias=LABEL.test(id.toLowerCase())?id.toLowerCase():'',domain=hostName(config.domain)
 return {id,status:!state?'unknown':state==='connected'?'connected':'not-connected',...(alias&&domain.includes('.')?{url:`https://${alias}.${domain}`}:{})}
}

/**
 * DSM's HTTPS port for a DDNS hostname: the external HTTPS port set in
 * Control Panel > External Access > Advanced (the one the router forwards),
 * else DSM's own (SYNO.Core.Web.DSM). undefined when neither was read.
 */
export function dsmHttpsPort(web:Record<string,unknown>|undefined,external:Record<string,unknown>|undefined):number|undefined {
 if(validPort(external?.https_port))return external.https_port
 return validPort(web?.https_port)?web.https_port:undefined
}
export const ddnsUrl=(hostname:string,port:number|undefined)=>{
 const host=hostName(hostname)
 return host&&port?`https://${host}${port===443?'':':'+port}`:undefined
}

// Control Panel's own reading of a record's status: normal and in process are
// green, an address not ready yet or a check still loading is on its way, a
// date is the last update; anything else is a failure. disabled is off.
function ddnsStatus(status:string):DdnsStatus|undefined {
 if(status==='disabled')return undefined
 if(status==='service_ddns_normal'||status==='service_status_inprocess'||/^\d{4}-\d{1,2}-\d{1,2} \d{1,2}:\d{2}$/.test(status))return 'normal'
 if(status==='loading'||status==='ip_not_ready')return 'updating'
 return 'failed'
}

/** SYNO.Core.DDNS.Record list v1: only the records turned on, never one DSM turned off. */
export function ddnsOf(list:Record<string,unknown>|undefined,port?:number):DdnsRecord[] {
 const records=Array.isArray(list?.records)?list.records as Record<string,unknown>[]:[]
 return records.flatMap(r=>{
  if(!r||r.enable!==true||!text(r.hostname))return []
  const status=ddnsStatus(text(r.status))
  if(!status)return []
  // A custom provider's id is its own name; Synology's is "Synology".
  const url=ddnsUrl(text(r.hostname),port)
  return [{hostname:text(r.hostname),provider:text(r.provider)||text(r.id),status,ip:text(r.ip)||text(r.ipv6),...(url?{url}:{})}]
 })
}

export async function readDsmAccess(ctx:Context):Promise<DsmAccess>{
 const [config,status,ddns]=await Promise.all([
  read(ctx,'SYNO.Core.QuickConnect','get',2),
  read(ctx,'SYNO.Core.QuickConnect','status',1),
  read(ctx,'SYNO.Core.DDNS.Record','list',1),
 ])
 // DSM's ports only matter for a DDNS hostname.
 const records=Array.isArray(ddns?.records)&&ddns.records.length
 const [web,external]=records?await Promise.all([read(ctx,'SYNO.Core.Web.DSM','get',2),read(ctx,'SYNO.Core.Web.DSM.External','get',1)]):[]
 return {quickconnect:quickConnectOf(config,status),ddns:ddnsOf(ddns,dsmHttpsPort(web,external))}
}

// Nobody checks either from outside: the reach is never claimed.
const unverified:LinkSpec={state:'unknown',label:'overview.reach.unverified'}

/** The QuickConnect lane: DSM's connection to Synology on the NAS side. */
export function quickConnectLane(q:QuickConnect|undefined):LaneSpec|undefined {
 if(!q)return undefined
 const nas:LinkSpec=q.status==='connected'?{state:'active',label:'overview.dsm.qcConnected'}:q.status==='not-connected'?{state:'error',label:'overview.dsm.qcNotConnected'}:{state:'unknown',label:'setup.unknown'}
 return {provider:QUICKCONNECT,links:[unverified,nas],addresses:q.url?[q.url]:[]}
}

/** All DDNS records as one lane to one hub: a failed one wins, then one still updating. */
export function ddnsLane(records:DdnsRecord[]):LaneSpec|undefined {
 if(!records.length)return undefined
 const nas:LinkSpec=records.some(r=>r.status==='failed')?{state:'error',label:'overview.dsm.ddnsFailed'}:records.some(r=>r.status==='updating')?{state:'pending',label:'overview.dsm.ddnsUpdating'}:{state:'active',label:'overview.dsm.ddnsNormal'}
 return {provider:DDNS,links:[unverified,nas],addresses:[...new Set(records.flatMap(r=>r.url?[r.url]:[]))]}
}

/**
 * Reads DSM's QuickConnect and DDNS after each completed refresh, never
 * before the Home's own status: the lanes join the topology when the answer
 * arrives. A failed read draws no lane.
 */
export function useDsmAccess(ctx:Context,refreshes:Ref<number>):Ref<DsmAccess> {
 const access=ref<DsmAccess>({ddns:[]})
 let disposed=false,epoch=0
 async function load(){
  const mine=++epoch
  const next=await readDsmAccess(ctx)
  if(!disposed&&mine===epoch)access.value=next
 }
 if(refreshes.value>0)void load()
 watch(refreshes,()=>void load())
 onUnmounted(()=>{disposed=true})
 return access
}
