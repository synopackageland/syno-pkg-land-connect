// Phone: the phone browser page (m/index.html) on each way that reaches this
// NAS's DSM, as the Overview's Phone section offers them in its picker. Every
// way Connect runs now gives one route, then DSM's own QuickConnect and DDNS
// addresses, then DSM's own address (the one this browser uses, else a LAN
// address). The QR
// code and link carry the page address only, never a sign-in.
import type {ConnectionStatus} from '../context'
import {addressesOf} from './addresses'
import {browserLink,originOf,temporaryHost,type PhoneAddNasReply} from './phoneLink'
import type {RelayStatus} from './topology'

export type PhoneRouteId = 'cloudflare' | 'workers' | 'ipv6' | 'quickconnect' | 'ddns' | 'netbird' | 'tailscale' | 'local'
/** What a route needs from the phone: the app, the same network, a certificate warning, a changing address. */
export type PhoneNote = 'sameNetwork' | 'selfSigned' | 'netbirdApp' | 'tailscaleApp' | 'temporary'
export interface PhoneRoute {id: PhoneRouteId; origin: string; link: string; publicHttps: boolean; notes: PhoneNote[]}

// Public ways first, Cloudflare leading; then the private networks.
const ORDER: PhoneRouteId[] = ['cloudflare', 'workers', 'ipv6', 'quickconnect', 'ddns', 'netbird', 'tailscale']
const PUBLIC = new Set<PhoneRouteId>(['cloudflare', 'workers', 'ipv6', 'quickconnect', 'ddns'])
/** DSM's own ways, read from DSM: QuickConnect's address and each DDNS address. */
export interface DsmRoutes {quickconnect?: string; ddns?: string[]}
const APP_NOTE: Partial<Record<PhoneRouteId, PhoneNote>> = {netbird: 'netbirdApp', tailscale: 'tailscaleApp'}
const isIP = (host: string) => /^\[.*\]$/.test(host) || /^\d{1,3}(\.\d{1,3}){3}$/.test(host)
const hostOf = (origin: string) => new URL(origin).hostname

/** The route for one DSM address of a way, as each Overview lane lists them. */
export function routeFor(id: PhoneRouteId, url: string, extra: PhoneNote[] = []): PhoneRoute | undefined {
  const origin = originOf(url)
  if (!origin) return undefined
  const notes: PhoneNote[] = [...extra]
  const note = APP_NOTE[id]
  if (note) notes.push(note)
  if (id === 'cloudflare' && temporaryHost(hostOf(origin))) notes.push('temporary')
  return {id, origin, link: browserLink(origin), publicHttps: PUBLIC.has(id) && origin.startsWith('https:'), notes: [...new Set(notes)]}
}

// One address per way: https before http, a name before an IP address.
function firstOrigin(urls: string[]): string | undefined {
  const origins = [...new Set(urls.map(originOf).filter((u): u is string => !!u))]
  const rank = (u: string) => (u.startsWith('https:') ? 0 : 2) + (isIP(hostOf(u)) ? 1 : 0)
  return origins.sort((a, b) => rank(a) - rank(b))[0]
}

/**
 * DSM's own address for the phone: the address this browser is on when it
 * is DSM's (a LAN address, or one off the LAN that no way below serves),
 * else the first LAN address. Without a reply, this browser's own origin.
 */
export function localRoute(nas: PhoneAddNasReply | undefined, here: string | undefined, externalHosts: Set<string> = new Set()): PhoneRoute | undefined {
  const list = (nas?.addresses ?? []).map(a => ({...a, url: originOf(a.url)})).filter(a => !!a.url && !externalHosts.has(hostOf(a.url)) && !temporaryHost(hostOf(a.url)))
  const pick = list.find(a => a.url === here) ?? list.find(a => a.kind === 'other') ?? list.find(a => a.kind === 'lan')
  if (pick) return routeFor('local', pick.url!, [...(pick.kind === 'lan' ? ['sameNetwork' as const] : []), ...(pick.sha256 ? ['selfSigned' as const] : [])])
  if (nas || !here || externalHosts.has(hostOf(here))) return undefined
  return routeFor('local', here)
}

/** The routes in picker order: each running way once, DSM's QuickConnect and DDNS, then DSM's own address. */
export function phoneRoutes(connections: Record<string, ConnectionStatus>, workers: RelayStatus | undefined, nas: PhoneAddNasReply | undefined, here: string | undefined, dsm: DsmRoutes = {}): PhoneRoute[] {
  const ways: PhoneRoute[] = []
  for (const id of ORDER) {
    let urls: string[] = []
    if (id === 'workers') urls = workers?.connected && workers.dsm?.provider === 'workers' ? [workers.dsm.url] : []
    else if (id === 'quickconnect') urls = dsm.quickconnect ? [dsm.quickconnect] : []
    else if (id === 'ddns') urls = dsm.ddns ?? []
    else if (connections[id]?.state === 'connected') urls = addressesOf(connections[id])
    const origin = firstOrigin(urls)
    const route = origin ? routeFor(id, origin) : undefined
    if (route) ways.push(route)
  }
  const external = new Set([...ORDER.flatMap(id => id === 'workers' ? (workers?.dsm?.url ? [workers.dsm.url] : []) : addressesOf(connections[id])), ...(dsm.quickconnect ? [dsm.quickconnect] : []), ...(dsm.ddns ?? [])].map(originOf).filter((u): u is string => !!u).map(hostOf))
  const local = localRoute(nas, here, external)
  return local ? [...ways, local] : ways
}

// The one need a panel names, the one that stops the page first: without
// the app or off the network it does not open; a warning can be passed.
const NEED: PhoneNote[] = ['netbirdApp', 'tailscaleApp', 'sameNetwork', 'selfSigned', 'temporary']
export const routeNeed = (route: PhoneRoute): PhoneNote | undefined => NEED.find(n => route.notes.includes(n))

/** The route the Phone section picks first: a public HTTPS way (Cloudflare first), else the first in order. */
export function bestRoute(routes: PhoneRoute[]): PhoneRoute | undefined {
  return routes.find(r => r.publicHttps) ?? routes[0]
}
