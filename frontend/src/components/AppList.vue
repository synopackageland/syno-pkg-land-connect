<script setup lang="ts">
import ConnectionError from './ConnectionError.vue'

// The App list (spec R12): one row per Package Land App on this NAS, with the
// ways it opens with, its address and one status. A row opens the App dialog,
// where every way of that App is changed and approved. Several Apps can be
// made public together in one approval (one cloudflared restart, R16b; one
// batch of Workers). With no way set up yet it shows a picture and one way
// forward, never a table of disabled controls. The list is read only when its
// page opens or is refreshed (P3).
//
// The Apps page shows every way. A provider page's Apps tab passes its way:
// the rows show only that way's chip and address, and open the same dialog.
// "Add app" adds any web service by URL; such a row carries a Custom tag and
// opens with the free link or the user's own domain only.
import {failureToast} from '../connect/errorToast'
import {computed,nextTick,onUnmounted,ref,watch} from 'vue'
import {Button,Callout,CellText,Checkbox,CopyCutField,DataGrid,Dialog,EmptyState,FormActions,IconButton,LoadingIndicator,Space,Tag,useToast} from '@synopackageland/ui'
import {ChevronRight} from '@lucide/vue'
import {useConnectState} from '../connect/state'
import {appsApi,entryChangeTitleKey,listedApps,rowStatus,statusTone,pauseReasons,type AppRow,type AppsView,type PauseReason,type PublicScope,type Scope} from '../connect/appUrls'
import {publicWayInUse} from '../connect/publicWay'
import {mainUrl,nothingSetUp,rowSummary,waysOf,waysReady,type WayUse} from '../connect/appWays'
import {useApprovalFlow} from '../connect/approvalFlow'
import {openFreeLink} from '../connect/freeLink'
import {translate} from '../i18n'
import AppApproval from './AppApproval.vue'
import AppEntryChange from './AppEntryChange.vue'
import AppDialog from './AppDialog.vue'
import AppIcon from './AppIcon.vue'
import NoWayPicture from './NoWayPicture.vue'
import WayPath from './WayPath.vue'
import CustomAppDialog from './CustomAppDialog.vue'
const props=defineProps<{active:boolean;way?:'private'|'netbird'}>()
const s=useConnectState()
const toast=useToast()
const t=(key:string)=>translate(s.ctx.locale,'apps.'+key)
const view=ref<AppsView>()
const loading=ref(false),loadError=ref('')
let disposed=false
onUnmounted(()=>{disposed=true})
// A busy NAS can fail one read (apps_unavailable, a dropped request): the
// page keeps the last list, says it is still reading and asks again after a
// growing pause while it stays open. Only another code is shown as an error.
const transient=(e:unknown)=>{const code=(e as {code?:string})?.code;return !code||code==='apps_unavailable'||code==='network'}
const retrying=ref(false)
let retryTimer:ReturnType<typeof window.setTimeout>|undefined,retries=0
function stopRetry(){if(retryTimer!==undefined)window.clearTimeout(retryTimer);retryTimer=undefined;retrying.value=false;retries=0}
onUnmounted(stopRetry)
function retryLater(){
 retrying.value=true
 const wait=Math.min(2000*2**retries,30000);retries++
 retryTimer=window.setTimeout(()=>{retryTimer=undefined;if(!disposed&&props.active)void load()},wait)
}
async function load(){
 if(loading.value)return
 if(retryTimer!==undefined){window.clearTimeout(retryTimer);retryTimer=undefined}
 loading.value=true
 // Connections still restoring: read again until they are ready.
 try{const next=await s.ctx.call<AppsView>(appsApi.list,{});if(!disposed){view.value=next;loadError.value='';stopRetry();if(next.starting)retryLater()}}
 catch(e){
  if(disposed)return
  if(!transient(e)){stopRetry();loadError.value=s.message(e);return}
  loadError.value='';retryLater()
 }
 finally{if(!disposed)loading.value=false}
}
// The NAS is read when the page opens and on Refresh, never in the background;
// a retry after a busy read stops when the page closes.
watch([()=>props.active,()=>s.refreshes.value],([active])=>{if(active)void load();else stopRetry()},{immediate:true})
// A provider's tab lists the Apps that can still use its way.
const apps=computed(()=>(view.value?.apps??[]).filter(a=>!props.way||!a.removed||props.way==='private'&&!!a.private))
// NetBird's devices reach an App port directly; its address shows once NetBird runs.
const netbirdHost=computed(()=>{const n=s.connectionFor('netbird');return n?.state==='connected'&&n.netbirdFQDN?n.netbirdFQDN:undefined})
const ready=computed(()=>waysReady(view.value,netbirdHost.value))
const empty=computed(()=>!props.way&&nothingSetUp(view.value,ready.value))
const ways=(row:AppRow)=>waysOf(row,ready.value).filter(w=>!props.way||w.way===props.way)
// Custom apps take no private way: a provider tab lists only the Apps that can use it.
const shown=computed(()=>props.way?listedApps({apps:apps.value}).filter(a=>!a.custom):apps.value)
const address=(row:AppRow)=>props.way?ways(row)[0]?.url:mainUrl(row,ways(row))
const setUp=()=>s.openWizard()
// Only the temporary address runs: it opens DSM alone by design, so the empty
// state says so and offers the three ways that carry Apps.
const temporaryOnly=computed(()=>{const c=s.connectionFor('cloudflare');return empty.value&&c?.mode==='quick'&&c.state==='connected'})
const appWays=[{way:'workers',label:'way.chip.relay'},{way:'cloudflare-domain',label:'way.chip.public'},{way:'tailscale',label:'dialog.private'}] as const
// Add app: one dialog for add and edit; a saved app is listed after a reload.
const customOpen=ref(false),customRow=ref<AppRow>()
function addCustom(){customRow.value=undefined;customOpen.value=true}
function editCustom(row:AppRow){dialogOpen.value=false;customRow.value=row;customOpen.value=true}
// The header's "Add app" shows on the Apps page once the list is read and a way is set up.
if(!props.way){
 watch(()=>!!view.value&&!empty.value,on=>{s.customAdd.value=on?addCustom:undefined},{immediate:true})
 onUnmounted(()=>{s.customAdd.value=undefined})
}

