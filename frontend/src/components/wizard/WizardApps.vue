<script setup lang="ts">
import ConnectionError from '../ConnectionError.vue'

// Step 3: what opens with the way. Every App that can is listed with a
// checkbox (video Apps never start checked on the Free link), DSM on top.
// Continue reads each target once and shows one approval for all of them;
// DSM goes with the first App, or alone when no App is chosen. A public way
// also shows whether DSM's own protections are on, read in the background.
import {hostnameIssue} from '../../connect/hostname'
import {nameConflict,type NameConflict,type NameReuse} from '../../connect/nameConflict'
import NameConflictRow from '../NameConflictRow.vue'
import {computed,onMounted,onUnmounted,ref} from 'vue'
import {Checkbox,LoadingIndicator,Space,Tag} from '@synopackageland/ui'
import {useConnectState} from '../../connect/state'
import {appsApi,listedApps,otherPublic,rowStatus,type AppRow,type AppsView,type Approval,type DsmInspection,type Inspection,type PublicResult,type Scope} from '../../connect/appUrls'
import {trustedLoginUrl} from '../../connect/login'
import {APP_SCOPE,type WizardWay} from '../../connect/wizard'
import {translate} from '../../i18n'
import AppApproval from '../AppApproval.vue'
import ProtectionChecks from '../ProtectionChecks.vue'
const props=defineProps<{way:WizardWay}>()
const emit=defineEmits<{done:[dsmHost?:string]}>()
const s=useConnectState()
const t=(key:string)=>translate(s.ctx.locale,'wizard.'+key)
const a=(key:string)=>translate(s.ctx.locale,'apps.'+key)
const scope=computed<Scope>(()=>APP_SCOPE[props.way]??'private')
const pub=computed(()=>scope.value!=='private')
let disposed=false
onUnmounted(()=>{disposed=true;window.clearTimeout(nameTimer);nameRevision++})

// The list and DSM's state are read once when the step opens.
const view=ref<AppsView>(),dsmReview=ref<DsmInspection>(),loading=ref(true),loadError=ref('')
const picked=ref<Record<string,boolean>>({}),dsmPicked=ref(true)
onMounted(async()=>{
 try{
  const [list,dsm]=await Promise.all([
   s.ctx.call<AppsView>(appsApi.list,{}),
   s.ctx.call<DsmInspection>(appsApi.inspectDsm,{scope:scope.value}).catch(()=>undefined),
  ])
  if(disposed)return
  view.value=list;dsmReview.value=dsm
  picked.value=Object.fromEntries(listedApps(list).filter(r=>available(r)&&!(scope.value==='relay'&&r.media)).map(r=>[r.package,true]))
 }catch(e){if(!disposed)loadError.value=s.message(e)}
 finally{if(!disposed)loading.value=false}
})
// Why an App cannot be chosen here, if anything: on already, on the other
// public way, or a state the Apps page explains.
function blocked(row:AppRow):string {
 if(scope.value!=='private'){const other=otherPublic(row,scope.value);if(other)return t('apps.otherWay').replace('{way}',t(other==='relay'?'way.workers':'way.cloudflare-domain'))}
 const status=rowStatus(row,scope.value)
 return status==='off'||status==='on'?'':a('status.'+status)
}
const available=(row:AppRow)=>rowStatus(row,scope.value)==='off'&&!blocked(row)
// DSM has its own row above, so the App list leaves it out.
const rows=computed(()=>listedApps(view.value))
const chosen=computed(()=>rows.value.filter(r=>available(r)&&picked.value[r.package]))
// DSM: on already when nothing is left to review; it goes with the Apps.
const dsmOn=computed(()=>!!dsmReview.value&&!dsmReview.value.dsm)
const dsmForced=computed(()=>!dsmOn.value&&chosen.value.length>0)
const dsmAlone=computed(()=>!chosen.value.length&&!dsmOn.value&&dsmPicked.value&&!!dsmReview.value?.dsm)

