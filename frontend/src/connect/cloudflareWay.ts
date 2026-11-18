// What the Cloudflare page's Own domain and Free link tabs share: one state
// set and how each tab counts the Apps it opens, DSM included.
import type {AppsView} from './appUrls'
import type {WorkersStatus} from './freeLink'

/** off: not set up; pending: setting up; on; attention: on but needs the user; error: setting up failed. */
export type WayState = 'off' | 'pending' | 'on' | 'attention' | 'error'

/** The host of an https:// address, for the address row. */
export const hostOf = (url = ''): string => url.replace(/^https:\/\//, '').replace(/\/.*$/, '')

/** Where public DSM opens, as the Free link status names it. */
export type DsmWay = 'workers' | 'cloudflare' | undefined
export const dsmWay = (status: WorkersStatus | undefined): DsmWay =>
  status?.dsm?.provider === 'workers' ? 'workers' : status?.dsm?.provider ? 'cloudflare' : undefined

/**
 * The Apps a way opens: each App with that way, and DSM when that way
 * serves it. DSM is public on one way at a time.
 */
export function wayApps(view: AppsView | undefined, scope: 'public' | 'relay', dsm: boolean): number | undefined {
  if (!view) return undefined
  return view.apps.filter(a => a[scope]).length + (dsm&&!view.apps.some(a=>a.dsm&&a[scope]) ? 1 : 0)
}
