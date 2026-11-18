<script setup lang="ts">
// The Home. While nothing is set up it is the first visit: the picture of
// your devices and the NAS with no way between, and Set up, which opens the
// setup wizard dialog. Once any way is, it is the returning view: Needs
// attention (only while something is wrong), then one topology lane per way,
// workers.dev included, with its addresses under the lane, and Add a way (the
// same wizard). A temporary address says under its lane that it changes on
// restart and offers the Free link. DSM's own QuickConnect and DDNS join the
// topology, read only, once DSM answers; their lanes open Control Panel >
// External Access. "Compare all ways" stays folded at the bottom of both.
// Which view shows follows the backend's state alone. Every lane with an
// address lists it the same way (copy, open, QR), QuickConnect and DDNS
// included. Under the topology, the Phone section picks one of those
// addresses for the phone browser page; while no way gives one, a Local
// network lane on DSM's own address does.
import {computed,nextTick,ref,watch,type Component} from 'vue'
import {Button,Callout,ConnectionTopology,DataTable,EmptyState,Link,SectionSkeleton,SettingsSection,Space,Tag,useToast,type TagAppearance,type TopologyLane,type TopologyNode} from '@synopackageland/ui'
import {Cloud,Globe,KeyRound,Link2,Monitor,Network,Router,Server,Shield,Signpost,Smartphone,Users,Waypoints,Wifi} from '@lucide/vue'
import {useConnectState} from '../connect/state'
import {checkReason,settling} from '../connect/checks'
import {cloudflareNode} from '../connect/cloudflareNode'
import {openFreeLink} from '../connect/freeLink'
import {METHODS,RELAY,overviewLanes,relayLane,type LaneSpec,type Method} from '../connect/topology'
import {COLUMNS,comparisonRows,optionFor} from '../connect/advisor'
import {homeMode,useWorkersStatus} from '../connect/home'
import {DDNS,QUICKCONNECT,ddnsLane,quickConnectLane,useDsmAccess,type DdnsStatus} from '../connect/dsmAccess'
import {openDdnsSettings,openQuickConnectSettings} from '../dsm-navigation'
import {translate} from '../i18n'
import type {ShellPage} from '../shell/pages'
import UrlCard from '../components/UrlCard.vue'
import NeedsAttention from '../components/NeedsAttention.vue'
import NoWayPicture from '../components/NoWayPicture.vue'
import SettingUp from '../components/SettingUp.vue'
import {usePhoneHome} from '../connect/phoneHome'
import PhoneSection from '../components/phone/PhoneSection.vue'
import ReadyGate from './ReadyGate.vue'
const s=useConnectState()
const t=(key:string)=>translate(s.ctx.locale,key)
type Lane=Method|'workers'|typeof QUICKCONNECT|typeof DDNS
const name=(method:string)=>method==='ipv6'?t('connect.ipv6'):method==='workers'?t('home.way.workers'):method===QUICKCONNECT?'QuickConnect':method===DDNS?'DDNS':t('ux.method.'+method)
const icons:Record<Lane,Component>={cloudflare:Cloud,workers:Link2,tailscale:Network,netbird:Shield,ipv6:Globe,quickconnect:Waypoints,ddns:Signpost}

// Checks of running methods: once when a method starts running and again
// after each page refresh; one still running is asked again every 2 s, one
// still settling every 5 s.
const checks=s.checks
const check=s.check
const {workers,read:workersRead}=useWorkersStatus(s)
const running=computed(()=>[...METHODS.filter(id=>s.connectionFor(id)?.state==='connected'),...(workers.value?.connected&&workers.value.dsm?.provider==='workers'?['workers']:[])])
watch(running,(now,before=[])=>{
 for(const id of before)if(!now.includes(id))s.clearCheck(id)
 for(const id of now)if(!before.includes(id))void check(id)
},{immediate:true})
watch(()=>workers.value?.dsm?.url,(url,before)=>{if(before&&url!==before){s.clearCheck('workers');if(running.value.includes('workers'))void check('workers')}})
watch(s.refreshes,()=>{for(const id of running.value)void check(id)})