// One approval for everything chosen.
const approval=ref<{items:Inspection[];dsmOnly?:DsmInspection}>()
const open=ref(false),preparing=ref(false),approving=ref(false),error=ref('')
const conflict=ref<NameConflict>(),reuses=ref<Record<string,NameReuse>>({})
const hostnames=ref<Record<string,string>>({}),checking=ref(false),namesValid=ref(true)
let nameTimer:ReturnType<typeof setTimeout>|undefined,nameRevision=0
const inspectApp=(pkg:string,hostname?:string)=>s.ctx.call<Inspection>(appsApi.inspect,{package:pkg,scope:scope.value,...(hostname?{hostname}:{}),...(reuses.value[pkg]?{reuseName:reuses.value[pkg]}:{}),...(hostnames.value['']?{dsmHostname:hostnames.value['']}:{})})
const inspectDsm=()=>s.ctx.call<DsmInspection>(appsApi.inspectDsm,{scope:scope.value,...(hostnames.value['']?{hostname:hostnames.value['']}:{})})
async function next(){
 if(!chosen.value.length&&!dsmAlone.value)return emit('done')
 preparing.value=true;error.value=''
 nameRevision++;window.clearTimeout(nameTimer);hostnames.value={};checking.value=false;namesValid.value=true
 try{
  if(dsmAlone.value){const d=await inspectDsm();if(disposed)return;if(!d.dsm)return emit('done');approval.value={items:[],dsmOnly:d}}
  else{
   const items=await Promise.all(chosen.value.map(r=>inspectApp(r.package)))
   if(disposed)return
   const waiting=items.filter(i=>!i.ready||!i.pin)
   if(waiting.length){error.value=`${a('notReady')}: ${waiting.map(i=>i.name).join(', ')}`;return}
   approval.value={items}
  }
  open.value=true
 }catch(e){if(!disposed)error.value=s.message(e)}
 finally{if(!disposed)preparing.value=false}
}
// The wizard footer draws the main action: Continue opens the approval,
// Skip goes to the addresses.
defineExpose({next,label:computed(()=>t(chosen.value.length||dsmAlone.value?'apps.review':'apps.skip')),blocked:computed(()=>loading.value||preparing.value)})
const nameError=(e:unknown)=>{conflict.value=nameConflict(e);return conflict.value?'':s.message(e)}
// A changed name is checked after a short pause and read again with the
// targets, so what the dialog shows is what gets approved.
function changeHostname(pkg:string,value:string){
 hostnames.value={...hostnames.value,[pkg]:value}
 error.value='';conflict.value=undefined;checking.value=true;namesValid.value=false
 const revision=++nameRevision
 window.clearTimeout(nameTimer)
 const issue=Object.entries(hostnames.value).map(([pkg,host])=>hostnameIssue(host,approval.value?.items.find(i=>i.package===pkg)?.hostname??approval.value?.dsmOnly?.dsm?.hostname??approval.value?.items.find(i=>i.dsm)?.dsm?.hostname??host)).find(Boolean)
 if(issue){
  error.value=translate(s.ctx.locale,'apps.hostname.'+issue);checking.value=false;return
 }
 nameTimer=window.setTimeout(async()=>{
  const current=approval.value
  if(!current)return
  try{
   if(current.dsmOnly){const d=await inspectDsm();if(!disposed&&revision===nameRevision&&d.dsm){approval.value={items:[],dsmOnly:d};namesValid.value=true};return}
   const items=await Promise.all(current.items.map(i=>inspectApp(i.package,hostnames.value[i.package]??i.hostname)))
   if(!disposed&&revision===nameRevision){if(items.some(i=>!i.ready||!i.pin)){error.value=a('notReady');return};approval.value={items};namesValid.value=true}
  }catch(e){if(!disposed&&revision===nameRevision)error.value=nameError(e)}
  finally{if(!disposed&&revision===nameRevision)checking.value=false}
 },300)
}
function recoverName(action:'reuse'|'use'|'check',name?:string){
 const c=conflict.value;if(!c)return
 const pkg=approval.value?.items.find(i=>(hostnames.value[i.package]??i.hostname)===c.conflictName)?.package??approval.value?.items[0]?.package??''
 if(action==='reuse'&&c.conflictName&&c.reuseRecord)reuses.value[pkg]={name:c.conflictName,record:c.reuseRecord}
 changeHostname(pkg,action==='use'?name!:c.conflictName!)
}
const approvalOf=(i:Inspection):Approval=>({...(i.reuseName?{reuseName:i.reuseName}:{}),...(i.hostname?{hostname:i.hostname}:{}),port:i.port,pin:i.pin!,...(i.token?{token:i.token}:{}),...(i.dsm?{dsm:i.dsm}:{})})
const same=(x:Inspection,y:Inspection)=>x.port===y.port&&JSON.stringify(x.pin)===JSON.stringify(y.pin)
async function approve(){
 const current=approval.value
 if(!current||approving.value||checking.value||!namesValid.value)return
 approving.value=true;error.value=''
 const dsmHost=current.dsmOnly?.dsm?.hostname??current.items.find(i=>i.dsm)?.dsm?.hostname
 try{
  if(current.dsmOnly)await s.ctx.call(appsApi.setDsm,{scope:scope.value,dsm:current.dsmOnly.dsm,token:current.dsmOnly.token,...(hostnames.value['']?{hostname:hostnames.value['']}:{})})
  else if(scope.value==='private'){
   // One App per call: each later App is read again for a fresh token and
   // goes on only while its port and certificate are what was approved.
   for(const [index,item] of current.items.entries()){
    const fresh=index?await inspectApp(item.package):item
    if(!fresh.ready||!fresh.pin||!same(fresh,item))throw new Error(`${a('notReady')}: ${item.name}`)
    await s.ctx.call(appsApi.setPrivate,{package:item.package,enabled:true,approval:approvalOf(fresh)})
   }
  }else{
   const approvals=Object.fromEntries(current.items.map(i=>[i.package,approvalOf(i)]))
   const result=await s.ctx.call<PublicResult>(scope.value==='relay'?appsApi.setRelay:appsApi.setPublic,{packages:current.items.map(i=>i.package),enabled:true,approvals})
   const login=scope.value==='public'&&trustedLoginUrl('cloudflare',result.loginUrl)
   if(login)window.open(login,'_blank','noopener,noreferrer')
   const failed=(result.results??[]).filter(r=>!r.ok).map(r=>current.items.find(i=>i.package===r.package)?.name??r.package)
   if(failed.length)throw new Error(`${a('failed')}: ${failed.join(', ')}`)
  }
  if(disposed)return
  open.value=false
  emit('done',dsmHost)
 }catch(e){if(!disposed)error.value=e instanceof Error&&!(e as {code?:string}).code?e.message:nameError(e)}
 finally{if(!disposed)approving.value=false}
}
</script>
<template>
 <Space direction="column" spacing="regular" data-wizard-apps :data-scope="scope">
  <LoadingIndicator v-if="loading" />
  <ConnectionError v-else-if="loadError" :message="loadError" :scope="scope" data-wizard-apps-error />
  <template v-else>
   <Space v-if="dsmReview" direction="column" spacing="dense" data-wizard-dsm :data-dsm-state="dsmOn?'on':dsmForced?'with-apps':dsmPicked?'picked':'off'">
    <Space align-items="center" spacing="dense" wrap>
     <Checkbox :model-value="dsmOn||dsmForced||dsmPicked" :disabled="dsmOn||dsmForced||preparing" :label="t('apps.dsm')" @update:model-value="dsmPicked=$event" />
     <Tag v-if="dsmOn" :label="a('status.on')" appearance="success" />
     <span v-else-if="dsmForced" data-wizard-dsm-with>{{t('apps.dsmWith')}}</span>
    </Space>
    <ProtectionChecks v-if="pub" />
   </Space>
   <Space direction="column" spacing="dense" data-wizard-app-list>
    <Space v-for="row in rows" :key="row.package" align-items="center" spacing="dense" wrap :data-wizard-app="row.package">
     <Checkbox :model-value="rowStatus(row,scope)==='on'||!!(available(row)&&picked[row.package])" :disabled="!available(row)||preparing" :label="row.name" @update:model-value="picked={...picked,[row.package]:$event}" />
     <Tag v-if="rowStatus(row,scope)==='on'" :label="a('status.on')" appearance="success" />
     <span v-else-if="blocked(row)" data-wizard-app-blocked>{{blocked(row)}}</span>
     <span v-else-if="scope==='relay'&&row.media" data-wizard-app-media>{{t('apps.media')}}</span>
    </Space>
    <span v-if="!rows.length" data-wizard-no-apps>{{t('apps.none')}}</span>
   </Space>
   <ConnectionError v-if="error&&!open" :message="error" :scope="scope" data-wizard-apps-error />
  </template>
  <NameConflictRow v-if="conflict&&!open" :locale="s.ctx.locale" :conflict="conflict" :disabled="preparing" @reuse="recoverName('reuse')" @use="name=>recoverName('use',name)" @check="recoverName('check')" />
  <AppApproval v-if="approval" v-model:open="open" :locale="s.ctx.locale" :scope="scope" :items="approval.items" :dsm-only="approval.dsmOnly?.dsm" :busy="approving" :checking="checking||!namesValid" :hostnames="hostnames" :error="error" :conflict="conflict" @recover="recoverName" @hostname="changeHostname" @approve="approve" />
 </Space>
</template>
