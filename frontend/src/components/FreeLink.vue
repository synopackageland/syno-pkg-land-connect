<script setup lang="ts">
import {relayLane} from '../connect/topology'
import ConnectionError from './ConnectionError.vue'

// The Free link tab: workers.dev in the user's own Cloudflare account, with
// no domain, on the layout it shares with the Own domain tab (CloudflareWay).
// Off, the sign-in slot holds one compact form: the token field, whose line
// links Cloudflare's own page with the permissions filled in, and, only when
// the account has no workers.dev name yet, the name. The name is set once for
// the whole account and every App address ends with it, so it starts filled
// in and a taken name offers another. Turning it on publishes DSM on it at
// once (the backend does, with no review: DSM signs people in itself). On,
// the address is DSM's; a Free link turned on before that, or whose DSM was
// cleared, offers Add DSM, and an own domain that serves DSM is named
// instead. Its own row is the free quota.
// The token never comes back to the page. The field clears at once; it is
// held in memory only between the paste and the name step.
// In the setup wizard (footer) the form's primary button (Check and connect,
// or Use this name) is the wizard footer's main action.
import {computed,onUnmounted,ref,watch} from 'vue'
import {Button,Callout,Checkbox,Dialog,Form,FormActions,FormItem,FormNote,InfoTip,Input,InputGroup,KeyValueRow,Link,Space,StepList,useToast} from '@synopackageland/ui'
import {useConnectState} from '../connect/state'
import {failureToast} from '../connect/errorToast'
import {appsApi,quotaReset,relayTokenUrl} from '../connect/appUrls'
import {defaultAccountName,freeLinkAddress,freeLinkState,nextName,subdomainSettingsUrl,toLabel,validLabel,type WorkersStatus} from '../connect/freeLink'
import {dsmWay,hostOf,type WayState} from '../connect/cloudflareWay'
import type {FormPrimary} from '../connect/connectForm'
import {translate} from '../i18n'
import CloudflareWay from './CloudflareWay.vue'
const props=defineProps<{status?:WorkersStatus;apps?:number;loading?:boolean;footer?:boolean}>()
const emit=defineEmits<{changed:[]}>()
const s=useConnectState()
const toast=useToast()
const t=(key:string)=>translate(s.ctx.locale,key)
const c=(key:string)=>t('cfpage.free.'+key)
const token=ref(''),checking=ref(false),error=ref<{text:string;code?:string}>()
const state=computed(()=>freeLinkState(props.status))
const wayState=computed<WayState>(()=>checking.value?'pending':({on:'on',attention:'attention',name:'off',off:'off'} as const)[state.value])
const address=computed(()=>freeLinkAddress(props.status))
const dsm=computed(()=>dsmWay(props.status))
const quota=computed(()=>`${t('apps.relay.fact.quota')} · ${t('apps.relay.fact.over')}`.replaceAll('{time}',quotaReset(s.ctx.locale)))

let held=''
const asking=ref(false)
const name=ref(''),nameError=ref(''),suggestion=ref('')
const fallback=computed(()=>s.connectionFor('cloudflare')?.suggestedName??'')
watch(fallback,next=>{if(!name.value)name.value=defaultAccountName(window.location.hostname,next)},{immediate:true})
const label=computed(()=>toLabel(name.value.trim()))
const naming=computed(()=>asking.value||state.value==='name')
// Turning it on makes DSM's sign-in page public, so the token step asks the
// same one consent as the own domain.
const confirmed=ref(false)
// Leaving the page forgets the held token, the consent and the name step.
function forget(){held='';asking.value=false;token.value='';error.value=undefined;nameError.value='';suggestion.value='';confirmed.value=false}
watch(()=>s.page.value,forget)
onUnmounted(forget)

