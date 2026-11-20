// Cloudflare's request body limit: every way through Cloudflare's edge (own
// domain, Free link, temporary address) refuses a larger upload with error
// 413 before the NAS sees it; downloads have no such limit. The catalog's
// upload-100mb limit is the one source: a way without it has no line.
// Cloudflare raises the limit only on its paid Business (200 MB) and
// Enterprise (500 MB) plans; Syno Connect Center does not read the plan, so
// the line names Free/Pro unless a plan is given.
import {optionById} from './advisor'
import {translate} from '../i18n'

export type CloudflareWayId='cloudflare-domain'|'cloudflare-workers'|'cloudflare-quick'
export type CloudflarePlan='free'|'pro'|'business'|'enterprise'

const PLAN_MB:Record<CloudflarePlan,number>={free:100,pro:100,business:200,enterprise:500}
const PLAN_NAME:Record<CloudflarePlan,string>={free:'Free/Pro',pro:'Free/Pro',business:'Business',enterprise:'Enterprise'}

export interface UploadLimit {mb:number;plan:string}

// The limit of one Cloudflare way, or undefined when the catalog names none.
export function uploadLimit(way:CloudflareWayId,plan:CloudflarePlan='free'):UploadLimit|undefined {
 const option=optionById(way)
 if(!option?.pricing.limits.includes('upload-100mb'))return undefined
 return {mb:PLAN_MB[plan],plan:PLAN_NAME[plan]}
}

// The line's two halves in words, as UploadLimit shows them beside their icons.
export function uploadLimitParts(locale:string,limit:UploadLimit):{upload:string;download:string} {
 const t=(key:string)=>translate(locale,'cflimit.'+key)
 return {upload:t('upload').replace('{mb}',String(limit.mb)).replace('{plan}',limit.plan),download:t('download')}
}

// The same line as text, for a list of notes where an icon cannot go.
export function uploadLimitText(locale:string,way:CloudflareWayId,plan?:CloudflarePlan):string {
 const limit=uploadLimit(way,plan)
 if(!limit)return ''
 const parts=uploadLimitParts(locale,limit)
 return `${parts.upload} · ${parts.download}`
}