// One line under the name, only when the App needs something.
const failures=ref<Record<string,PublicScope>>({})
function rowLine(row:AppRow,used:WayUse[]):string{
 if(failures.value[row.package]&&!row[failures.value[row.package]!])return t(failures.value[row.package]==='relay'?'relay.failed':'public.dnsFailed')
 const entryChange=(['public','relay','private'] as const).map(k=>row[k]?.entryChange).find(c=>c)
 if(entryChange)return t(entryChangeTitleKey(entryChange))
 if(row.custom?.reachable===false)return t('custom.unreachable')
 const status=rowSummary(row,used)
 if(status==='paused'){const way=used.find(w=>w.status==='paused')!.way as Scope;const reason=row[way]?.reason;return t('reason.'+(pauseReasons.includes(reason as PauseReason)?reason:'unknown'))}
 if(['waiting','removed','cleanup','needs-update'].includes(status))return t('line.'+status)
 return ''
}
// A chip that is not simply on carries its status: "Tailscale · Paused".
const chipTone=(w:WayUse)=>w.status==='on'?'neutral':statusTone[w.status]
const chipLabel=(w:WayUse)=>t('way.chip.'+w.way)+(w.status==='on'?'':' · '+t('status.'+w.status))

// The App dialog. A deep link that names an App (the DSM menu's "Set up this
// App in Connect") marks its row and, once a way is set up, opens its dialog;
// nothing is changed without a click.
const repairScope=ref<Scope>()
const dialogPackage=ref(''),dialogOpen=ref(false)
const dialogRow=computed(()=>apps.value.find(a=>a.package===dialogPackage.value))
function openApp(row:AppRow,repair?:Scope){repairScope.value=repair;dialogPackage.value=row.package;dialogOpen.value=true}
const launchFocus=ref('')
watch([()=>s.appsFocus.value,()=>s.page.value,view],async([focus,page])=>{
 if(props.way||!focus||!focus.scope&&!focus.package||page!=='apps'||!view.value)return
 // The free link's "Choose apps" lands on the list, where Apps are picked.
 if(!focus.package)return
 launchFocus.value=focus.package??''
 const row=apps.value.find(a=>a.package===focus.package)
 await nextTick()
 // appsRoute admits only package-name characters, so the name is safe in a selector.
 document.querySelector(`[data-app-row="${focus.package}"]`)?.scrollIntoView({block:'center'})
 if(row&&!empty.value&&!row.removed)openApp(row)
})

