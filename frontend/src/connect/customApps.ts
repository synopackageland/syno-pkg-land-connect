// Custom apps: what the add and edit dialog sends, and the checks it makes
// before sending. The NAS checks everything again; these only say early what
// it would refuse. The words come from the apps.custom.* catalog keys.
import type {Context,DsmPort} from '@synopackageland/sdk'
import type {AppRow} from './appUrls'

/** A section of DSM's port definitions (services.d); id is PortInfo's port_id. */
export interface ServiceLabel {id: string; title: string; desc?: string; titleKey?: string; descKey?: string}

/** One pre-fill from the NAS: a DSM package with a web port, a service DSM declares or a listening port. */
export interface Candidate {package?:string;icon?:string;name: string; url: string; source: 'package' | 'service' | 'listening' | 'synology'; service?: ServiceLabel}

/** "Web Station (HTTP)" for DSM's generic HTTP and HTTPS titles, the project for a container, else the title. */
export function serviceName(title: string, desc = '', id = ''): string {
  if (desc && /^https?$/i.test(title)) return `${desc} (${title})`
  if (desc && /^docker\(/i.test(title)) return desc
  return title || desc || id
}

/**
 * The name a candidate shows: DSM's translation of its port definition when
 * PortInfo has the same section, or names its port, else the NAS's own name.
 */
export function candidateName(c: Candidate, dsm: DsmPort[]): string {
  const port = Number(candidatePort(c))
  dsm = dsm.filter(p => p.protocol === 'tcp' || p.protocol === 'all')
  const known = (c.service && dsm.find(p => p.id === c.service!.id)) || (!c.service && c.source === 'listening' ? dsm.find(p => p.ports.includes(port)) : undefined)
  if (known && (known.name || known.desc)) return serviceName(known.name, known.desc, known.id)
  if (c.service) return serviceName(c.service.title, c.service.desc, c.service.id)
  return c.name
}

export interface CustomForm {name: string; url: string; appSignIn: boolean; insecureTls: boolean; acceptRisk: boolean}

export function customForm(row?: AppRow): CustomForm {
  const c = row?.custom
  return {name: row?.name ?? '', url: c?.url ?? '', appSignIn: !!c?.appSignIn, insecureTls: !!c?.insecureTls, acceptRisk: !!c?.appSignIn}
}

/** A typed URL can select a declared package entry, never create a DSM proxy. */
export function knownCandidate(raw:string,candidates:Candidate[]):Candidate|undefined {
 try {const u=new URL(raw.trim());if(u.username||u.password||u.search||u.hash)return undefined
 return candidates.find(c=>{if(!c.package)return false;const v=new URL(c.url);return u.protocol===v.protocol&&u.port===v.port&&u.pathname===v.pathname&&['127.0.0.1','localhost',location.hostname].includes(u.hostname)})}catch{return undefined}
}

/** The URL's error code, or '' when the NAS would take it. */
export function urlError(raw: string): '' | 'custom_url_invalid' | 'custom_url_scheme' | 'custom_url_path' | 'custom_target_refused' {
  const text = raw.trim()
  if (!text) return 'custom_url_invalid'
  // "ftp:" or "mailto:" names another scheme; "nas.local:8080" is a host without one.
  if (!/^[a-z][a-z0-9+.-]*:\/\//i.test(text)) return /^[a-z][a-z0-9+.-]*:(?!\d)/i.test(text) ? 'custom_url_scheme' : 'custom_url_invalid'
  let u: URL
  try {u = new URL(text)} catch {return 'custom_url_invalid'}
  if (u.protocol !== 'http:' && u.protocol !== 'https:') return 'custom_url_scheme'
  if (u.username || u.password || !u.hostname) return 'custom_url_invalid'
  if (/\/(?:\.\.?)(?:\/|$)/.test(text.replace(/^https?:\/\/[^/]+/i,'')) || u.pathname.includes('//') || /%|\\/.test(u.pathname) || u.search || u.hash || text.includes('#') || text.includes('?')) return 'custom_url_path'
  const host = u.hostname.replace(/^\[|\]$/g, '').toLowerCase()
  // Link-local (cloud metadata), unspecified, multicast and broadcast never become targets.
  if (/^169\.254\./.test(host) || /^fe[89ab][0-9a-f]:/.test(host) || host === '0.0.0.0' || host === '::' || host === 'fd00:ec2::254' || host === '255.255.255.255' || /^2(2[4-9]|3\d)\./.test(host) || /^ff[0-9a-f]{2}:/.test(host)) return 'custom_target_refused'
  return ''
}

/** A name of 1 to 64 characters. */
export const nameValid = (name: string) => {const n = [...name.trim()].length; return n >= 1 && n <= 64}

/** Turning Connect's sign-in off needs the risk confirmation, unless it is already off. */
export function needsRisk(form: CustomForm, row?: AppRow): boolean {
  return form.appSignIn && !row?.custom?.appSignIn
}

export const isHttps = (url: string) => /^https:\/\//i.test(url.trim())

/** The request for apps.customadd (no id) or apps.customedit. */
export function customRequest(form: CustomForm, id?: string) {
  return {
    ...(id ? {id} : {}),
    name: form.name.trim(),
    url: form.url.trim(),
    appSignIn: form.appSignIn,
    insecureTls: isHttps(form.url) && form.insecureTls,
    ...(form.appSignIn && form.acceptRisk ? {acceptRisk: true} : {}),
  }
}

/** The name a picked candidate fills in; a bare port gets "Port 8080" from the caller. */
export function candidatePort(c: Candidate): string {
  try {return new URL(c.url).port || (c.url.startsWith('https') ? '443' : '80')} catch {return ''}
}

/** Login Portal is read in the signed-in administrator's window, never as root.
 * Missing window transport is a standalone host; DSM refusals remain visible.
 */
export async function readSynologyPortals(ctx: Context): Promise<unknown[]> {
 if (!ctx.webApi) return []
 const [data, desktop] = await Promise.all([
  ctx.webApi('SYNO.Core.AppPortal','list',2,{additional:['default_setting']}) as Promise<{portal:Record<string,unknown>[]}> ,
  ctx.webApi('SYNO.Core.Desktop.Initdata','get',1,{action:'jsconfig'}).catch(()=>null) as Promise<{JSConfig?:Record<string,Record<string,{appPortalId?:string;icon?:string;jsBaseURL?:string}>>}|null>,
 ])
 if (!Array.isArray(data?.portal)) throw new Error('Invalid SYNO.Core.AppPortal list response')
 const icons=new Map<string,string>()
 for(const file of Object.values(desktop?.JSConfig??{}))for(const [id,app] of Object.entries(file)){
  if(!app.icon||!app.jsBaseURL)continue
  const icon='/'+app.jsBaseURL.replace(/^\//,'')+'/'+app.icon.replace('{0}','64')
  if(/^\/webman\/[a-zA-Z0-9_./-]+\.png$/.test(icon)&&!icon.includes('..')&&!icon.includes('//'))icons.set(app.appPortalId||id,icon)
 }
 // Project DSM's open-ended response onto the strict App route contract. Fields
 // such as acl/fqdn/hsts and factory ports are metadata, not candidate inputs.
 return data.portal.filter(p=>p&&typeof p.id==='string'&&typeof p.display_name==='string').map(p=>{
  const suggestion:Record<string,unknown>={id:p.id,display_name:p.display_name}
  if(typeof p.alias==='string')suggestion.alias=p.alias
  for(const key of ['http_port','https_port'])if(typeof p[key]==='number')suggestion[key]=p[key]
  const additional=p.additional as {default_setting?:{alias?:unknown}}|undefined
  if(typeof additional?.default_setting?.alias==='string')suggestion.additional={default_setting:{alias:additional.default_setting.alias}}
  const icon=icons.get(String(p.id));if(icon)suggestion.icon=icon
  return suggestion
 })
}
