// The Overview's Phone section: the routes (each running way, DSM's own
// QuickConnect and DDNS addresses, then DSM's own address from phone.addnas,
// read-only, on each Home refresh and never before the first one), the route
// picked in its menu, and whether the section is open. The menu starts on the
// best route (public HTTPS, Cloudflare first) and keeps the user's pick while
// that route lasts. The section starts open; folding it is remembered in this
// browser, after which it starts folded and opens only when asked.
import {computed,ref,watch,type Ref} from 'vue'
import type {ConnectState} from './state'
import {currentLocation,originOf,type PhoneAddNasReply} from './phoneLink'
import {bestRoute,phoneRoutes,type DsmRoutes,type PhoneRoute} from './phoneRoutes'
import type {RelayStatus} from './topology'

export const PHONE_CLOSED_KEY = 'synopkgconnect.phonePanelClosed'
function closedBefore(): boolean {
  try { return globalThis.localStorage?.getItem(PHONE_CLOSED_KEY) === '1' } catch { return false }
}
function rememberClosed() {
  try { globalThis.localStorage?.setItem(PHONE_CLOSED_KEY, '1') } catch { /* storage off: the section starts open again next time */ }
}

export function usePhoneHome(s: Pick<ConnectState, 'ctx' | 'connections' | 'refreshes'>, workers: Ref<RelayStatus | undefined>, dsm: () => DsmRoutes) {
  const nas = ref<PhoneAddNasReply>()
  let epoch = 0
  async function load() {
    const mine = ++epoch
    try {
      const reply = await s.ctx.call<PhoneAddNasReply>('phone.addnas', currentLocation())
      if (mine === epoch) nas.value = Array.isArray(reply?.addresses) ? reply : undefined
    } catch { if (mine === epoch) nas.value = undefined }
  }
  // phone.addnas is for administrators: read it only once the entry gate has let a refresh run
  if (s.refreshes.value > 0) void load()
  watch(s.refreshes, () => void load())
  const here = typeof window === 'undefined' ? undefined : originOf(window.location.origin)
  const routes = computed(() => phoneRoutes(s.connections.value, workers.value, nas.value, here, dsm()))
  /** DSM's own address, as a Local network lane, while no way gives a route. */
  const local = computed<PhoneRoute | undefined>(() => routes.value.every(r => r.id === 'local') ? routes.value[0] : undefined)
  /** The link the user picked; the best route while they have not, or once theirs is gone. */
  const picked = ref<string>()
  const route = computed(() => routes.value.find(r => r.link === picked.value) ?? bestRoute(routes.value))
  function pick(link: string) { picked.value = link }
  const open = ref(!closedBefore())
  function setOpen(now: boolean) {
    if (!now && open.value) rememberClosed()
    open.value = now
  }
  return {routes, local, route, pick, open, setOpen}
}
