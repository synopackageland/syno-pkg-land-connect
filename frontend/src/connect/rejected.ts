// A change the running connection cannot keep (apply_unavailable): the
// backend refused it and nothing changed. It is that action's result, shown
// beside it until dismissed, left or a later check passes; never the
// connection's status. Apply anyway sends the same change with replace, and
// the connection drops for about a minute while it switches.
import type {ConnectionStatus} from '../context'
import {addressesOf} from './addresses'

export const REJECTED_CODE='apply_unavailable'
export interface RejectedChange {provider:string;params:Record<string,unknown>;signIn?:boolean}

const hostOf=(address:string)=>{try{return new URL(address).host}catch{return ''}}

/** Whether this page itself comes through the connection's address, so switching it drops the page too. */
export function browsingThrough(status:ConnectionStatus|undefined,host:string):boolean {
 if(!status||!host)return false
 return [...addressesOf(status).map(hostOf),status.hostname??''].some(h=>h!==''&&h===host)
}

/**
 * A change that can go ahead by reconnecting without asking: the temporary
 * address changes on every restart anyway, so replacing it loses nothing,
 * unless this page comes through it.
 */
export function replacesSafely(params:Record<string,unknown>,status:ConnectionStatus|undefined,host:string):boolean {
 return params.provider==='cloudflare'&&status?.mode==='quick'&&!browsingThrough(status,host)
}
