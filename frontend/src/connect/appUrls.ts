import type {NameReuse} from './nameConflict'
// App URLs: the Package Land Apps Connect can carry to the private network
// (Tailscale), a public Cloudflare name or a workers.dev relay in the
// user's own Cloudflare account, and the approvals behind them.
// The shapes follow the external-access spec (§5.4 R12–R18, §7.2): the
// backend scans only when this page opens or is refreshed, a "waiting"
// target is never written to state, and nothing opens without a click.

/** The API methods the page calls. */
export const appsApi = {
  list: 'apps.list',
  inspect: 'apps.inspect',
  setPrivate: 'apps.setprivate',
  setPublic: 'apps.setpublic',
  reenable: 'apps.reenable',
  clear: 'apps.clear',
  cloudflareConnect: 'apps.cloudflareconnect',
  cloudflareDisconnect: 'apps.cloudflaredisconnect',
  cleanupOrphans: 'apps.cleanuporphans',
  inspectDsm: 'apps.inspectdsm',
  setDsm: 'apps.setdsm',
  setRelay: 'apps.setrelay',
  relayConnect: 'apps.relayconnect',
  relayDisconnect: 'apps.relaydisconnect',
  workersStatus: 'apps.workersstatus',
  workersPublishDsm: 'apps.workerspublishdsm',
  resetState: 'apps.resetstate',
  customAdd: 'apps.customadd',
  customEdit: 'apps.customedit',
  customRemove: 'apps.customremove',
  customPause: 'apps.custompause',
  customCandidates: 'apps.customcandidates',
} as const

/** public is the Cloudflare own-domain name; relay the workers.dev address. An App has at most one of them. */
export type Scope = 'private' | 'public' | 'relay'
export type PublicScope = Exclude<Scope, 'private'>

/** gateway.json's protection fields (R12a), which only shape the disclosure text. */
export interface Protection {entry?:string; access?:string; upstreamAccess?: string; passthrough?: string[]; optionalPassthrough?: string[]}

/** Why a scope is paused; every pause needs a click to resume (N17). */
// The reasons the backend stores on a paused scope (internal/apps pin proxy,
// lifecycle and Cloudflare routes); "removed" shows as the Removed status.
export const pauseReasons = ['entry_changed', 'port_changed', 'protection_changed', 'cert_mismatch', 'route_mismatch', 'duplicate_port', 'dsm_port_changed', 'authority_changed', 'route-failed', 'activation-failed', 'admin_paused'] as const
export type PauseReason = typeof pauseReasons[number]

export interface PackageEntry {package:string;port:number;path?:string}
export interface EntryChange {beforeProtection?:Protection;afterProtection?:Protection;before:PackageEntry;after:PackageEntry;reason:string}
export const entryLabel=(e:PackageEntry)=>`https://127.0.0.1:${e.port}${e.path??'/'}`
export const entryChangeTitleKey=(c:EntryChange)=>entryLabel(c.before)===entryLabel(c.after)?'reason.protection_changed':'entry.changed'
export interface ScopeView {
 entryChange?:EntryChange
  state: 'active' | 'paused'
  /** A PauseReason; any other text shows as a plain pause. */
  reason?: string
  url: string
  /** false while the target cannot be reached right now: "waiting", never written to state. */
  reachable?: boolean
  /** The provider route is not removed yet; the proxy already refuses it. */
  cleanup?: boolean
}

export interface AppRow {
 entry?:PackageEntry
  dsm?: boolean
  package: string
  name: string
  port: number
  /** The App's SDK predates /_spl/ping: listed, but it cannot be opened. */
  needsUpdate?: boolean
  /** The target is unreachable; this is not evidence that its SDK needs an update */
  waiting?: boolean
  /** The App's SDK can take a public address (SDK_EXT, SDK 0.29.0 or later). */
  publicCapable?: boolean
  /** INFO is gone: the App was uninstalled; Clear removes what Connect kept. */
  removed?: boolean
  protection: Protection
  /** The App's 64-pixel main-menu icon on the DSM origin. */
  icon?: string
  /** The App's own settings.cgi on the DSM origin. */
  settingsUrl?: string
  /** gateway.json's external policy; single_origin: the App signs in on one address only. */
  external?: {single_origin?: boolean}
  private?: ScopeView
  public?: ScopeView
  relay?: ScopeView
  /** Video or many large files: never preselected for workers.dev, and warned about when picked. */
  media?: 'high' | 'medium'
  /** An app the administrator added by URL; package is its custom.<name> ID. */
  custom?: CustomView
}

/** The Apps of a list without DSM's own row, for views that show DSM in a row of its own. */
export const listedApps=(view:{apps?:AppRow[]}|undefined):AppRow[]=>(view?.apps??[]).filter(row=>!row.dsm)