const ways=computed(()=>{
 const all=overviewLanes(s.connections.value,checks.value,s.statusUnavailable.value)
 const relay=relayLane(workers.value,s.statusUnavailable.value,checks.value.workers)
 if(!relay)return all
 // workers.dev sits after Cloudflare, whose account it uses.
 const at=all[0]?.provider==='cloudflare'?1:0
 return [...all.slice(0,at),relay,...all.slice(at)]
})
// DSM's own ways come last and never make a first visit a returning one.
const dsm=useDsmAccess(s.ctx,s.refreshes)
const lanes=computed(()=>[...ways.value,...[quickConnectLane(dsm.value.quickconnect),ddnsLane(dsm.value.ddns)].filter(l=>!!l)])
const mode=computed(()=>homeMode({read:(s.refreshes.value>0&&workersRead.value)||s.statusUnavailable.value,unavailable:s.statusUnavailable.value,lanes:ways.value.length,workers:workers.value}))
// Phone: one section under the topology with a menu of every way's address;
// while no way runs, a Local network lane on DSM's own address.
const phone=usePhoneHome(s,workers,()=>({quickconnect:dsm.value.quickconnect?.url,ddns:dsm.value.ddns.flatMap(r=>r.url?[r.url]:[])}))
// The header's Connect your phone opens the section and brings it into view.
const phoneSection=ref<HTMLElement>()
watch(s.phoneRequests,()=>{phone.setOpen(true);void nextTick(()=>phoneSection.value?.scrollIntoView({block:'start',behavior:'smooth'}))})
const LOCAL='local'
const localLane=computed<TopologyLane|undefined>(()=>!running.value.length&&phone.local.value?{id:LOCAL,source:{id:'from-local',label:t('overview.node.yourDevices'),icon:Smartphone},via:{id:'via-local',label:t('phone.route.local'),icon:Wifi},links:[{state:'active',label:t('phone.local.reach')},{state:'active',label:t('overview.nas.running')}]}:undefined)
// The NAS name comes from the backend, even when this window uses a public address.
const target=computed<TopologyNode>(()=>({id:'nas',label:t('overview.node.nas'),caption:s.app.value?.nasName||undefined,icon:Server}))
const sources:Record<Lane,{label:string;icon:Component}>={cloudflare:{label:'overview.node.anyBrowser',icon:Monitor},workers:{label:'overview.node.anyBrowser',icon:Monitor},tailscale:{label:'overview.node.yourDevices',icon:Smartphone},netbird:{label:'overview.node.yourDevices',icon:Smartphone},ipv6:{label:'overview.node.anyBrowser',icon:Monitor},quickconnect:{label:'overview.node.anyoneWithId',icon:Users},ddns:{label:'overview.node.anyBrowser',icon:Monitor}}
function topologyLane(lane:LaneSpec):TopologyLane {
 const id=lane.provider as Lane
 const via=id==='cloudflare'||id==='workers'?cloudflareNode(id==='workers'?'free':'domain',s.ctx.locale):id==='ipv6'?{label:t('overview.node.router'),icon:Router}:{label:name(id),caption:dsmCaption(id),icon:icons[id]}
 const [left,right]=lane.links.map(l=>({state:l.state,label:t(l.label),icon:l.icon==='key'?KeyRound:undefined}))
 // The daemon's own count of online devices, shown only when it reported one.
 const peers=lane.peersOnline===undefined?undefined:t('overview.peersOnline').replace('{count}',String(lane.peersOnline))
 return {id,source:{id:`from-${id}`,label:t(sources[id].label),caption:peers,icon:sources[id].icon},via:{id:`via-${id}`,...via},links:[left,right]}
}
const topology=computed(()=>[...lanes.value.map(topologyLane),...(localLane.value?[localLane.value]:[])])
// Why a running lane is red: its check failed after the settling window,
// with Check again. A way that failed or waits for the user is under Needs
// attention, with its action.
function reason(provider:string):string {
 const c=s.connectionFor(provider)
 if(s.statusUnavailable.value||(provider==='workers'?!workers.value?.connected:c?.state!=='connected'))return ''
 const result=checks.value[provider]
 return result?.state==='failed'?checkReason(s.ctx.locale,result.code):''
}
// workers.dev lives on the Cloudflare page's Free link tab; QuickConnect and
// DDNS in DSM's Control Panel > External Access, which a window outside DSM
// cannot open: then a toast names where.
const toast=useToast()
function go(id:string){
 if(id===LOCAL)return
 if(id===QUICKCONNECT||id===DDNS){
  if(!(id===QUICKCONNECT?openQuickConnectSettings(s.ctx):openDdnsSettings(s.ctx)))toast.info(t('home.dsm.openInDsm'))
  return
 }
 if(id!==RELAY)return s.go(id as ShellPage)
 openFreeLink(s)
}
// The QuickConnect ID, or the one DDNS hostname; several are listed under the lane.
function dsmCaption(id:Lane):string|undefined {
 if(id===QUICKCONNECT)return dsm.value.quickconnect?.id||undefined
 if(id!==DDNS)return undefined
 const records=dsm.value.ddns
 return records.length===1?records[0].hostname:t('home.ddns.records').replace('{count}',String(records.length))
}
const ddnsColumns=computed(()=>(['hostname','provider','status','ip'] as const).map(key=>({key,label:t(`home.ddns.${key}`)})))
const ddnsRows=computed(()=>dsm.value.ddns.map((r,i)=>({id:String(i),...r})))
const ddnsTone:Record<DdnsStatus,TagAppearance>={normal:'success',updating:'warning',failed:'danger'}
// A temporary address changes on restart; its lane offers the Free link.
const temporary=(provider:string)=>provider==='cloudflare'&&s.connectionFor('cloudflare')?.state==='connected'&&s.connectionFor('cloudflare')?.mode==='quick'
// Compare all ways: folded at the bottom; the wizard opens it and brings it into view.
const compareOpen=ref(false)
const compare=ref<HTMLElement>()
watch(s.compareRequests,()=>{compareOpen.value=true;void nextTick(()=>compare.value?.scrollIntoView({block:'start',behavior:'smooth'}))})
// Fixed widths sized to each column's longest text, so a long cell wraps in its own column and the table scrolls sideways.
const widths:Record<typeof COLUMNS[number],string>={option:'11em',protection:'10em',gate:'13em',fit:'13em',reach:'11em',scope:'10em',cost:'8em',accounts:'16em',client:'10em',difficulty:'7em',who:'14em',via:'12em',https:'16em',public:'5em',pairWith:'16em',limits:'20em'}
const columns=computed(()=>COLUMNS.map(key=>({key,label:t(`advisor.column.${key}`),width:widths[key]})))
const rows=computed(()=>comparisonRows(s.ctx.locale,t))

