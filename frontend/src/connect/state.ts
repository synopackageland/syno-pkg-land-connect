// Shared connection state for the DSM shell pages. One instance is created per
// DSM window mount, so no user or session state leaks between mounts.
import {inject, onMounted, onUnmounted, provide, ref, shallowRef, watch, type InjectionKey, type Ref} from 'vue'
import {activePage, followLaunchRoute, type Context} from '@synopackageland/sdk'
import type {AppStatus, ConnectionStatus, PortSnapshot} from '../context'
import {translate, uiLocale} from '../i18n'
import {portText} from '../port-i18n'
import {appsRoute,type AppsView} from './appUrls'
import {shellPages, type ShellPage} from '../shell/pages'
import type {WizardWay} from './wizard'
import {REJECTED_CODE,replacesSafely,type RejectedChange} from './rejected'
import {checkConnection,checkPollDelay,type CheckResult} from './checks'
import {githubText,isGitHubCode,type GitHubCode} from './github'
import {needsSignIn, failLoginTab, openLoginTab, sendLoginTab, trustedLoginUrl, type LoginFallback, type LoginPage, type LoginTab} from './login'

export interface ConnectState {
  dsmErrorProvider: Ref<string>
  dsmReviewAction: Ref<(()=>void)|undefined>
  trustedDsm: Ref<AppsView['trustedDsm']>
  ctx: Context
  app: Ref<AppStatus | undefined>
  statusUnavailable: Ref<boolean>
  status: Ref<ConnectionStatus | undefined>
  connections: Ref<Record<string,ConnectionStatus>>
  connectionFor(id:string): ConnectionStatus | undefined
  prepareServices(ids:string[]):Promise<void>
  disconnectProvider(id:string):Promise<void>
  busy: Ref<boolean>
  /** Counts completed refreshes, so a page can redo its own checks. */
  refreshes: Ref<number>
  downloading: Ref<string>
  /** The provider whose start is running, so its own button shows the progress. */
  starting: Ref<string>
  /** Where a sign-in tab could not take the user, so the page offers the address. */
  loginFallback: Ref<LoginFallback | undefined>
  error: Ref<string>
  notice: Ref<string>
  /** The last connection check each provider page read, for the pictures beside it. */
  checks: Ref<Record<string, CheckResult | undefined>>
  check(provider:string):Promise<CheckResult>
  clearCheck(provider:string):void
  /** The last change the running connection could not keep; shown beside it, never as its status. */
  rejected: Ref<RejectedChange | undefined>
  /** Sends the rejected change again, accepting that the connection drops while it switches. */
  applyAnyway(): void
  dismissRejected(provider?: string): void
  token: Ref<string>
  provider: Ref<string>
  page: Ref<ShellPage>
  pages: readonly ShellPage[]
  ipv6Confirmed: Ref<boolean>
  portView: Ref<PortSnapshot | undefined>
  refresh(): Promise<void>
  /** signIn opens the sign-in tab for a start needsSignIn cannot tell from params. */
  connectProvider(params: Record<string, unknown>,serviceIds?:string[],signIn?:boolean): Promise<void>
  /** Where go() asked the page to open, such as its tab. */
  guide: Ref<Guide | undefined>
  go(target: ShellPage, guide?: Guide): void
  /** The section (and later the App) the App URLs page brings into view. */
  appsFocus: Ref<AppsFocus | undefined>
  /** The setup wizard: one dialog, opened from the Overview and App URLs. */
  wizardOpen: Ref<boolean>
  /** The way the wizard selects when it opens; unset, it resumes from the backend's state. */
  wizardWay: Ref<WizardWay | undefined>
  openWizard(way?: WizardWay): void
  /** Counts requests to unfold the Overview's comparison ("Compare all ways" in the wizard). */
  compareRequests: Ref<number>
  /** Each Connect your phone from the header: the Overview opens its Phone section and scrolls to it. */
  phoneRequests: Ref<number>
  /** "Add app" in the page header: set by the App URLs list while adding is possible. */
  customAdd: Ref<(() => void) | undefined>
  message(error: unknown): string
  /** The GitHub refusal behind a message shown, so its callout offers "Add a GitHub token". */
  githubAction(text: string): GitHubCode | undefined
  /** Opens Settings at the GitHub token field. */
  openGitHubToken(): void
  portsResolved(view: PortSnapshot | undefined): void
}

// Where a page opens when another page sends the user there: tab is the
// page tab it opens on.
export interface Guide {tab?: string}
// What the App URLs page brings into view, in the deep link's terms
// (route /apps/<package>/<scope>): a scope alone shows its section.
export interface AppsFocus {package?: string; scope?: 'private' | 'public' | 'workers'}

