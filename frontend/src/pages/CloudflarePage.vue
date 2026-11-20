<script setup lang="ts">
// Cloudflare: everything Cloudflare on one page. Own domain is the tunnel on
// the user's domain and the Cloudflare sign-in behind its App addresses;
// Free link is workers.dev in the user's own account; the two share one
// layout (CloudflareWay) and differ only where they really do. Temporary is
// the quick tunnel the wizard also offers; Apps lists the Apps and DSM either
// opens. TCP services such as Drive Client go over a private network: Own
// domain's footer points there.
import {cloudflareDashboard} from '../connect/nameConflict'
import {computed,ref,watch} from 'vue'
import {Button,FormNote,KeyValueRow,Link,Space} from '@synopackageland/ui'
import ProviderLayout from '../components/ProviderLayout.vue'
import UrlCard from '../components/UrlCard.vue'
import ConnectionCheck from '../components/ConnectionCheck.vue'
import {provideServerStatus} from '../connect/serverStatus'
import ServerDependencyBanner from '../components/ServerDependencyBanner.vue'
import OwnDomain from '../components/OwnDomain.vue'
import AccessEmailList from '../components/AccessEmailList.vue'
import CloudflareApps from '../components/CloudflareApps.vue'
import FreeLink from '../components/FreeLink.vue'
import TemporaryAddress from '../components/TemporaryAddress.vue'
import UploadLimit from '../components/UploadLimit.vue'
import LanSwitchSetting from '../components/LanSwitchSetting.vue'
import ReadyGate from './ReadyGate.vue'
import {useConnectState} from '../connect/state'
import {appsApi,type AppsView} from '../connect/appUrls'
import {freeLinkAddress,freeLinkState,type WorkersStatus} from '../connect/freeLink'
import {dsmWay,wayApps} from '../connect/cloudflareWay'
import type {PageTab} from '../connect/pageTabs'
import {translate} from '../i18n'
const s=useConnectState()
const server=provideServerStatus(s,'cloudflare')
const t=(key:string)=>translate(s.ctx.locale,key)
const status=computed(()=>s.connectionFor('cloudflare'))
const off=computed(()=>['disconnected','failed'].includes(status.value?.state??'disconnected'))
// A stopped connection names its saved own domain; a token's address is not one.
const saved=computed(()=>status.value?.state==='connected'||status.value?.mode==='token'?'':status.value?.hostname??'')
// The own domain's address while it runs: DSM opens there unless the Free link serves DSM.
const openApps=()=>{s.appsFocus.value={};s.go('apps')}

// workers.dev and the Apps on Cloudflare, read when the page opens, on
// Refresh and after a change here; the page renders at once without them.
const workers=ref<WorkersStatus>(),workersLoading=ref(false)
let workersRead=false
const view=ref<AppsView>(),appsLoading=ref(false)
const freeAvailable=computed(()=>workers.value?.code!=='workers_unavailable')
async function loadWorkers(){
 // Only the first read shows as loading, so a later one never unmounts a step being filled in.
 workersLoading.value=!workersRead
 try{const w=await s.ctx.call<WorkersStatus>(appsApi.workersStatus,{});workers.value=typeof w?.connected==='boolean'?w:undefined}catch{/* the tab says it is still reading */}
 finally{workersLoading.value=false;workersRead=true}
}
async function loadApps(){
 if(appsLoading.value)return
 appsLoading.value=true
 try{const v=await s.ctx.call<AppsView>(appsApi.list,{});if(Array.isArray(v?.apps))view.value=v}catch{/* the list stays as it was */}
 finally{appsLoading.value=false}
}
const reload=()=>{void loadWorkers();void loadApps()}
const access=ref<InstanceType<typeof AccessEmailList>>()
watch([()=>s.page.value,()=>s.refreshes.value],([page])=>{if(page==='cloudflare')reload()},{immediate:true})
const dsmOnFreeLink=computed(()=>dsmWay(workers.value)==='workers')
const freeApps=computed(()=>wayApps(view.value,'relay',dsmOnFreeLink.value))
// The Apps tab's DSM row: the way that serves public DSM and its address.
// The Free link runs without the tunnel: while the tunnel is off, the status
// badge follows it, so it never reads Off beside a Free link that is on, and
// the status card shows its address and check in the tunnel's rows: the
// address DSM opens at with copy, Open and QR code, then the connection check
// (through that address). On without DSM it names the account's name only.
const freeStatus=computed(()=>status.value?.state!=='connected'&&freeAvailable.value&&!!workers.value&&freeLinkState(workers.value)!=='off')
const freeWord=computed(()=>{if(!freeAvailable.value||!workers.value)return undefined;const state=freeLinkState(workers.value);return state==='on'?'on' as const:state==='off'?undefined:'attention' as const})