// What a running private network still needs: the app on each other device.
const download=(provider:string)=>provider==='tailscale'||provider==='netbird'?optionFor(provider).links?.download:undefined
</script>
<template>
 <ReadyGate><Space direction="column" spacing="regular" :data-home-mode="mode">
  <SectionSkeleton v-if="mode==='loading'" :lines="3" />
  <Space v-else-if="mode==='first'" class="home-first" direction="column" align-items="center" spacing="none" data-home-first>
   <NoWayPicture :locale="s.ctx.locale" />
   <EmptyState :title="t('wizard.title')">
    <template #actions><Button variant="primary" data-home-set-up @click="s.openWizard()">{{t('home.setUp')}}</Button></template>
   </EmptyState>
  </Space>
  <ConnectionTopology v-if="mode==='first'&&localLane" :target="target" :lanes="[localLane]" :aria-label="t('phone.route.local')" data-home-local>
   <template #lane-footer-local><UrlCard :url="phone.local.value!.origin" :locale="s.ctx.locale" :label="`${t('phone.route.local')} ${t('ux.row.address')}`" /></template>
  </ConnectionTopology>
  <template v-else-if="mode==='returning'">
   <NeedsAttention :workers="workers" />
   <Space direction="column" spacing="regular" data-overview>
    <ConnectionTopology :target="target" :lanes="topology" :aria-label="t('overview.title')" @select-lane="go">
     <template v-for="lane in lanes" :key="lane.provider" #[`lane-footer-${lane.provider}`]>
      <Space direction="column" spacing="dense">
       <SettingUp v-if="running.includes(lane.provider)&&settling(checks[lane.provider])" :locale="s.ctx.locale" />
       <Space v-if="reason(lane.provider)" align-items="center" spacing="dense" wrap><span data-lane-reason>{{reason(lane.provider)}}</span><Button data-lane-check @click="check(lane.provider)">{{t('connect.check.again')}}</Button></Space>
       <span v-if="download(lane.provider)&&s.connectionFor(lane.provider)?.state==='connected'" data-lane-next>{{t('overview.next.'+lane.provider)}} <Link :href="download(lane.provider)!" external>{{t('overview.download')}}</Link></span>
       <UrlCard v-for="address in lane.addresses" :key="address" :url="address" :locale="s.ctx.locale" :label="`${name(lane.provider)} ${t('ux.row.address')}`" />
       <DataTable v-if="lane.provider==='ddns'&&dsm.ddns.length" :columns="ddnsColumns" :rows="ddnsRows" row-key="id" compact :fill="false" mobile-view="auto" data-lane-ddns>
        <template #cell-status="{row}"><Tag :label="t('home.ddns.status.'+row.status)" :appearance="ddnsTone[row.status as DdnsStatus]" /></template>
       </DataTable>
       <Callout v-if="temporary(lane.provider)" tone="warning" variant="inline" data-lane-temporary>{{t('temporary.changes')}}<template #actions><Button data-lane-free-link @click="s.openWizard('workers')">{{t('home.switchFreeLink')}}</Button></template></Callout>
      </Space>
     </template>
     <template v-if="localLane" #lane-footer-local><UrlCard :url="phone.local.value!.origin" :locale="s.ctx.locale" :label="`${t('phone.route.local')} ${t('ux.row.address')}`" /></template>
    </ConnectionTopology>
    <Space><Button data-home-add-way @click="s.openWizard()">{{t('home.addWay')}}</Button></Space>
   </Space>
  </template>
  <div v-if="(mode==='first'||mode==='returning')&&phone.routes.value.length" ref="phoneSection"><PhoneSection :locale="s.ctx.locale" :routes="phone.routes.value" :route="phone.route.value" :open="phone.open.value" @pick="phone.pick" @update:open="phone.setOpen" /></div>
  <div ref="compare"><SettingsSection v-model:open="compareOpen" :title="t('home.compare')" data-choice-notes data-home-compare>
   <p>{{t('connect.home.help')}}</p>
   <DataTable :columns="columns" :rows="rows" row-key="id" mobile-view="auto" compact :fill="false" data-advisor-table />
   <p data-advisor-https-note>{{t('advisor.httpsNote')}}</p>
  </SettingsSection></div>
 </Space></ReadyGate>
</template>
<style scoped>
/* The first visit: the picture, the heading and Set up as one centred block. */
.home-first{padding-block:var(--ui-space-6) var(--ui-space-4)}
.home-first :deep([data-no-way-picture]){margin-bottom:var(--ui-space-2)}
</style>
