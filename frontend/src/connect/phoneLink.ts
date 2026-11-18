// "Connect your phone": the add-NAS QR code (format v1) the Syno Package Land
// mobile app scans to add this NAS with every address it can use.
//
//   synopkgland://add-nas#<base64url, no padding, of UTF-8 JSON>
//   {"v":1,"name":"<DSM server name>","urls":[{"url":"https://host:port","kind":"lan"|"cf"|"netbird"|"other","sha256":"<leaf DER SHA-256, self-signed/untrusted https only>"}]}
//
// urls are origins in order of preference (LAN first, then external), 1–8 of
// them, and the payload JSON is at most 2 KB. The code carries addresses only:
// never a token, password, cookie, session or one-time code.
import type {ConnectionStatus} from '../context'
import type {RelayStatus} from './topology'

export const ADD_NAS_SCHEME = 'synopkgland://add-nas#'
export const MAX_URLS = 8
export const MAX_PAYLOAD_BYTES = 2048

export type PhoneKind = 'lan' | 'cf' | 'netbird' | 'other'
/** Where an address comes from; the dialog's label and hint follow it. It never enters the code. */
export type PhoneVia = 'lan' | 'cloudflare' | 'workers' | 'netbird' | 'tailscale' | 'current'
export interface PhoneAddress {url: string; kind: PhoneKind; sha256?: string; via: PhoneVia}
export interface PayloadUrl {url: string; kind: PhoneKind; sha256?: string}
export interface AddNasPayload {v: 1; name: string; urls: PayloadUrl[]}

/** phone.addnas: DSM's name and LAN origins (kind lan), and the browser's own address off the LAN (kind other). */
export interface PhoneAddNasReply {name: string; ports: boolean; addresses: Array<{url: string; kind: 'lan' | 'other'; sha256?: string}>}

/** The origin of an http(s) URL without credentials, or undefined. */
export function originOf(raw: string): string | undefined {
  let u: URL
  try { u = new URL(raw) } catch { return undefined }
  if ((u.protocol !== 'https:' && u.protocol !== 'http:') || u.username || u.password || !u.hostname) return undefined
  return u.origin
}

const hexSha = /^[0-9a-f]{64}$/
const isIP = (host: string) => /^\[.*\]$/.test(host) || /^\d{1,3}(\.\d{1,3}){3}$/.test(host)
// A name before an IP address: names survive an address change.
const namesFirst = (urls: string[]) => [...urls].sort((a, b) => Number(isIP(new URL(a).hostname)) - Number(isIP(new URL(b).hostname)))

function connected(c: ConnectionStatus | undefined): c is ConnectionStatus {
  return c?.state === 'connected'
}
function endpointOrigins(c: ConnectionStatus, keep: (url: string) => boolean = () => true): string[] {
  return namesFirst([...new Set((c.endpoints ?? []).map(e => originOf(e.url)).filter((u): u is string => !!u && keep(u)))])
}

/**
 * DSM's external addresses from the ways Connect runs now: the Cloudflare
 * hostname, DSM's workers.dev address, then NetBird and Tailscale. A way that
 * is off or not connected adds nothing. A temporary Cloudflare address is
 * left out: the phone keeps the addresses it saves, and that one changes on
 * restart.
 */
export function externalAddresses(connections: Record<string, ConnectionStatus>, workers?: RelayStatus): PhoneAddress[] {
  const out: PhoneAddress[] = []
  const cf = connections.cloudflare
  if (connected(cf) && cf.mode !== 'quick') for (const url of endpointOrigins(cf, u => u.startsWith('https:') && !temporaryHost(new URL(u).hostname))) out.push({url, kind: 'cf', via: 'cloudflare'})
  if (workers?.connected && workers.dsm?.provider === 'workers') {
    const url = originOf(workers.dsm.url)
    if (url) out.push({url, kind: 'cf', via: 'workers'})
  }
  const nb = connections.netbird
  if (connected(nb)) for (const url of endpointOrigins(nb)) out.push({url, kind: 'netbird', via: 'netbird'})
  const ts = connections.tailscale
  if (connected(ts)) for (const url of endpointOrigins(ts)) out.push({url, kind: 'other', via: 'tailscale'})
  return out
}

