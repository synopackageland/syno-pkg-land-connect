// The Free link (workers.dev) on the Cloudflare page: the account's one
// workers.dev name, shared by every App address on that Cloudflare account
// (<app>.<name>.workers.dev), and what the page shows about it.
import {hostOf} from './cloudflareWay'

/** The workers.dev status apps.workersstatus and apps.relayconnect answer. */
export interface WorkersStatus {
  usage?:{public:boolean;relay:boolean}
  connected: boolean
  relayState?: 'connected'|'connecting'|'reconnecting'|'disconnected'
  /** The saved token no longer works, or a Disconnect still has Workers to delete. */
  invalid?: boolean
  /** The account's workers.dev name. */
  subdomain?: string
  /** workers_subdomain_required: the account has no name yet and the page asks for one. */
  code?: string
  /** Where public DSM opens: on this Free link (workers) or on the own domain (cloudflare). */
  dsm?: {provider: string; url: string}
  /** Why turning the Free link on left DSM where it was, such as apps_dsm_public_elsewhere. */
  dsmCode?: string
}

/** The address the Free link shows: DSM's when it serves DSM, else the account's name. */
export function freeLinkAddress(status: WorkersStatus | undefined): string {
  if (status?.dsm?.provider === 'workers') return hostOf(status.dsm.url)
  return status?.subdomain ? `*.${status.subdomain}.workers.dev` : ''
}

/** One DNS label, as the backend checks it. */
export const LABEL = /^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/

export const validLabel = (name: string): boolean => LABEL.test(name)

/** Lower case, other characters as hyphens, at most 63 characters, no hyphen at either end. */
export function toLabel(text: string): string {
  return text.toLowerCase().replace(/[^a-z0-9-]+/g, '-').replace(/-+/g, '-').slice(0, 63).replace(/^-+|-+$/g, '')
}

/**
 * The name the page fills in: the NAS's own host name when DSM is opened by
 * name, else Connect's installation name (nas-<id>), which no other NAS uses.
 */
export function defaultAccountName(host: string, fallback = ''): string {
  const first = host.split('.')[0] ?? ''
  const ip = /^\d+$/.test(first) || host.includes(':')
  const name = ip || first === 'localhost' ? '' : toLabel(first)
  return validLabel(name) ? name : validLabel(fallback) ? fallback : 'my-nas'
}

/** A name to try after a taken one: the same name with a short number. */
export function nextName(name: string, random = Math.random): string {
  const n = String(100 + Math.floor(random() * 900))
  return toLabel(`${name.slice(0, 59)}-${n}`)
}

/** Where the Free link stands, for the status tag and the path picture. */
export type FreeLinkState = 'off' | 'name' | 'on' | 'attention'
export function freeLinkState(status: WorkersStatus | undefined): FreeLinkState {
  if (!status) return 'off'
  if (status.connected) return status.invalid ? 'attention' : 'on'
  return status.code === 'workers_subdomain_required' ? 'name' : 'off'
}

/** Cloudflare's own page for the account's workers.dev name. */
export const subdomainSettingsUrl = 'https://dash.cloudflare.com/?to=/:account/workers/subdomain'

/** Opens the Cloudflare page on its Free link tab. */
export function openFreeLink(s: {go(target: 'cloudflare', guide?: {tab?: string}): void}): void {
  s.go('cloudflare', {tab: 'free'})
}
