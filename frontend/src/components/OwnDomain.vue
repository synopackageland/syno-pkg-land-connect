<script setup lang="ts">
// The Own domain tab: the Cloudflare tunnel on the user's domain, on the
// layout it shares with the Free link tab (CloudflareWay). Off, the sign-in
// slot is Authorize in Cloudflare (the name, then the domain picked while
// signing in; no token to paste), after a sign-in already saved on the NAS
// (Reconnect, Disconnect);
// while the temporary address runs it is the name alone. On, the address is
// the one DSM opens at, the Apps row counts DSM with the own-domain Apps, and
// the way's own row is the Cloudflare sign-in with its zone. Change address
// (an own domain only; a token's address is set in Cloudflare) signs in
// again; Turn off stops the tunnel. Once the address runs, DSM's
// External Access may name it (DsmExternal).
import {computed,ref,watch} from 'vue'
import {Button,InfoTip,Space,useToast} from '@synopackageland/ui'
import CloudflareOffDialog from './CloudflareOffDialog.vue'
import {dependencyPathLinks,type DependencyReport} from '../connect/dependency'
import CloudflareWay from './CloudflareWay.vue'
import CloudflareDomain from './CloudflareDomain.vue'
import CloudflareAccount from './CloudflareAccount.vue'
import CloudflareConnectForm from './connect-forms/CloudflareConnectForm.vue'
import DsmExternal from './DsmExternal.vue'
import {useConnectState} from '../connect/state'
import {nameConflict,type NameReuse} from '../connect/nameConflict'
import {settling} from '../connect/checks'
import {publicWayInUse} from '../connect/publicWay'
import {hostOf,wayApps,type WayState} from '../connect/cloudflareWay'
import {appsApi,type AppsView} from '../connect/appUrls'
import {translate} from '../i18n'
const props=defineProps<{dependency?:DependencyReport;view?:AppsView;dsmOnFreeLink?:boolean}>()
const offOpen=ref(false)
const emit=defineEmits<{changed:[]}>()
const s=useConnectState()
const toast=useToast()
const t=(key:string)=>translate(s.ctx.locale,key)
const c=(key:string)=>t('cloudflare.'+key)
const status=computed(()=>s.connectionFor('cloudflare'))
const temporary=computed(()=>status.value?.state==='connected'&&status.value.mode==='quick')
const running=computed(()=>status.value?.state==='connected'&&(status.value.mode==='domain'||status.value.mode==='token'))
// A new own domain shows as setting up while it settles, like the status card.
const state=computed<WayState>(()=>{
 const st=status.value?.state
 if(st==='connected')return temporary.value?'off':settling(s.checks.value.cloudflare)?'pending':'on'
 if(st==='connecting'||st==='needs-login')return 'pending'
 return st==='failed'?'error':'off'
})
// An own domain or token address stays the same, so DSM may advertise it.
const address=computed(()=>running.value?status.value?.hostname||hostOf(status.value?.endpoints?.find(e=>e.public)?.url):'')
const apps=computed(()=>wayApps(props.view,'public',running.value&&!props.dsmOnFreeLink))
const currentName=computed(()=>(status.value?.hostname||hostOf(status.value?.endpoints?.[0]?.url)).split('.')[0]??'')
const conflict=computed(()=>nameConflict(status.value))
// A taken name stays in the field until its alternative is chosen.
const typedName=computed(()=>(conflict.value?.conflictName??(status.value?.mode==='domain'?status.value.hostname:undefined)??status.value?.suggestedName)?.split('.')[0])
const starting=computed(()=>s.starting.value==='cloudflare')
const cleaning=ref(false)
const orphaned=computed(()=>props.view?.public.orphanedRoutes??0)
// A dependency's missing tunnel hop belongs here only while this way should be
// up: turned off on purpose, the path says so and names no failure.
const pathLinks=computed(()=>dependencyPathLinks(props.dependency,state.value==='on'||state.value==='attention'))
// Sending a new name or leaving the page closes the change panel.
const changeKey=ref(0)
watch(()=>s.page.value,()=>{changeKey.value++})
function domain(subdomain:string,reuseName?:NameReuse){
 if(s.busy.value)return
 changeKey.value++
 void s.connectProvider({provider:'cloudflare',mode:'domain',subdomain,...(reuseName?{reuseName}:{}),confirmExposure:true},undefined,conflict.value?false:undefined)
}
const chooseApps=()=>{s.appsFocus.value={};s.go('apps')}
async function cleanupOrphans(){
 if(cleaning.value)return
 cleaning.value=true
 try{
  await s.ctx.call<{removed:number}>(appsApi.cleanupOrphans,{})
  toast.success(c('orphansDone'))
  emit('changed')
 }catch(error){toast.danger(c('orphansFailed'),s.message(error))}
 finally{cleaning.value=false}
}
</script>
<template>
 <CloudflareWay kind="domain" :state="state" :links="pathLinks" :locale="s.ctx.locale" :address="address" :apps="apps" :change-title="c('change')" :turn-off-label="t('cfpage.domain.turnOff')" :busy="s.busy.value" :change-key="changeKey" :stoppable="state!=='off'" data-cf-domain-tab @choose-apps="chooseApps" @turn-off="offOpen=true">
  <template #attention><DsmExternal :hostname="state==='on'?address:''" /></template>
  <template #extra>
   <Space direction="column" spacing="regular">
    <CloudflareAccount :account="view?.public" :in-use="publicWayInUse('public',view)" @changed="emit('changed')" />
    <Space v-if="orphaned" direction="column" spacing="dense" data-cf-orphaned-routes>
     <span>{{c('orphans').replace('{count}',String(orphaned))}}</span>
     <Button :loading="cleaning" :disabled="cleaning" data-cf-cleanup-orphans @click="cleanupOrphans">{{c('cleanupOrphans')}}</Button>
    </Space>
   </Space>
  </template>
  <template #signin>
   <Space v-if="temporary" direction="column" spacing="regular" data-cf-temporary><CloudflareDomain :conflict="conflict" :initial="typedName" :locale="s.ctx.locale" :disabled="s.busy.value" :loading="starting" @connect="domain" /></Space>
   <template v-else><CloudflareAccount :account="view?.public" :in-use="publicWayInUse('public',view)" @changed="emit('changed')" /><CloudflareConnectForm /></template>
  </template>
  <template v-if="status?.mode==='domain'" #change>
   <Space direction="column" spacing="regular" data-cf-change-panel>
    <Space align-items="center" spacing="dense"><span>{{c('changeShort')}}</span><InfoTip icon="info" :content="c('changeHelp')" :aria-label="c('change')" /></Space>
    <CloudflareDomain :conflict="conflict" :locale="s.ctx.locale" :disabled="s.busy.value" :loading="starting" :initial="currentName" @connect="domain" />
   </Space>
  </template>
 </CloudflareWay>
 <CloudflareOffDialog v-model:open="offOpen" @changed="emit('changed')" />
</template>