// Public for several Apps at once: picked rows, one approval.
const selected=ref<string[]>([])
const selectable=(row:AppRow,scope:PublicScope)=>!row.dsm&&rowStatus(row,scope)==='off'&&!row.public&&!row.relay&&(scope==='relay'?ready.value.relay:ready.value.public)
const batchScopes=computed(()=>props.way?[]:(['relay','public'] as const).filter(scope=>scope==='relay'?ready.value.relay:ready.value.public))
const pickable=(row:AppRow)=>batchScopes.value.some(scope=>selectable(row,scope))
const batchRows=(scope:PublicScope)=>apps.value.filter(r=>selected.value.includes(r.package)&&selectable(r,scope))
function pick(row:AppRow,on:boolean){selected.value=on?[...selected.value,row.package]:selected.value.filter(p=>p!==row.package)}
const batch=useApprovalFlow(s)
const batchOpen=ref(false),batchBusy=ref(false)
async function prepareBatch(scope:PublicScope){
 const rows=batchRows(scope)
 if(!rows.length||batchBusy.value)return
 batchBusy.value=true
 try{
  const waiting=await batch.inspect(rows.map(r=>r.package),scope)
  if(waiting.length)toast.info(t('notReady'),waiting.join(', '))
  else batchOpen.value=true
 }catch(e){if(!disposed)failureToast(toast,s,t('failed'),e)}
 finally{batchBusy.value=false}
}
async function approveBatch(){
 const scope=batch.approval.value?.scope as PublicScope|undefined
 const failed=await batch.approve()
 if(failed===false||!scope)return
 const next={...failures.value}
 for(const pkg of batch.approval.value!.items.map(i=>i.package)){if(failed[pkg])next[pkg]=scope;else delete next[pkg]}
 failures.value=next
 selected.value=[];batchOpen.value=false
 if(!disposed)toast.success(t('applied'))
 await load()
}

// The address is the widest column and takes the room left; "Opens with" is
// as wide as the longest row of chips, estimated from their text (CJK glyphs
// are wider), up to 280px so the address stays the widest; a longer row wraps
// its chips. The user drags any header edge, remembered per list; the pick
// box and the open arrow stay narrow and fixed.
const textWidth=(text:string)=>[...text].reduce((w,c)=>w+(c.charCodeAt(0)>0x2e7f?12:6.5),0)
const chipsWidth=computed(()=>Math.max(0,...shown.value.map(row=>{
 const labels=ways(row).map(chipLabel)
 if(!labels.length)labels.push(t('status.'+rowSummary(row,ways(row))))
 return labels.reduce((w,label)=>w+textWidth(label)+18,0)+8*(labels.length-1)
})))
const withWidth=computed(()=>Math.round(Math.min(280,Math.max(120,chipsWidth.value+24,textWidth(t('page.opensWith'))+24))))
// The pick box column shows only when a listed row can be picked, so the
// header never starts with an empty column.
const columns=computed(()=>[
 ...(shown.value.some(pickable)?[{key:'pick',label:'',width:40,resizable:false,hideable:false}]:[]),
 {key:'app',label:t('col.app'),width:200,minWidth:140,wrap:true},
 {key:'with',label:t('page.opensWith'),width:withWidth.value,minWidth:100,wrap:true},
 {key:'address',label:t('row.address'),minWidth:220},
 {key:'open',label:'',width:48,resizable:false,hideable:false,type:'actions' as const},
])
const tableRows=computed(()=>shown.value.map(r=>({...r}) as unknown as Record<string,unknown>))
const appOf=(row:Record<string,unknown>)=>row as unknown as AppRow