const tabs=computed<PageTab[]>(()=>[
 {value:'domain',label:t('cfpage.tab.domain')},
 ...(freeAvailable.value?[{value:'free',label:t('cfpage.tab.free')}]:[]),
 {value:'temporary',label:t('cfpage.tab.temporary')},
 {value:'apps',label:t('cfpage.tab.apps'),list:true},
])
// The page lands on the Free link when it is the only Cloudflare way in use, once per visit.
const landing=ref('')
watch(()=>s.page.value,()=>{landing.value=''})
watch(workers,w=>{if(!landing.value&&w)landing.value=freeLinkState(w)!=='off'&&off.value&&!saved.value?'free':'domain'})

</script>
<template>
 <ReadyGate><ProviderLayout provider="cloudflare" :tabs="tabs" :initial-tab="landing" :path="false" :stop="false" :other-word="freeWord">
  <template #notice><ServerDependencyBanner :report="server.status.value?.dependencies" :job="server.status.value?.job" :unavailable="server.unavailable.value" @refresh="server.load" /></template>
  <template #status>
   <Space v-if="status?.cleanupNote" spacing="dense" wrap data-cf-cleanup-note><span>{{t('cloudflare.conflict.cleanup')}}</span><Link v-if="cloudflareDashboard(status.cleanupUrl)" :href="cloudflareDashboard(status.cleanupUrl)" external>{{t('cloudflare.conflict.open')}}</Link></Space>
   <template v-if="freeStatus">
    <KeyValueRow :label="t('ux.row.address')" data-cf-free-status>
     <UrlCard v-if="dsmOnFreeLink" :url="workers!.dsm!.url" :locale="s.ctx.locale" :label="t('ux.row.address')" />
     <span v-else>{{freeLinkAddress(workers)}}</span>
    </KeyValueRow>
    <ConnectionCheck v-if="dsmOnFreeLink" :key="workers!.dsm!.url" provider="workers" :relay-state="workers?.relayState" />
   </template>
   <!-- Every way through Cloudflare shares the upload limit: it stays on the Connection tab whatever runs. -->
   <KeyValueRow :label="t('cflimit.row')" data-cf-limit-status><UploadLimit :locale="s.ctx.locale" /></KeyValueRow>
   <!-- Switching to the local network also covers every way through Cloudflare. -->
   <KeyValueRow :label="t('lanswitch.row')" data-cf-lan-switch><LanSwitchSetting :ctx="s.ctx" /></KeyValueRow>
  </template>

  <template #tab-domain>
   <OwnDomain :dependency="server.status.value?.dependencies" :view="view" :dsm-on-free-link="dsmOnFreeLink" @changed="loadApps" />
   <AccessEmailList ref="access" />
   <FormNote data-cf-private-hint>{{t('cfpage.domain.privateHint')}} <Link href="#" data-cf-private-open @click.prevent="s.openWizard('tailscale')">{{t('cfpage.domain.privateUse')}}</Link></FormNote>
  </template>
  <!-- The email list is the tab's one setting: its Reset and Apply stay at the bottom. -->
  <template #apply-domain>
   <Button :disabled="!access?.dirty||access.busy" data-access-reset @click="access?.reset()">{{t('skel.reset')}}</Button>
   <Button variant="primary" :disabled="!access?.dirty||access.busy||access.working" data-access-review @click="access?.review()">{{t('access.apply')}}</Button>
  </template>

  <template #tab-free><FreeLink :status="workers" :apps="freeApps" :loading="workersLoading" @changed="reload" /><FormNote data-cf-free-hint>{{t('cfpage.free.hint')}}</FormNote></template>
  <template #tab-temporary><TemporaryAddress /></template>
  <template #tab-apps><CloudflareApps :view="view" :loading="appsLoading"><Space><Link href="#" data-cf-apps-open @click.prevent="openApps">{{t('cfpage.apps.open')}}</Link></Space></CloudflareApps></template>

 </ProviderLayout></ReadyGate>
</template>
