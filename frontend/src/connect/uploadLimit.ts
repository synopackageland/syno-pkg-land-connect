// Cloudflare's upload limits, from the catalog's marks; a way without one has
// no line. Downloads have no such limit on any way. In a web browser, DSM's
// pages send large files in pieces (docs/large-uploads.md), so the line says
// large files work there and the limit holds for other apps.
// - upload-100mb (own domain, Free link): Cloudflare's edge refuses a request
//   body over 100 MB with error 413 before the NAS sees it. Cloudflare raises
//   the limit only on its paid Business (200 MB) and Enterprise (500 MB)
//   plans; Syno Connect Center does not read the plan, so the line names
//   Free/Pro unless a plan is given.
// - upload-100s (temporary address): no size limit was measured, but one
//   request must finish within 100 seconds, so a large upload on a slow line
//   fails (at about 5 MB/s, 300 MB does).
import {optionById} from './advisor'
import {translate} from '../i18n'

export type CloudflareWayId='cloudflare-domain'|'cloudflare-workers'|'cloudflare-quick'
export type CloudflarePlan='free'|'pro'|'business'|'enterprise'

const PLAN_MB:Record<CloudflarePlan,number>={free:100,pro:100,business:200,enterprise:500}
const PLAN_NAME:Record<CloudflarePlan,string>={free:'Free/Pro',pro:'Free/Pro',business:'Business',enterprise:'Enterprise'}
const QUICK_SECONDS=100

export type UploadLimit={kind:'size';mb:number;plan:string}|{kind:'time';seconds:number}

// The limit of one Cloudflare way, or undefined when the catalog names none.
export function uploadLimit(way:CloudflareWayId,plan:CloudflarePlan='free'):UploadLimit|undefined {
 const limits=optionById(way)?.pricing.limits??[]
 if(limits.includes('upload-100mb'))return {kind:'size',mb:PLAN_MB[plan],plan:PLAN_NAME[plan]}
 if(limits.includes('upload-100s'))return {kind:'time',seconds:QUICK_SECONDS}
 return undefined
}

// The line's words, as UploadLimit shows them beside their icons, and the tooltip.
export function uploadLimitParts(locale:string,limit:UploadLimit):{upload:string;download:string;detail:string} {
 const t=(key:string)=>translate(locale,'cflimit.'+key)
 const download=t('download')
 if(limit.kind==='time'){
  const s=String(limit.seconds)
  return {upload:t('uploadTime').replace('{s}',s),download,detail:t('detailTime').replace('{s}',s)}
 }
 return {upload:t('upload').replace('{mb}',String(limit.mb)).replace('{plan}',limit.plan),download,detail:t('detail')}
}

// The same line as text, for a list of notes where an icon cannot go.
export function uploadLimitText(locale:string,way:CloudflareWayId,plan?:CloudflarePlan):string {
 const limit=uploadLimit(way,plan)
 if(!limit)return ''
 const parts=uploadLimitParts(locale,limit)
 return `${parts.upload} · ${parts.download}`
}