/**
 * A custom app: any web service on the NAS or the LAN, added by URL. It opens
 * only with the free link or the user's own domain. DSM sign-in comes first
 * unless the administrator confirmed that the app signs people in itself.
 */
export interface CustomView {
  url: string
  appSignIn: boolean
  insecureTls: boolean
  /** The list's TCP check; a hint only, it never pauses the app. */
  reachable?: boolean
}

/**
 * The workers.dev relay: an API token the user pasted once, and the
 * account's workers.dev subdomain. Absent from a backend without the relay.
 */
export interface RelayView {
  dsm?: {provider:string;url:string}
  connected: boolean
  /** The account's workers.dev subdomain: addresses are <worker>.<subdomain>.workers.dev. */
  subdomain?: string
  /** The saved token no longer works; existing Workers stay until Disconnect. */
  invalid?: boolean
}

export interface AppsView {
  trustedDsm?: {issuer:string;notAfter:string;certSha256?:string}
  apps: AppRow[]
  private: {available: boolean; code?: 'tailscale-off' | 'https-off' | 'tailscale-outdated'; host?: string}
  public: {available: boolean; enabled?: boolean; connected: boolean; zone?: string; invalid?: boolean; orphanedRoutes?: number}
  relay?: RelayView
  /** Connect's state could not be read: every App counts as not approved. */
  stateCorrupt?: boolean
  /** Connections are still restoring; the page reads again until they are ready. */
  starting?: boolean
}

/** The certificate the target presents, and how Connect will pin it (R16a). */
export type PinView = {previous?:{issuer:string;notAfter:string;certSha256?:string}} & (
  | {mode: 'W'; certSha256?:string; names: string[]; issuer: string; notAfter: string; previousNames?: string[]}
  | {mode: 'S'; spki: string; issuer: string; notAfter: string; previousSpki?: string; certSha256?: string}
  /** A custom app: no certificate pin, the approval binds its configured URL. */
  | {mode: 'C'; issuer: string; notAfter: string})

/** What an approval shows; ready is false while the App cannot be reached or does not answer its ping. */
export interface Inspection {
 entry?:PackageEntry
 reuseName?:NameReuse
  hostname?: string
  /** Binds the confirmation to the inspected metadata, pin and revision */
  token?: string
  ready: boolean
  package: string
  name: string
  port: number
  protection: Protection
  pin?: PinView
  /** The first private App also routes DSM through Connect in the same approval (R14). */
  dsm?: {hostname?:string;port: number; pin: PinView}
  custom?: CustomView
}

/** The values the administrator saw and approved; the backend refuses when they no longer match. */
export interface Approval {
 reuseName?:NameReuse;hostname?:string;token?: string; port: number; pin: PinView; dsm?: {hostname?:string;port: number; pin: PinView}}

/** DSM alone, for a provider that routes DSM without any App; dsm is absent when nothing needs review. */
export interface DsmInspection {scope: Scope; dsm?: {hostname?:string;port: number; pin: PinView}; token?: string}

/**
 * A provider status that fails because DSM has no approved certificate yet:
 * DSM-only remote access, or an upgrade from before DSM needed approval.
 */
export function dsmNeedsReview(status: {state?: string; code?: string;apply?:{code?:string}} | undefined): boolean {
  return status?.code === 'pin_proxy_required'||status?.apply?.code==='pin_proxy_required'
}

/** Whether the Cloudflare tunnel serves now, not only whether Cloudflare is authorized. */
export type TunnelHealth = 'serving' | 'connecting' | 'stopped'
export function tunnelHealth(status: {state?: string; mode?: string} | undefined): TunnelHealth {
  if (status?.state === 'connected' && status.mode === 'domain') return 'serving'
  if (status?.state === 'connecting' || status?.state === 'needs-login') return 'connecting'
  return 'stopped'
}

/** The public mode an App uses other than scope, if any: one public mode per App. */
export function otherPublic(row: AppRow, scope: PublicScope): PublicScope | undefined {
  const other = scope === 'public' ? 'relay' : 'public'
  return row[other] ? other : undefined
}

/**
 * Cloudflare's create-token page with the relay's permissions filled in:
 * Workers Scripts Edit uploads each Worker with its Durable Object, secret
 * and workers.dev route and creates the account's workers.dev subdomain;
 * the same permission also allows account discovery. An explicit accountId
 * remains available when discovery does not return one account. Workers
 * Routes Edit lets the same token route the self-hosted NetBird server's
 * Signal stream to its bridge Worker on the user's own domain.
 */