function errorText(e:unknown):string{
 const code=(e as {code?:string})?.code??''
 const key='cfpage.code.'+code
 return code.startsWith('workers_')&&t(key)!==key?t(key):s.message(e)
}
async function send(params:Record<string,unknown>){
 checking.value=true;error.value=undefined;nameError.value='';suggestion.value=''
 try{
  const result=await s.ctx.call<WorkersStatus>(appsApi.relayConnect,params)
  if(!result.connected&&result.code==='workers_subdomain_required'){held=String(params.token??'');asking.value=true;return}
  forget()
  toast.success(t('apps.relay.connected').replace('{subdomain}',result.subdomain??''))
  if(result.dsmCode&&result.dsmCode!=='apps_dsm_public_elsewhere')toast.warning(t('cfpage.free.dsmFailed'),errorText({code:result.dsmCode}))
  emit('changed')
 }catch(e){
  const code=(e as {code?:string})?.code
  if(naming.value&&(code==='workers_subdomain_taken'||code==='workers_subdomain_invalid')){
   nameError.value=errorText(e)
   if(code==='workers_subdomain_taken')suggestion.value=nextName(label.value)
   held=String(params.token??held)
  }else error.value={text:errorText(e),code}
  // A refused name leaves the token saved for the next name: the status says so.
  if(naming.value)emit('changed')
 }finally{checking.value=false}
}
// The token step: one paste, cleared at once.
function connect(){
 const value=token.value.trim()
 if(!value||checking.value||!confirmed.value&&state.value!=='attention')return Promise.resolve()
 token.value=''
 return send({token:value})
}
// The name step: the held token, or one pasted again after the page was left.
const needToken=computed(()=>naming.value&&!held)
const nameReady=computed(()=>validLabel(label.value)&&(!needToken.value||!!token.value.trim()))
function claim(){
 if(!nameReady.value||checking.value)return Promise.resolve()
 const value=held||token.value.trim()
 token.value=''
 return send({token:value,subdomain:label.value})
}
// The same action as the form's own button, for the wizard footer.
const primary=computed<FormPrimary|undefined>(()=>{
 if(!props.footer||state.value==='on'||props.loading&&!props.status)return undefined
 if(naming.value&&state.value!=='attention')return {label:c('nameUse'),loading:checking.value,disabled:checking.value||!nameReady.value,run:claim}
 return {label:t('apps.relay.connect'),loading:checking.value,disabled:checking.value||!token.value.trim()||!confirmed.value&&state.value!=='attention',run:connect}
})
defineExpose({primary})
function trySuggestion(){name.value=suggestion.value;suggestion.value='';nameError.value=''}

// Free link cleanup keeps credentials and the Signal bridge used by other features.
const offOpen=ref(false),turningOff=ref(false),offError=ref<{text:string;code?:string}>()
watch(offOpen,()=>{offError.value=undefined})
async function turnOff(){
 if(turningOff.value)return
 turningOff.value=true;offError.value=undefined
 try{await s.ctx.call(appsApi.relayDisconnect,{});offOpen.value=false;toast.success(t('apps.relay.disconnected'))}
 catch(e){offError.value={text:errorText(e),code:(e as {code?:string})?.code}}
 finally{turningOff.value=false;emit('changed')}
}
const goApps=()=>{s.appsFocus.value={scope:'workers'};s.go('apps')}

