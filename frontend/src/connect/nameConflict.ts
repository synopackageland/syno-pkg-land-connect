export interface NameReuse {name:string;record:string}
export interface NameConflict {
 objectType?:'worker'|'route'|'dns'|'tunnel'
 dashboardUrl?:string
 reason:'leftover'|'other_connect'|'other'
 suggestedName?:string
 conflictName?:string
 dnsUrl?:string
 tunnelUrl?:string
 reuseRecord?:string
}
export function nameConflict(error:unknown):NameConflict|undefined {
 const e=error as {code?:string;details?:NameConflict;hostname?:string}&Partial<NameConflict>
 if(!['cloudflare_name_taken','workers_name_conflict'].includes(e?.code??''))return
 return {conflictName:e.hostname,...(e.details??e),reason:(e.details??e).reason??'other'} as NameConflict
}
// Only generated Cloudflare dashboard locations may become clickable links.
export function cloudflareDashboard(value?:string):string|undefined {
 if(!value)return
 try{const u=new URL(value);if(u.protocol==='https:'&&['dash.cloudflare.com','one.dash.cloudflare.com'].includes(u.hostname)&&!u.username&&!u.password)return u.href}catch{/* omit malformed links */}
}
