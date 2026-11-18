// One approval at a time, for the Apps page's batch and the App dialog alike:
// read the targets first; only reachable Apps that answer their ping get an
// approval (R16a), a not-ready one says so. A public name can be changed in
// the approval and is checked 300 ms after the last key, as the NAS sees it;
// approve() sends exactly what was shown.
import {hostnameIssue} from './hostname'
import {nameConflict,type NameConflict,type NameReuse} from './nameConflict'
import {onUnmounted,ref} from 'vue'
import {appsApi,type Approval,type Inspection,type PublicResult,type Scope} from './appUrls'
import {trustedLoginUrl} from './login'
import {translate} from '../i18n'
import type {ConnectState} from './state'

export interface Pending {scope: Scope; items: Inspection[]; reenable: boolean}

export function useApprovalFlow(s: ConnectState) {
  const t = (key: string) => translate(s.ctx.locale, 'apps.' + key)
  const approval = ref<Pending>()
  const conflict=ref<NameConflict>(),reuses=ref<Record<string,NameReuse>>({})
  const hostnames = ref<Record<string, string>>({}), error = ref(''), checking = ref(false), valid = ref(true), approving = ref(false)
  let timer: ReturnType<typeof setTimeout> | undefined, revision = 0, disposed = false
  onUnmounted(() => {disposed = true; window.clearTimeout(timer); revision++})
  const nameError = (e:unknown) => {conflict.value=nameConflict(e);return conflict.value?'':s.message(e)}

  function reset() {revision++; window.clearTimeout(timer); hostnames.value = {}; error.value = ''; conflict.value=undefined;reuses.value={}; checking.value = false; valid.value = true}

  /** Reads the targets; the names of those not ready yet come back instead of an approval. */
  async function inspect(packages: string[], scope: Scope, reenable = false): Promise<string[]> {
    const items = await Promise.all(packages.map(p => s.ctx.call<Inspection>(appsApi.inspect, {package: p, scope})))
    const waiting = items.filter(i => !i.ready || !i.pin)
    if (waiting.length) return waiting.map(i => i.name)
    reset()
    approval.value = {scope, items, reenable}
    return []
  }

  function changeHostname(pkg: string, value: string) {
    hostnames.value = {...hostnames.value, [pkg]: value}
    error.value = ''; conflict.value=undefined; checking.value = true; valid.value = false
    const current = ++revision
    window.clearTimeout(timer)
    const issue=Object.entries(hostnames.value).map(([pkg,host])=>hostnameIssue(host,approval.value?.items.find(i=>i.package===pkg)?.hostname??approval.value?.items.find(i=>i.dsm)?.dsm?.hostname??host)).find(Boolean)
    if (issue) {
      error.value = t('hostname.'+issue); checking.value = false; return
    }
    timer = window.setTimeout(async () => {
      const a = approval.value
      if (!a) return
      try {
        const items = await Promise.all(a.items.map(i => s.ctx.call<Inspection>(appsApi.inspect, {package: i.package, scope: a.scope, hostname: hostnames.value[i.package] ?? i.hostname, ...(reuses.value[i.package]?{reuseName:reuses.value[i.package]}:{}), ...(hostnames.value[''] ? {dsmHostname: hostnames.value['']} : {})})))
        if (!disposed && current === revision) {
          if (items.some(i => !i.ready || !i.pin)) {error.value = t('notReady'); return}
          approval.value = {...a, items}; valid.value = true
        }
      } catch (e) {if (!disposed && current === revision) error.value = nameError(e)}
      finally {if (!disposed && current === revision) checking.value = false}
    }, 300)
  }

  /**
   * Sends the approval. A public batch answers per App: the failed ones come
   * back by package with their code. false: the NAS refused, error says why.
   */
  async function approve(): Promise<false | Record<string, string>> {
    const a = approval.value
    if (!a || approving.value || checking.value || !valid.value) return false
    approving.value = true
    const approvals: Record<string, Approval> = Object.fromEntries(a.items.map(i => [i.package, {...(i.reuseName?{reuseName:i.reuseName}:{}),...(i.hostname ? {hostname: i.hostname} : {}), port: i.port, pin: i.pin!, ...(i.token ? {token: i.token} : {}), ...(i.dsm ? {dsm: i.dsm} : {})}]))
    try {
      if (a.reenable) {await s.ctx.call(appsApi.reenable, {package: a.items[0]!.package, scope: a.scope, approval: approvals[a.items[0]!.package]}); return {}}
      if (a.scope === 'private') {await s.ctx.call(appsApi.setPrivate, {package: a.items[0]!.package, enabled: true, approval: approvals[a.items[0]!.package]}); return {}}
      const result = await s.ctx.call<PublicResult>(a.scope === 'relay' ? appsApi.setRelay : appsApi.setPublic, {packages: a.items.map(i => i.package), enabled: true, approvals})
      const login = a.scope === 'public' && trustedLoginUrl('cloudflare', result.loginUrl)
      if (login) window.open(login, '_blank', 'noopener,noreferrer')
      return Object.fromEntries((result.results ?? []).filter(r => !r.ok).map(r => [r.package, r.code ?? 'failed']))
    } catch (e) {
      if (!disposed) error.value = nameError(e)
      return false
    } finally {approving.value = false}
  }

  function recoverName(action:'reuse'|'use'|'check',name?:string){
    const c=conflict.value;if(!c)return
    const item=approval.value?.items.find(i=>(hostnames.value[i.package]??i.hostname)===c.conflictName)??approval.value?.items[0]
    if(!item)return
    if(action==='reuse'&&c.reuseRecord&&c.conflictName)reuses.value[item.package]={name:c.conflictName,record:c.reuseRecord}
    changeHostname(item.package,action==='use'?name!:(c.conflictName??hostnames.value[item.package]??item.hostname!))
  }
  return {conflict,recoverName,approval, hostnames, error, checking, valid, approving, inspect, changeHostname, approve, reset}
}