// Add DSM: one click for a Free link that is on without it.
const addingDsm=ref(false)
async function addDsm(){
 if(addingDsm.value)return
 addingDsm.value=true
 try{await s.ctx.call<WorkersStatus>(appsApi.workersPublishDsm,{});toast.success(t('cfpage.free.dsmAdded'))}
 catch(e){failureToast(toast,s,t('cfpage.free.dsmFailed'),e)}
 finally{addingDsm.value=false;emit('changed')}
}
</script>
<template>
 <CloudflareWay kind="free" :state="wayState" :links="relayLane(status,s.statusUnavailable.value,s.checks.value.workers)?.links" :locale="s.ctx.locale" :address="address" :apps="apps" :change-title="c('rename')" :turn-off-label="c('turnOff')" :busy="turningOff" data-free-link :data-free-state="state" @choose-apps="goApps" @turn-off="offOpen=true">
  <template #attention>
   <span v-if="loading&&!status" role="status" data-free-loading>{{t('apps.loading')}}</span>
   <template v-if="state==='attention'">
    <Callout tone="warning" :title="t('apps.relay.invalid')" data-free-invalid>{{t('apps.relay.invalidLine')}}</Callout>
    <Form>
     <FormItem :label="t('apps.relay.token')"><Input v-model="token" type="password" autocomplete="off" :disabled="checking" data-free-token @keydown.enter.prevent="connect" /><template #description><Space align-items="center" spacing="dense" wrap><Link :href="relayTokenUrl()" external data-free-token-link>{{t('apps.relay.step.tokenLink')}}</Link><InfoTip icon="info" :content="`${t('apps.relay.step.tokenLine')} · ${t('apps.relay.tokenHelp')}`" :aria-label="t('apps.relay.token')" /></Space></template></FormItem>
     <FormItem v-if="error" label="" inset><ConnectionError :message="error.text" :title="t('apps.relay.checkFailed')" :code="error.code" data-free-error /></FormItem>
     <FormActions v-if="!footer" inset><Button variant="primary" kind="tool" :loading="checking" :disabled="checking||!token.trim()" data-free-connect @click="connect">{{t('apps.relay.connect')}}</Button></FormActions>
    </Form>
   </template>
  </template>
  <template #address>
   <FormNote v-if="dsm==='cloudflare'" data-free-dsm-elsewhere>{{c('dsmElsewhere').replace('{host}',hostOf(status?.dsm?.url))}}</FormNote>
   <Button v-else-if="!dsm" :loading="addingDsm" :disabled="addingDsm" data-free-add-dsm @click="addDsm">{{c('addDsm')}}</Button>
  </template>
  <template #extra>
   <KeyValueRow :label="c('quotaLabel')">
    <Space align-items="center" spacing="dense"><span data-free-quota>{{c('quota').replace('{time}',quotaReset(s.ctx.locale))}}</span><InfoTip icon="info" :content="quota" :aria-label="c('quotaLabel')" /></Space>
   </KeyValueRow>
  </template>
  <template #signin>
   <Form v-if="!(loading&&!status)" :data-free-step="naming?'name':'token'">
    <FormItem v-if="!naming||needToken" :label="t('apps.relay.token')">
     <Input v-model="token" type="password" autocomplete="off" :disabled="checking" data-free-token @keydown.enter.prevent="naming?claim():connect()" />
     <template #description><Space align-items="center" spacing="dense" wrap><Link :href="relayTokenUrl()" external data-free-token-link>{{t('apps.relay.step.tokenLink')}}</Link><InfoTip icon="info" :content="`${t('apps.relay.step.tokenLine')} · ${t('apps.relay.tokenHelp')}`" :aria-label="t('apps.relay.token')" /></Space></template>
    </FormItem>
    <FormItem v-else :label="t('apps.relay.token')" text-only><span data-free-token-done>{{c('tokenDone')}}</span></FormItem>
    <FormItem v-if="naming" :label="c('name')" :help="nameError||undefined" :validate-status="nameError||(label&&!validLabel(label))?'error':undefined">
     <Space align-items="center" spacing="dense"><InputGroup><Input v-model="name" autocomplete="off" :disabled="checking" :invalid="!!nameError" data-free-name-input @keydown.enter.prevent="claim" /><span data-free-suffix>.workers.dev</span></InputGroup><InfoTip icon="info" :content="c('nameHelp')" :aria-label="c('name')" /></Space>
     <template #description><span data-free-example>{{c('example').replace('{name}',label||'…')}}</span></template>
    </FormItem>
    <FormItem v-if="!naming" label="" inset><Checkbox v-model="confirmed" :label="t('cloudflare.confirm')" :disabled="checking" data-free-confirm /></FormItem>
    <FormActions v-if="suggestion" inset><Button kind="tool" data-free-suggestion @click="trySuggestion">{{c('nameTry').replace('{name}',suggestion)}}</Button></FormActions>
    <FormItem v-if="error" label="" inset><ConnectionError :message="error.text" :title="t('apps.relay.checkFailed')" :code="error.code" data-free-error /></FormItem>
    <FormActions v-if="!footer" inset>
     <Button v-if="naming" variant="primary" kind="tool" :loading="checking" :disabled="checking||!nameReady" data-free-claim @click="claim">{{c('nameUse')}}</Button>
     <Button v-else variant="primary" kind="tool" :loading="checking" :disabled="checking||!token.trim()||!confirmed" data-free-connect @click="connect">{{t('apps.relay.connect')}}</Button>
    </FormActions>
   </Form>
  </template>
  <template #change>
   <Space direction="column" spacing="regular" data-free-rename>
    <Callout tone="warning" :title="c('renameWarn')" data-free-rename-warning />
    <StepList :items="[{title:c('renameOff')},{title:c('renameCloudflare')},{title:c('renameAgain')}]">
     <template #description="{index}"><Link v-if="index===1" :href="subdomainSettingsUrl" external data-free-rename-link>{{c('renameLink')}}</Link></template>
    </StepList>
   </Space>
  </template>
 </CloudflareWay>
 <Dialog v-model:open="offOpen" :title="t('apps.relay.disconnectTitle')" data-free-off-dialog>
  <Space direction="column" spacing="dense"><span>{{t('apps.relay.disconnectLine')}}</span><span>{{t('apps.relay.disconnectToken')}}</span><Callout v-if="offError" tone="danger" :code="offError.code" :title="t('apps.failed')" data-free-off-error>{{offError.text}}</Callout></Space>
  <template #footer><Button :disabled="turningOff" @click="offOpen=false">{{t('apps.cancel')}}</Button><Button variant="danger" :loading="turningOff" :disabled="turningOff" data-free-off-confirm @click="turnOff">{{c('turnOff')}}</Button></template>
 </Dialog>
</template>