export const relayPermissions = [{key: 'workers_scripts', type: 'edit'}, {key: 'workers_routes', type: 'edit'}, {key:'access',type:'edit'}, {key:'access_acct',type:'edit'}] as const
/** Token inventory; optional Access uses the shared create-token link. */
export const cloudflareTokensUrl = 'https://dash.cloudflare.com/profile/api-tokens'
export function relayTokenUrl(name = 'Syno Connect Center'): string {
  const q = new URLSearchParams({permissionGroupKeys: JSON.stringify(relayPermissions), accountId: '*', zoneId: 'all', name})
  return `https://dash.cloudflare.com/profile/api-tokens?${q.toString()}`
}

/** The free plan's daily reset, 00:00 UTC, in the viewer's own clock (08:00 in Taiwan). */
export function quotaReset(locale: string, timeZone?: string): string {
  const reset = new Date(Date.UTC(2026, 0, 1, 0, 0))
  try {return reset.toLocaleTimeString(locale.replace('_', '-'), {hour: '2-digit', minute: '2-digit', hourCycle: 'h23', timeZone})}
  catch {return '00:00'}
}

export interface PublicResult {results: Array<{package: string; ok: boolean; code?: string}>; loginUrl?: string}

/**
 * A single-origin App signs in only on the one address set in its own
 * settings. Its private URL can be that address: the origin with the App port
 * and no path, which is what the App's settings page accepts. Connect never
 * writes it there; the administrator saves it in the App's own settings.
 */
export function singleOriginUrl(row: AppRow, host?: string): string | undefined {
  if (!row.external?.single_origin) return undefined
  const url = row.private?.url ?? (host ? `https://${host}:${row.port}` : '')
  return url ? url.replace(/\/+$/, '') : undefined
}

/** The section an App URLs deep link names, in the page's own scope terms. */
export const focusScopes = {private: 'private', public: 'public', workers: 'relay'} as const

const PACKAGE = /^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$/

/**
 * Reads a launch route /apps[/<package>[/<scope>]], as the DSM menu
 * launcher's "Set up this App in Connect" button sends it. Any other route
 * gives undefined. Following it only brings a row into view; it changes
 * nothing.
 */
export function appsRoute(route: string): {package?: string; scope?: keyof typeof focusScopes} | undefined {
  const parts = route.split('/')
  if (parts[0] !== '' || parts[1] !== 'apps' || parts.length > 4) return undefined
  const [, , pkg, scope] = parts
  if (pkg === undefined || pkg === '') return parts.length === 2 || parts.length === 3 ? {} : undefined
  if (!PACKAGE.test(pkg)) return undefined
  if (scope === undefined) return {package: pkg}
  return Object.hasOwn(focusScopes, scope) ? {package: pkg, scope: scope as keyof typeof focusScopes} : undefined
}

/** What a row shows: one tag, at most one line and the one action that applies. */
export type RowStatus = 'off' | 'on' | 'waiting' | 'paused' | 'removed' | 'cleanup' | 'needs-update'

export function rowStatus(row: AppRow, scope: Scope): RowStatus {
  const view = row[scope]
  if (row.removed || view?.reason === 'removed') return 'removed'
  if (view?.cleanup) return 'cleanup'
  if (view?.state === 'paused') return 'paused'
  if (view && view.reachable === false) return 'waiting'
  if (view) return 'on'
  // A custom app that does not answer can still be approved: the list says so in its line.
  if (row.waiting && !row.custom) return 'waiting'
  if (row.needsUpdate || (scope !== 'private' && !row.publicCapable)) return 'needs-update'
  return 'off'
}

export const statusTone = {off: 'neutral', on: 'success', waiting: 'warning', paused: 'warning', removed: 'neutral', cleanup: 'danger', 'needs-update': 'neutral'} as const

/**
 * The protection summary (R7): the general line and every exception, never
 * only the first line. A public scope adds the DSM sign-in line.
 */
export function protectionLines(p: Protection, scope: Scope): Array<{key: string; paths?: string}> {
  const lines: Array<{key: string; paths?: string}> = [{key: p.upstreamAccess === 'public' ? 'ownSignIn' : 'dsmSignIn'}]
  if (p.passthrough?.length) lines.push({key: 'passthrough', paths: p.passthrough.join(', ')})
  if (p.optionalPassthrough?.length) lines.push({key: 'optional', paths: p.optionalPassthrough.join(', ')})
  if (scope !== 'private') lines.push({key: 'publicDsm'})
  return lines
}

/** A SHA-256 fingerprint in colon-separated byte pairs, as DSM writes it. */
export function fingerprint(hex: string): string {
  return hex.replace(/[^0-9a-f]/gi, '').toUpperCase().match(/.{1,2}/g)?.join(':') ?? ''
}
