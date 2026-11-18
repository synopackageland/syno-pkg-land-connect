export const accessApi={get:'cloudflare.access.get',set:'cloudflare.access.set',sync:'cloudflare.access.sync',token:'cloudflare.access.token'} as const
export const defaultSessionDuration='720h'
export const sessionDurations=['15m','30m','1h','6h','12h','24h','168h','720h','730h'] as const
export interface AccessStatus {sessionDuration?:string;state:'off'|'pending'|'synced'|'error';code?:string;revision:number;emails:string[];endpoints:Array<{host:string;protected:boolean}>;checked?:number;working?:boolean}
export function normalizeEmails(values:string[]):string[]{
 if(values.some(v=>[...v].some(c=>c.charCodeAt(0)<32)))throw new Error('access_invalid_email')
 const result=values.map(value=>{const [local,...domain]=value.trim().split('@');return `${local}@${domain.join('@').toLowerCase()}`})
 if(result.length>100||result.some(value=>!/^\S+@[^\s@*.]+(?:\.[^\s@*.]+)+$/.test(value)||/[*<>]/.test(value)))throw new Error('access_invalid_email')
 return [...new Set(result)].sort()
}
