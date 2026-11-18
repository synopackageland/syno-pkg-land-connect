// The connection methods as backend/internal/advisor/catalog.json describes
// them: the comparison table, the method facts and the steps read the file
// itself; the backend's advisor.recommend, which answers ctl recommend, ranks
// from the same file. Every enumerated value is a message key.
import catalog from '../../../backend/internal/advisor/catalog.json'
import type {Method} from './topology'

export interface CatalogOption {
 id:string;provider?:string;mode?:string;tier?:number;native?:boolean;goals:Record<string,number>;requires?:string[]
 fit:string;reach:string;scope:string;cost:string;accounts:string[];client:string;difficulty:number;https:string;limits:string[]
 who:string;via:string;gate:Gate;public:boolean;pairWith:string[];links?:Record<string,string>
 risk:'own-devices'|'login-page';protection:Protection;install:Install;pricing:Pricing;guide:string[]
 // Steps for a use that differ from guide, and the capability a use relies on.
 guideFor?:Record<string,string[]>
}
// The sign-in in front of the NAS's own: Cloudflare Access email sign-in, DSM
// sign-in before an app, signing in to the private network, or none.
export type Gate='cloudflare-access'|'dsm-sign-in'|'network-sign-in'|'none'
// What stands between the internet and the NAS; none means services face it directly.
export type Protection='cloudflare'|'private-network'|'synology-relay'|'none'
// What a connecting device installs, and whether computers and phones can use the method.
export interface Install {app:string;desktop:Answer;phone:Answer;links?:Record<string,string>}
type Answer='yes'|'no'|'unknown'
// What may cost money, from the official pages in links as read in checked.
export interface Pricing {limits:string[];links:Record<string,string>;checked:string}
export interface Catalog {version:string;goals:string[];options:CatalogOption[];protections:Record<string,string>}
export const CATALOG=catalog as unknown as Catalog

export const optionById=(id:string)=>CATALOG.options.find(o=>o.id===id)

// The one option that describes a method card: Cloudflare's first is its
// temporary address, the quickest start.
export const optionFor=(method:Method)=>CATALOG.options.find(o=>o.provider===method)!

// Every message key a catalog value names.
export function catalogKeys():string[] {
 const keys=new Set<string>()
 for(const goal of CATALOG.goals)keys.add(`advisor.goal.${goal}`)
 for(const o of CATALOG.options){
  keys.add(`advisor.option.${o.id}`)
  for(const field of ['fit','reach','scope','cost','client','who','via','gate'] as const)keys.add(`advisor.${field}.${o[field]}`)
  keys.add(`advisor.difficulty.${o.difficulty}`)
  keys.add(`advisor.https.${o.https}`);keys.add(`advisor.httpsTag.${o.https}`)
  for(const a of o.accounts)keys.add(`advisor.accounts.${a}`)
  for(const l of o.limits)keys.add(`advisor.limits.${l}`)
  for(const p of o.pairWith)keys.add(`advisor.pairWith.${p}`)
 }
 for(const column of COLUMNS)keys.add(`advisor.column.${column}`)
 keys.add('advisor.accounts.none')
 // The comparison's protection words and DSM's own protection checks.
 for(const o of CATALOG.options)keys.add(`guide.protection.${o.protection}`)
 for(const check of ['on','off','unknown','turnOn','howTo'])keys.add(`guide.check.${check}`)
 return [...keys]
}

export const COLUMNS=['option','protection','gate','fit','reach','scope','cost','accounts','client','difficulty','who','via','https','public','pairWith','limits'] as const

// One table row per option, in words.
export function comparisonRows(locale:string,t:(key:string)=>string):Record<string,string>[] {
 // Chinese and Japanese separate list items with an ideographic comma.
 const sep=/^(zh|ja)/.test(locale)?'、':', '
 const list=(prefix:string,values:string[])=>values.length?values.map(v=>t(`${prefix}.${v}`)).join(sep):t('advisor.accounts.none')
 return CATALOG.options.map(o=>({
  id:o.id,option:t(`advisor.option.${o.id}`),protection:t(`guide.protection.${o.protection}`),gate:t(`advisor.gate.${o.gate}`),fit:t(`advisor.fit.${o.fit}`),reach:t(`advisor.reach.${o.reach}`),scope:t(`advisor.scope.${o.scope}`),cost:t(`advisor.cost.${o.cost}`),
  accounts:list('advisor.accounts',o.accounts),client:t(`advisor.client.${o.client}`),difficulty:t(`advisor.difficulty.${o.difficulty}`),
  who:t(`advisor.who.${o.who}`),via:t(`advisor.via.${o.via}`),https:t(`advisor.https.${o.https}`),public:t(o.public?'advisor.yes':'advisor.no'),
  pairWith:list('advisor.pairWith',o.pairWith),limits:list('advisor.limits',o.limits),
 }))
}
