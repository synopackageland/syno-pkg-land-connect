// The Apps page in the user's terms: one row per App, listing the ways it
// opens with. Private ways: Tailscale ("Only my devices", one switch) and
// NetBird (shown only: its devices reach the App port directly). Public is one
// choice: off, the user's own domain (Cloudflare) or the free workers.dev link.
import {rowStatus,type AppRow,type AppsView,type PublicScope,type RowStatus} from './appUrls'

export type Way = 'private' | 'netbird' | 'public' | 'relay'
/** The public choice in the App dialog; an App has at most one public way. */
export type PublicChoice = 'off' | PublicScope

export interface WayUse {way: Way; status: RowStatus; url?: string}

/** The ways set up on this NAS, which an App can open with. */
export interface WaysReady {private: boolean; netbird?: string; public: boolean; relay: boolean}

export function waysReady(view: AppsView | undefined, netbirdHost?: string): WaysReady {
  return {
    private: !!view?.private.available,
    ...(netbirdHost ? {netbird: netbirdHost} : {}),
    public: !!view?.public.available && !view.public.invalid,
    relay: !!view?.relay?.connected && !view.relay.invalid,
  }
}

/** Nothing reaches the NAS from outside yet and no App keeps a way: the page shows its empty state. */
export function nothingSetUp(view: AppsView | undefined, ready: WaysReady): boolean {
  if (!view || ready.private || ready.netbird || ready.public || ready.relay) return false
  // A connected but broken provider still has something to fix, not to set up.
  if (view.public.connected || view.relay?.connected) return false
  return !view.apps.some(a => a.private || a.public || a.relay)
}

/** The ways an App opens with now, or did and needs a click to again: the row's chips. */
export function waysOf(row: AppRow, ready: WaysReady): WayUse[] {
  const out: WayUse[] = []
  for (const way of ['relay', 'public', 'private'] as const) {
    if (!row[way]) continue
    const status = rowStatus(row, way)
    out.push({way, status, ...(row.removed ? {} : {url: row[way]!.url})})
  }
  // NetBird reaches an app port on the NAS itself; a custom app is reached only through Connect.
  if (ready.netbird && !row.removed && !row.custom && !row.dsm) out.push({way: 'netbird', status: 'on', url: netbirdUrl(ready.netbird, row.port,row.entry?.path)})
  return out
}

export const netbirdUrl = (host: string, port: number, path = '/') => `https://${host}:${port}${path}`

/** The public way an App uses, if any. */
export function publicChoice(row: AppRow): PublicChoice {
  return row.relay ? 'relay' : row.public ? 'public' : 'off'
}

// Worst first: the row shows the status that needs the user, else On, else the App's own state.
const order: RowStatus[] = ['cleanup', 'paused', 'removed', 'waiting', 'on', 'needs-update', 'off']

/** One status for the whole row. */
export function rowSummary(row: AppRow, ways: WayUse[]): RowStatus {
  if (row.removed) return 'removed'
  const statuses = ways.map(w => w.status)
  if (!statuses.length) return row.waiting && !row.custom ? 'waiting' : row.needsUpdate ? 'needs-update' : 'off'
  return order.find(s => statuses.includes(s)) ?? 'off'
}

/**
 * The address the row shows. A single-origin App signs in at one address
 * only, so the row never offers a second one: its private URL when it has one
 * (the address its settings take for the private network), else its public one.
 */
export function mainUrl(row: AppRow, ways: WayUse[]): string | undefined {
  const live = ways.filter(w => w.url && w.status !== 'removed' && w.status !== 'cleanup')
  if (row.external?.single_origin) return (live.find(w => w.way === 'private') ?? live.find(w => w.way !== 'netbird'))?.url
  return live[0]?.url
}

/**
 * What saving the dialog does, in order: what goes, then what comes, each
 * with its own confirmation or approval. A public way that changes from one
 * kind to the other is turned off first, because an App has one public way.
 */
export interface Plan {off: Array<'private' | PublicScope>; on: Array<'private' | PublicScope>}

export function planChange(row: AppRow, draft: {private: boolean; public: PublicChoice}): Plan {
  const plan: Plan = {off: [], on: []}
  const now = publicChoice(row)
  if (now !== draft.public) {
    if (now !== 'off') plan.off.push(now)
    if (draft.public !== 'off') plan.on.push(draft.public)
  }
  if (!!row.private !== draft.private) (draft.private ? plan.on : plan.off).push('private')
  return plan
}
