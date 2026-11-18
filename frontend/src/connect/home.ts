// The Home: first visit (nothing set up: Set up opens the wizard) or
// returning (Needs attention, the ways around the topology, Add a way). Which one follows the
// backend's state alone, never this browser's memory.
import {onUnmounted,ref,watch,type Ref} from 'vue'
import type {ConnectionStatus} from '../context'
import {publicWayInUse} from './publicWay'
import {dsmNeedsReview} from './appUrls'
import {METHODS,type Method,type RelayStatus} from './topology'
import type {ConnectState} from './state'
import type {DsmReviewProvider} from './dsmReview'

/** apps.workersstatus: the workers.dev relay in the user's own Cloudflare account. */
export type WorkersStatus = RelayStatus

export type HomeMode = 'loading' | 'unknown' | 'first' | 'returning'

/**
 * loading until the first status and the workers.dev status were read;
 * unknown when the status cannot be read and nothing is known (the shell
 * shows why); returning once any way is set up, workers.dev included.
 */
export function homeMode(o: {read: boolean; unavailable: boolean; lanes: number; workers?: WorkersStatus}): HomeMode {
  if (!o.read) return 'loading'
  if (o.lanes || o.workers?.connected || o.workers?.invalid&&o.workers.usage?.relay!==false) return 'returning'
  return o.unavailable ? 'unknown' : 'first'
}

/** One thing to fix, with its one action: review DSM's certificate, or open the way's page. */
export type AttentionItem =
  | {id: string; kind: 'dsm'; provider: DsmReviewProvider}
  | {id: string; kind: 'way'; provider: Method; state: 'failed' | 'needs-login' | 'needs-machine-auth'; code?: string}
  | {id: string; kind: 'workers'; code?: string}

/**
 * Everything the user has to act on, in one list: it replaces the DSM review
 * callouts and per-page failure callouts on the Home. Nothing is listed while
 * the status cannot be read.
 */
export function attentionItems(connections: Record<string, ConnectionStatus>, workers: WorkersStatus | undefined, unavailable: boolean): AttentionItem[] {
  if (unavailable) return []
  const items: AttentionItem[] = []
  for (const provider of METHODS) {
    const c = connections[provider]
    if (!c) continue
    if(provider==='cloudflare'&&['cloudflare_cert_invalid','cloudflare_reconnect_required'].includes(c.code??'')&&workers?.usage&&!publicWayInUse('public',undefined,workers))continue
    if (dsmNeedsReview(c)) items.push({id: `dsm-${provider}`, kind: 'dsm', provider})
    else if (c.state === 'failed' || c.state === 'needs-login' || c.state === 'needs-machine-auth') items.push({id: provider, kind: 'way', provider, state: c.state, code: c.code})
  }
  if (workers?.invalid&&publicWayInUse('relay',undefined,workers)) items.push({id: 'workers', kind: 'workers', code: workers.code})
  return items
}

/**
 * Reads apps.workersstatus after each completed refresh; read turns true once
 * one answer (or failure) came back. A backend without the relay, or a
 * failed read, leaves it undefined: no lane, no item.
 */
export function useWorkersStatus(s: ConnectState): {workers: Ref<WorkersStatus | undefined>; read: Ref<boolean>} {
  const workers = ref<WorkersStatus>()
  const read = ref(false)
  let disposed = false, epoch = 0
  async function load() {
    const mine = ++epoch
    try {
      const next=await s.ctx.call<WorkersStatus>('apps.workersstatus',{})
      if (!disposed && mine === epoch) workers.value = typeof next?.connected === 'boolean' ? next : undefined
    } catch {
      if (!disposed && mine === epoch) workers.value = undefined
    } finally {
      if (!disposed && mine === epoch) read.value = true
    }
  }
  // The Home can mount before or after the first refresh completes.
  if (s.refreshes.value > 0) void load()
  watch(s.refreshes, () => void load())
  onUnmounted(() => {disposed = true})
  return {workers, read}
}