const programs: Record<string, string> = {cloudflare: 'cloudflared', tailscale: 'tailscale', netbird: 'netbird'}
const providerNames: Record<string, string> = {cloudflare: 'Cloudflare', tailscale: 'Tailscale', netbird: 'NetBird'}

const ConnectStateKey: InjectionKey<ConnectState> = Symbol('connect-state')
// DSM refused the caller: forget what it saw and show DSM's own reason.
const refusals = ['session_required', 'credential_not_ready', 'admin_required']
const refusal = (e: unknown): string | undefined => {const code = (e as {code?: string})?.code; return code && refusals.includes(code) ? code : undefined}

export function provideConnectState(ctx: Context): ConnectState {
  const app = ref<AppStatus>()
  const statusUnavailable=ref(false)
  const status = ref<ConnectionStatus>()
  const connections=ref<Record<string,ConnectionStatus>>({})
  const connectionFor=(id:string)=>connections.value[id]
  function ingest(s:ConnectionStatus){
    statusUnavailable.value=false
    status.value=s
    trustedDsm.value=s.trustedDsm
    if(s.connections) connections.value=Object.fromEntries(s.connections.map(c=>[c.provider,c]))
    else if(s.provider!=="fake") connections.value={...connections.value,[s.provider]:s}
  }
  const busy = ref(false)
  const refreshes = ref(0)
  const downloading = ref('')
  const starting = ref('')
  const loginFallback = ref<LoginFallback>()
  let loginTab: LoginTab | undefined
  const error = ref('')
  const notice = ref('')
  const rejected = ref<RejectedChange>()
  const checks = ref<Record<string, CheckResult | undefined>>({})
  const checkTimers=new Map<string,ReturnType<typeof window.setTimeout>>()
  const checkRequests=new Map<string,Promise<CheckResult>>()
  const checkEpochs=new Map<string,number>()
  function clearCheck(provider:string){
    checkEpochs.set(provider,(checkEpochs.get(provider)??0)+1)
    window.clearTimeout(checkTimers.get(provider));checkTimers.delete(provider);checkRequests.delete(provider)
    const next={...checks.value};delete next[provider];checks.value=next
  }
  function check(provider:string):Promise<CheckResult>{
    const pending=checkRequests.get(provider);if(pending)return pending
    window.clearTimeout(checkTimers.get(provider));checkTimers.delete(provider)
    const epoch=checkEpochs.get(provider)??0
    const request=checkConnection(ctx,provider).then(result=>{
      if(!disposed&&(checkEpochs.get(provider)??0)===epoch){
        checks.value={...checks.value,[provider]:result}
        const delay=checkPollDelay(result)
        if(delay!==undefined)checkTimers.set(provider,window.setTimeout(()=>void check(provider),delay))
      }
      return result
    }).finally(()=>{if(checkRequests.get(provider)===request)checkRequests.delete(provider)})
    checkRequests.set(provider,request);return request
  }
  onUnmounted(()=>{for(const timer of checkTimers.values())window.clearTimeout(timer)})

  const dsmErrorProvider=ref('')
  const dsmReviewAction=shallowRef<()=>void>()
  const trustedDsm=ref<AppsView['trustedDsm']>()
  const token = ref('')
  const provider = ref('fake')
  const pages = shellPages()
  const page = ref<ShellPage>(activePage('status', pages))
  const ipv6Confirmed = ref(false)
  const portView = ref<PortSnapshot>()
  let disposed = false
  let statusEpoch=0
  let polling=false,timer:ReturnType<typeof window.setInterval>|undefined
  async function pollStatus(){
    if(disposed||busy.value||polling||!app.value)return
    polling=true
    const epoch=statusEpoch
    try{const latest=await ctx.call<ConnectionStatus>('connection.status');if(!disposed&&!busy.value&&epoch===statusEpoch)ingest(latest)}
    catch(e){if(!disposed&&epoch===statusEpoch){statusUnavailable.value=true;if((e as {code?: string})?.code==='connect_stopped')error.value=message(e);if(refusal(e)){app.value=undefined;connections.value={};token.value=''}}}
    finally{polling=false}
  }

  const t = (key: string) => translate(ctx.locale, `provider.${key}`)
  const u = (key: string) => translate(ctx.locale, `ux.${key}`)

  // Users see only translated text: a code without its own message reads as the generic error.
  function message(e: unknown): string {
    const text = codeMessage(e)
    return /\b[a-z0-9]+(?:_[a-z0-9]+)+\b|^[\w-]+(?:\.[\w-]+)+$/.test(text) ? u('error.generic') : text
  }
  // The last GitHub refusal put into words, so the callout showing it can
  // offer the GitHub token.
  let lastGitHub: {code: GitHubCode; text: string} | undefined
  function githubAction(text: string): GitHubCode | undefined {
    return text && lastGitHub?.text === text ? lastGitHub.code : undefined
  }
  function openGitHubToken(): void {
    wizardOpen.value = false
    go('settings', {tab: 'github-token'})
  }
  function codeMessage(e: unknown): string {
    const code = (e as {code?: string})?.code
    if (isGitHubCode(code)) {
      const at = (e as {retryAt?: string}).retryAt
      const text = githubText(key => translate(ctx.locale, key), when => new Intl.DateTimeFormat(uiLocale(ctx.locale), {dateStyle: 'medium', timeStyle: 'short'}).format(new Date(when)), code, at)
      lastGitHub = {code, text}
      return text
    }
    if(code==='pin_proxy_required'){const failed=Object.values(connections.value).filter(c=>c.code===code);dsmErrorProvider.value=provider.value==='tailscale'||failed.length===1&&failed[0]?.provider==='tailscale'?'tailscale':'cloudflare'}
    if(code?.startsWith('apply_'))return translate(ctx.locale,`apply.code.${code}`)
    if (code === 'netbird_cloudflare_in_use') return translate(ctx.locale, 'dependency.changeBlocked')
    if (code === 'connect_stopped') return translate(ctx.locale, 'error.connect_stopped')
    if (code === 'port_detection_unavailable') return portText(ctx.locale, 'ports.error.detection')
    if (code === 'ports_required') return portText(ctx.locale, 'ports.ipv6ActivateHelp')
    if (code === 'binary_required') return t('binaryRequired')
    if (code === 'download_failed') return translate(ctx.locale, 'connect.downloadFailed')
    if (code === 'invalid_cloudflare_token') return t('invalidCloudflareToken')
    if (code?.startsWith('cloudflare_')) return translate(ctx.locale, `cloudflare.code.${code}`)
    if (code === 'pin_proxy_required' || code?.startsWith('apps_')) return translate(ctx.locale, `apps.code.${code}`)
    if (code?.startsWith('custom_')) return translate(ctx.locale, `apps.custom.code.${code}`)
    if (code === 'start_failed' || code?.endsWith('_start_failed')) return t('startFailed')
    if (code?.endsWith('_stop_failed')) return t('stopFailed')
    if (provider.value === 'tailscale' || provider.value === 'ipv6' || provider.value === 'netbird') {
      return refusal(e) ? translate(ctx.locale, `error.${code}`) : t('failed')
    }
    if (refusal(e)) return translate(ctx.locale, `error.${code}`)
    const known: Record<string, string> = {invalid_demo_token: 'demoToken', provider_unavailable: 'provider', settings_unavailable: 'settings'}
    return u(`error.${known[code || ''] || 'generic'}`)
  }

  // Another accepted start or stop is still running: that is progress, not a failure.
  function refused(e: unknown, params: Record<string, unknown> = {}, signIn = false): void {
    const code = (e as {code?: string})?.code
    if (code === REJECTED_CODE && typeof params.provider === 'string') rejected.value = {provider: params.provider, params, signIn}
    else if (code === 'connect_busy') notice.value = t('connecting')
    else if (code === 'disconnect_busy') notice.value = t('disconnecting')
    else error.value = message(e)
  }

  async function refresh(): Promise<void> {
    if (busy.value || disposed) return
    statusEpoch++
    busy.value = true; error.value = ''
    try {
      const a = await ctx.call<AppStatus>('app.status')
      if (disposed) return
      app.value = a
      const s = await ctx.call<ConnectionStatus>('connection.status')
      if (disposed) return
      ingest(s)
      provider.value = s.provider
      if(!Object.values(connections.value).some(c=>c.code==='pin_proxy_required'))dsmErrorProvider.value=''
      try {const v=await ctx.call<PortSnapshot>('ports.list');if(!disposed&&v?.entries)portsResolved(v)}catch{if(!disposed)portsResolved(undefined)}
      if (!disposed) refreshes.value++
    } catch (e) {
      if (!disposed) {
        statusUnavailable.value=true
        error.value = message(e)
        if(refusal(e)){app.value=undefined;status.value=undefined;connections.value={};token.value=''}
      }
    } finally {
      if (!disposed) busy.value = false
    }
  }

  async function connectProvider(params: Record<string, unknown>,serviceIds?:string[],signIn=false): Promise<void> {
    if (busy.value || disposed) return
    statusEpoch++
    busy.value = true; error.value = ''; notice.value = ''; rejected.value = undefined
    if (typeof params.provider === 'string') provider.value = params.provider
    starting.value = String(params.provider ?? '')
    startLogin(params,signIn)
    try {
      if(serviceIds) await prepareServices(serviceIds)
      const s = await connectReplacing(params)
      if (!disposed) ingest(s)
    } catch (e) {
      if (!disposed) refused(e, params, signIn)
      failSignIn(message(e))
    } finally {
      if (!disposed) {busy.value = false; starting.value = ''}
    }
  }

  // Runs within the click, before any await, so the browser allows the tab.
  function startLogin(params: Record<string, unknown>,signIn=false): void {
    closeLoginTab()
    loginFallback.value = undefined
    if(params.retry===true)return
    if (!signIn && !needsSignIn(params)) return
    const id = String(params.provider)
    loginTab = openLoginTab(id, loginPage(id)) ?? undefined
    if (!loginTab) loginFallback.value = {provider: id, reason: 'blocked'}
  }

  function loginPage(id: string): LoginPage {
    const text = (key: string) => translate(ctx.locale, `connect.login.${key}`).replace('{provider}', providerNames[id] ?? id)
    return {lang: uiLocale(ctx.locale), title: text('preparing'), wait: text('preparingWait'), next: text('preparingNext')}
  }

  function failSignIn(reason: string): void {
    if (!loginTab) return
    failLoginTab(loginTab, {lang: uiLocale(ctx.locale), title: translate(ctx.locale, 'connect.login.failed'), wait: reason, next: translate(ctx.locale, 'connect.login.failedNext')})
    loginTab = undefined
  }

  function closeLoginTab(): void {
    loginTab?.tab.close()
    loginTab = undefined
  }

  // The tab follows the connection: it is sent to a trusted sign-in page, or
  // closed once no sign-in is needed or the address cannot be trusted. A tab
  // the user already closed leaves the page to offer the address instead.
  watch(connections, all => {
    const c = loginTab && all[loginTab.provider]
    if (!loginTab || !c) return
    if (c.state === 'connecting' || c.state === 'unknown') return
    if (c.state === 'failed') return failSignIn(message({code: c.code}))
    if (c.state !== 'needs-login') return closeLoginTab()
    if(c.provider==='netbird'&&c.phase==='trust-login-origin')return closeLoginTab()
    if (!c.loginUrl) return
    const url = trustedLoginUrl(loginTab.provider, c.loginUrl,loginTab.provider==='tailscale'||loginTab.provider==='netbird'?c:c.userCode,c.loginOrigins)
    if (!url) return closeLoginTab()
    if (loginTab.tab.closed) loginFallback.value = {provider: loginTab.provider, reason: 'closed'}
    else sendLoginTab(loginTab, url)
    loginTab = undefined
  })

  // A provider whose program is not downloaded yet downloads it in place and
  // connects again; IPv6 needs no program.
  // A change the running connection cannot keep goes ahead by reconnecting
  // when that loses nothing (replacesSafely); otherwise the page asks.
  async function connectReplacing(params: Record<string, unknown>): Promise<ConnectionStatus> {
    try {
      return await connectWithProgram(params)
    } catch (e) {
      const current = connections.value[String(params.provider)]
      if ((e as {code?: string})?.code !== REJECTED_CODE || params.replace || !replacesSafely(params, current, window.location.host)) throw e
      return await connectWithProgram({...params, replace: true})
    }
  }

  function applyAnyway(): void {
    const r = rejected.value
    if (!r) return
    void connectProvider({...r.params, replace: true}, undefined, r.signIn)
  }
  function dismissRejected(id?: string): void {
    if (!id || rejected.value?.provider === id) rejected.value = undefined
  }

  async function connectWithProgram(params: Record<string, unknown>): Promise<ConnectionStatus> {
    try {
      return await ctx.call<ConnectionStatus>('connection.connect', params)
    } catch (e) {
      const product = programs[String(params.provider)]
      if ((e as {code?: string})?.code !== 'binary_required' || !product) throw e
      await download(product, String(params.provider))
      return await ctx.call<ConnectionStatus>('connection.connect', params)
    }
  }

  // A download GitHub refused keeps its reason (rate limit and reset time, or
  // a rejected token), so the page can point to the GitHub token.
  async function download(product: string, id: string): Promise<void> {
    downloading.value = id
    type Binary = {busy?: boolean; current?: string; errorCode?: string; retryAt?: string}
    let p: Binary | undefined
    try {
      p = await ctx.call<Binary>('binaries.update', {product})
      while (p.busy && !disposed) {
        await new Promise(resolve => window.setTimeout(resolve, 1500))
        p = await ctx.call<Binary>('binaries.status', {product})
      }
    } catch {
      p = undefined
    } finally {
      if (!disposed) downloading.value = ''
    }
    if (p && (p.busy || p.current)) return
    if (isGitHubCode(p?.errorCode)) throw {code: p.errorCode, retryAt: p.retryAt}
    throw {code: 'download_failed'}
  }

  async function prepareServices(ids:string[]):Promise<void> {
    for(const id of ids){
      const entry=portView.value?.entries.find(e=>e.id===id)
      if(!entry||entry.port===null||!['ready','disabled'].includes(entry.state))throw {code:'ports_required'}
      if(entry.enabled)continue
      const params=entry.source==='dsm'?{id,enabled:true,confirmExposure:true}:{id,name:entry.name,port:entry.port,protocol:entry.protocol,enabled:true,confirmExposure:true}
      const result=await ctx.call<PortSnapshot>(entry.source==='dsm'?'ports.dsmset':'ports.upsert',params)
      portsResolved(result)
    }
  }

  async function disconnectProvider(id:string):Promise<void> {
    if(busy.value||disposed)return
    statusEpoch++
    closeLoginTab()
    busy.value=true;error.value='';notice.value=''
    try {const s=await ctx.call<ConnectionStatus>('connection.disconnect',{provider:id});if(!disposed)ingest(s)}
    catch(e){if(!disposed)refused(e)}
    finally{if(!disposed)busy.value=false}
  }

  const guide = ref<Guide>()
  const appsFocus = ref<AppsFocus>()
  const wizardOpen = ref(false)
  const wizardWay = ref<WizardWay>()
  function openWizard(way?: WizardWay): void {
    wizardWay.value = way
    wizardOpen.value = true
  }
  const compareRequests = ref(0)
  const phoneRequests = ref(0)
  const customAdd = shallowRef<() => void>()
  function go(target: ShellPage, next?: Guide): void {
    guide.value = next
    page.value = target
  }
  // The DSM menu launcher links an App it cannot open to its row here, as
  // /apps/<package>/<scope>, both when it opens Connect and when Connect is
  // already open. The row is only brought into view; nothing is changed.
  const stopFollowing = followLaunchRoute(ctx, route => {
    const focus = appsRoute(route)
    if (!focus || disposed) return
    appsFocus.value = {...focus}
    go('apps')
  })
  onUnmounted(stopFollowing)

  function portsResolved(view: PortSnapshot | undefined): void {
    if(view && portView.value && view.revision<portView.value.revision)return
    portView.value = view
  }

  // A transient secret or consent never survives leaving its page: pages stay
  // mounted for polling, so the reset is explicit rather than an unmount.
  watch(page, () => { token.value = ''; ipv6Confirmed.value = false; rejected.value = undefined })

  function schedulePoll():void {
    const waiting=Object.values(connections.value).some(c=>c.provider==='netbird'&&['connecting','needs-login'].includes(c.state))
    timer=window.setTimeout(async()=>{await pollStatus();if(!disposed)schedulePoll()},waiting?2000:5000)
  }
  onMounted(() => {
    ctx.reopen(() => { if (!disposed) void refresh() })
    void refresh()
    schedulePoll()
  })
  onUnmounted(() => {
    disposed = true; window.clearInterval(timer); closeLoginTab(); token.value = ''; status.value = undefined
  })

  const state: ConnectState = {
    ctx, dsmErrorProvider, dsmReviewAction, trustedDsm,
    app, statusUnavailable, status, connections, connectionFor, prepareServices, disconnectProvider, busy, refreshes, downloading, starting, loginFallback, error, notice, checks, check, clearCheck, rejected, applyAnyway, dismissRejected, token, provider, page, pages,
    ipv6Confirmed, portView,
    guide, appsFocus, wizardOpen, wizardWay, openWizard, compareRequests, phoneRequests, customAdd, refresh, connectProvider, go, message, githubAction, openGitHubToken, portsResolved,
  }
  provide(ConnectStateKey, state)
  return state
}

export function useConnectState(): ConnectState {
  const state = inject(ConnectStateKey)
  if (!state) throw new Error('ConnectState is not provided')
  return state
}