/** A Cloudflare quick tunnel's hostname, which changes on every restart. */
export const temporaryHost = (host: string) => host === 'trycloudflare.com' || host.endsWith('.trycloudflare.com')

/** The temporary Cloudflare address Connect serves now, which the code leaves out. */
export function temporaryAddress(connections: Record<string, ConnectionStatus>): string | undefined {
  const cf = connections.cloudflare
  if (!connected(cf)) return undefined
  return cf.mode === 'quick' ? endpointOrigins(cf)[0] : endpointOrigins(cf, u => temporaryHost(new URL(u).hostname))[0]
}

/** UTF-8 JSON, base64url without padding. */
export function encodePayload(payload: AddNasPayload): string {
  const bytes = new TextEncoder().encode(JSON.stringify(payload))
  let bin = ''
  for (const b of bytes) bin += String.fromCharCode(b)
  return btoa(bin).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}
export function payloadBytes(payload: AddNasPayload): number {
  return new TextEncoder().encode(JSON.stringify(payload)).length
}
export const addNasLink = (payload: AddNasPayload) => ADD_NAS_SCHEME + encodePayload(payload)

const toPayloadUrl = (a: PhoneAddress): PayloadUrl => (a.sha256 && hexSha.test(a.sha256) && a.url.startsWith('https:') ? {url: a.url, kind: a.kind, sha256: a.sha256} : {url: a.url, kind: a.kind})

export interface PhoneCode {payload: AddNasPayload; link: string; addresses: PhoneAddress[]}

/**
 * The code for this NAS: LAN addresses first, then the external ways, then
 * the browser's own address when it is none of those. Each origin once, at
 * most 8, trimmed from the end until the payload fits 2 KB. undefined when
 * no address is left.
 */
export function phoneCode(name: string, nas: PhoneAddNasReply | undefined, external: PhoneAddress[]): PhoneCode | undefined {
  const lan: PhoneAddress[] = [], other: PhoneAddress[] = []
  for (const a of nas?.addresses ?? []) {
    const url = originOf(a.url)
    if (!url) continue
    const sha256 = a.sha256 && hexSha.test(a.sha256) ? a.sha256 : undefined
    ;(a.kind === 'lan' ? lan : other).push({url, kind: a.kind === 'lan' ? 'lan' : 'other', via: a.kind === 'lan' ? 'lan' : 'current', ...(sha256 ? {sha256} : {})})
  }
  const externalHosts = new Set(external.map(a => new URL(a.url).hostname))
  // The browser's own address is dropped when it is a way listed above or a temporary Cloudflare address.
  const keepOther = (a: PhoneAddress) => !externalHosts.has(new URL(a.url).hostname) && !temporaryHost(new URL(a.url).hostname)
  const seen = new Set<string>()
  let addresses = [...lan, ...external, ...other.filter(keepOther)].filter(a => !seen.has(a.url) && !!seen.add(a.url)).slice(0, MAX_URLS)
  const make = (list: PhoneAddress[]): AddNasPayload => ({v: 1, name: name.trim() || 'NAS', urls: list.map(toPayloadUrl)})
  while (addresses.length && payloadBytes(make(addresses)) > MAX_PAYLOAD_BYTES) addresses = addresses.slice(0, -1)
  if (!addresses.length) return undefined
  const payload = make(addresses)
  return {payload, link: addNasLink(payload), addresses}
}

/** The browser's own address, which phone.addnas checks against the NAS's networks. */
export function currentLocation(loc: Pick<Location, 'protocol' | 'hostname' | 'port'> = window.location) {
  const port = Number(loc.port)
  return {protocol: loc.protocol, host: loc.hostname, ...(Number.isInteger(port) && port > 0 ? {port} : {})}
}

/**
 * The address Connect shows, copies and puts in QR codes. DSM's nginx
 * redirects /m (ui.short_paths) to the stable page
 * /webman/3rdparty/SynoPkgConnect/m/index.html (ui.stable_pages), which
 * forwards to m/ in the current build, so a saved link or home-screen icon
 * keeps working after Connect is updated. The long address still works.
 */
export const BROWSER_PATH = '/m'
export const browserPath = (): string => BROWSER_PATH
/** The phone browser page on one of this NAS's addresses. */
export const browserLink = (origin: string, path: string = browserPath()) => origin.replace(/\/+$/, '') + path