const resetOpen=ref(false),resetting=ref(false)
// Reset keeps the unreadable records as a backup file and starts with every App off.
async function resetRecords(){
 resetting.value=true
 try{await s.ctx.call(appsApi.resetState,{confirm:true});resetOpen.value=false;if(!disposed)toast.success(t('reset.done'))}
 catch(e){if(!disposed)failureToast(toast,s,t('failed'),e)}
 finally{resetting.value=false;await load()}
}

// The Cloudflare sign-in and the free link's token live on the Cloudflare
// page; a broken one only gets a pointer there. Apps on it keep serving.
const attention=computed(()=>props.way?undefined:view.value?.public.invalid&&publicWayInUse('public',view.value)?'public':view.value?.relay?.invalid&&publicWayInUse('relay',view.value)?'relay':undefined)
function fixAttention(){if(attention.value==='relay')openFreeLink(s);else s.go('cloudflare')}
</script>
<template>
 <!-- The list fills the page or tab: the table keeps its header and scrolls its rows. -->
 <Space direction="column" spacing="regular" :data-apps-page="way??''">
  <slot />
   <ConnectionError v-if="loadError" :message="loadError" :title="t('loadFailed')" data-apps-error><template #actions><Button :loading="loading" @click="load">{{t('retry')}}</Button></template></ConnectionError>
   <LoadingIndicator v-if="retrying&&view" :text="t('busy')" data-apps-busy />
   <Callout v-if="view?.stateCorrupt" tone="warning" :title="t('corrupt')" data-apps-corrupt>{{t('corruptLine')}}<template #actions><Button :disabled="resetting" data-apps-reset @click="resetOpen=true">{{t('reset.action')}}</Button></template></Callout>
   <Callout v-if="view?.starting" tone="info" :title="t('starting')" data-apps-starting />
   <LoadingIndicator v-if="(loading||retrying)&&!view" :text="t(retrying?'busy':'loading')" data-apps-loading />
   <span v-else-if="view&&!shown.length" data-apps-none>{{t('empty')}}</span>

   <Space v-else-if="temporaryOnly" direction="column" align-items="center" spacing="regular" data-apps-empty data-apps-temporary-only>
    <WayPath way="cloudflare-quick" phase="on" app="DSM" :locale="s.ctx.locale" />
    <EmptyState :title="t('page.temporaryTitle')" :description="t('page.temporaryLine')">
     <template #actions><Button v-for="w in appWays" :key="w.way" size="sm" :data-apps-way="w.way" @click="s.openWizard(w.way)">{{t(w.label)}}</Button></template>
    </EmptyState>
   </Space>
   <Space v-else-if="empty" direction="column" align-items="center" spacing="regular" data-apps-empty>
    <NoWayPicture :locale="s.ctx.locale" />
    <EmptyState :title="t('page.emptyTitle')" :description="apps.map(a=>a.name).join(' · ')">
     <template #actions><Button variant="primary" data-apps-setup @click="setUp">{{t('page.setUp')}}</Button></template>
    </EmptyState>
   </Space>

   <template v-else-if="view">
    <DataGrid :columns="columns" :rows="tableRows" row-key="package" :state-key="`apps-${way??'all'}`" :footer="false" :page-size="1000" data-apps-table>
     <template #cell-pick="{row}"><Checkbox v-if="pickable(appOf(row))" :model-value="selected.includes(appOf(row).package)" :aria-label="`${t('public.pick')}: ${appOf(row).name}`" :data-app-pick="appOf(row).package" @update:model-value="pick(appOf(row),!!$event)" /></template>
     <template #cell-app="{row}">
      <Space align-items="center" spacing="compact" :data-app-row="appOf(row).package" :data-status="rowSummary(appOf(row),ways(appOf(row)))" :data-launch-focus="launchFocus===appOf(row).package||undefined">
       <AppIcon :src="appOf(row).icon" :dsm="appOf(row).dsm" />
       <CellText v-if="!appOf(row).custom" :text="appOf(row).name" :secondary="rowLine(appOf(row),ways(appOf(row)))" wrap :data-app-name="appOf(row).package" />
       <!-- A custom row keeps its name and tag on one line and its status on one line below. -->
       <CellText v-else :secondary="rowLine(appOf(row),ways(appOf(row)))" :title="rowLine(appOf(row),ways(appOf(row)))||undefined" :data-app-name="appOf(row).package">
        <Space align-items="center" spacing="compact"><span>{{appOf(row).name}}</span><Tag :label="t('custom.tag')" appearance="neutral" :data-app-custom="appOf(row).package" /></Space>
       </CellText>
      </Space>
      <AppEntryChange v-for="scope in (['public','relay','private'] as const).filter(k=>appOf(row)[k]?.entryChange)" :key="scope" :locale="s.ctx.locale" :change="appOf(row)[scope]!.entryChange!" @update="openApp(appOf(row),scope)" />
     </template>
     <template #cell-with="{row}">
      <Space spacing="compact" wrap data-app-ways>
       <Tag v-for="w in ways(appOf(row))" :key="w.way" :label="chipLabel(w)" :appearance="chipTone(w)" tooltip :data-app-way="w.way" :data-way-status="w.status" />
       <Tag v-if="!ways(appOf(row)).length" :label="t('status.'+rowSummary(appOf(row),ways(appOf(row))))" :appearance="statusTone[rowSummary(appOf(row),ways(appOf(row)))]" data-app-status />
      </Space>
     </template>
     <template #cell-address="{row}"><CopyCutField v-if="address(appOf(row))" :model-value="address(appOf(row))!" :copy-label="t('copy')" :aria-label="`${t('row.address')}: ${appOf(row).name}`" data-app-url /></template>
     <template #cell-open="{row}"><IconButton variant="ghost" :label="`${t('page.open')}: ${appOf(row).name}`" :data-app-open="appOf(row).package" @click="openApp(appOf(row))"><ChevronRight /></IconButton></template>
    </DataGrid>
    <FormActions v-if="selected.length" data-apps-batch>
     <Button v-for="scope in batchScopes" :key="scope" :variant="batchScopes.length===1?'primary':'secondary'" :disabled="!batchRows(scope).length||batchBusy" :loading="batchBusy" :data-apps-batch-create="scope" @click="prepareBatch(scope)">{{t('page.makePublic.'+scope)}}</Button>
    </FormActions>
   </template>

   <Callout v-if="attention" tone="warning" :title="t(attention==='relay'?'relay.invalid':'cf.invalid')" :data-apps-attention="attention"><template #actions><Button data-apps-attention-open @click="fixAttention">{{t('public.open')}}</Button></template></Callout>

  <AppDialog v-if="dialogRow" v-model:open="dialogOpen" :row="dialogRow" :repair-scope="repairScope" :ready="ready" :private-host="view?.private.host" :private-code="view?.private.code" @changed="load" @edit="editCustom" />
  <CustomAppDialog v-if="!way" v-model:open="customOpen" :row="customRow" @saved="load" @removed="load" @choose="id=>{const row=apps.find(a=>a.package===id);if(row)openApp(row)}" />
  <AppApproval v-if="batch.approval.value" v-model:open="batchOpen" :locale="s.ctx.locale" :scope="batch.approval.value.scope" :items="batch.approval.value.items" :busy="batch.approving.value" :checking="batch.checking.value||!batch.valid.value" :hostnames="batch.hostnames.value" :error="batch.error.value" :conflict="batch.conflict.value" @recover="batch.recoverName" @hostname="batch.changeHostname" @approve="approveBatch" />
  <Dialog v-model:open="resetOpen" :title="t('reset.title')" data-apps-reset-dialog>
   <Space direction="column" spacing="dense"><span>{{t('reset.line')}}</span><span>{{t('reset.backup')}}</span></Space>
   <template #footer><Button :disabled="resetting" @click="resetOpen=false">{{t('cancel')}}</Button><Button variant="danger" :loading="resetting" :disabled="resetting" data-apps-reset-confirm @click="resetRecords">{{t('reset.action')}}</Button></template>
  </Dialog>
 </Space>
</template>
<style scoped>
[data-launch-focus]{outline:2px solid var(--ui-focus-ring,var(--ui-primary));outline-offset:4px;border-radius:4px}
</style>
