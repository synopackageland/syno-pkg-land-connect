<script setup lang="ts">
import {onMounted,onUnmounted,ref} from 'vue'
import {Button,Callout,Card,CardGrid,KeyValueRow,SettingsSection,Skeleton,Space,Tag} from '@synopackageland/ui'
import {translate,uiLocale} from './i18n'
import GitHubTokenAction from './components/GitHubTokenAction.vue'
import {githubText,isGitHubCode} from './connect/github'
import type {Context} from '@synopackageland/sdk'
const props=defineProps<{ctx:Context}>()
// githubToken asks the page to bring its GitHub token field into view.
const emit=defineEmits<{githubToken:[]}>()
function t(key:string,version=''){return translate(props.ctx.locale,'binaries.'+key).replace('{version}',version)}
interface BinaryStatus {runtimeVersion?:string;product:string;current:string;latest:string;previous:string;checkedAt:string;verification:string;updateAvailable:boolean;phase:string;error:string;errorCode?:string;retryAt?:string;busy?:boolean}
const products=['cloudflared','tailscale','netbird','netbird-server'],states=ref<Record<string,BinaryStatus>>({}),busy=ref<Record<string,boolean>>({}),errors=ref<Record<string,string>>({})
const submitting=new Set<string>()
const reading=new Set<string>()
const actions=ref<Record<string,string>>({})
const generations:Record<string,number>={}
const readFailures=new Set<string>()
const readAttempts:Record<string,number>={}
let disposed=false,timer:ReturnType<typeof window.setInterval>|undefined
const running=()=>products.filter(product=>busy.value[product]||readFailures.has(product))
function poll(){
 if(running().length && !timer)timer=window.setInterval(()=>void refresh(running()),1500)
 if(!running().length){window.clearInterval(timer);timer=undefined}
}
// Each product reads on its own, so a slow one never holds back the others.
async function read(product:string){
 if(disposed||reading.has(product)||submitting.has(product))return
 const generation=generations[product]??0
 reading.add(product)
 try{const s=await props.ctx.call<BinaryStatus>('binaries.status',{product});if(disposed||generation!==(generations[product]??0))return;states.value[product]=s;busy.value[product]=!!s.busy;readAttempts[product]=0;const recovered=readFailures.delete(product);if(recovered||(s.error&&errors.value[product]!==t('noPrevious')))errors.value[product]=''}
 catch{if(!disposed&&generation===(generations[product]??0)){
  readFailures.add(product);readAttempts[product]=(readAttempts[product]??0)+1
  // A lost status response is not a completed download. Keep polling; after
  // repeated failures release the stale spinner and offer a retry. The daemon
  // still refuses duplicate jobs while its original one is active.
  if(!busy.value[product]||readAttempts[product]>=3){busy.value[product]=false;const shown=states.value[product];if(shown)states.value[product]={...shown,busy:false,phase:'failed'};errors.value[product]=t('error')}
 }}
 finally{reading.delete(product)}
}
async function refresh(list=products){
 await Promise.all(list.map(read))
 if(!disposed)poll()
}
async function run(product:string,action:string){
 if(busy.value[product]||disposed)return
 busy.value[product]=true;submitting.add(product);actions.value[product]=action;generations[product]=(generations[product]??0)+1;errors.value[product]=''
 // Until the daemon answers, the card says the job started, not the previous result.
 const shown=states.value[product];if(shown)states.value[product]={...shown,busy:true,phase:action==='check'?'checking':'queued'}
 try{const s=await props.ctx.call<BinaryStatus>('binaries.'+action,{product});if(!disposed){states.value[product]=s;busy.value[product]=!!s.busy}}
 catch(e){if(!disposed){busy.value[product]=false;const shown=states.value[product];if(shown)states.value[product]={...shown,busy:false,phase:'failed'};const error=e as {code?:string;message?:string};errors.value[product]=error?.code==='binary_no_previous'?t('noPrevious'):error?.message||t('error')}}
 finally{submitting.delete(product);if(!disposed){poll();await refresh([product])}}
}
const title=(product:string)=>product==='tailscale'?'tailscale + tailscaled':product==='netbird-server'?t('name.netbird-server'):product
// The version the provider runs; a download only takes effect on reconnect.
const inUse=(product:string)=>states.value[product]?.runtimeVersion||states.value[product]?.current||''
// The backend reports how a download was checked as a code and when as RFC 3339.
const verified=(product:string)=>{const v=states.value[product]?.verification;return v?t('verified.'+v):'—'}
const when=(at:string)=>new Intl.DateTimeFormat(uiLocale(props.ctx.locale),{dateStyle:'medium',timeStyle:'short'}).format(new Date(at))
const checked=(product:string)=>{const at=states.value[product]?.checkedAt;return at?when(at):'—'}
// GitHub's hourly quota, a rejected token or a refusal is not a network fault;
// say which, and when to retry.
const failure=(product:string)=>{const s=states.value[product];if(isGitHubCode(s?.errorCode))return githubText(key=>translate(props.ctx.locale,key),when,s.errorCode,s.retryAt);return s?.errorCode==='github_refused'?t('refused'):s?.error||t('error')}
// The GitHub refusal a card shows, unless an action error replaced it.
const github=(product:string)=>{const s=states.value[product];return !errors.value[product]&&!s?.busy&&isGitHubCode(s?.errorCode)?s.errorCode:undefined}
const loading=(product:string,action:string)=>!!busy.value[product]&&(actions.value[product]??(states.value[product]?.phase==='checking'?'check':'update'))===action
const pending=(product:string)=>{const s=states.value[product];return s?.current&&s.runtimeVersion&&s.current!==s.runtimeVersion?s.current:''}
onMounted(()=>void refresh())
defineExpose({refresh:()=>refresh()})
onUnmounted(()=>{disposed=true;window.clearInterval(timer)})
</script>
<template>
 <Space direction="column" spacing="regular">
  <CardGrid min-item-width="360px">
  <Card v-for="product in products" :key="product" :title="title(product)" :aria-label="product" :aria-busy="!!busy[product]" :loading="!states[product]&&!errors[product]">
   <template #header-actions>
    <Tag v-if="inUse(product)" :label="t('inUse',inUse(product))" appearance="success" data-binary-in-use />
    <Tag v-else-if="states[product]" :label="t('missing')" data-binary-in-use />
    <Skeleton v-else-if="!errors[product]" width="6em" height="1.5em" data-binary-in-use-loading />
   </template>
   <Space direction="column" spacing="regular">
    <span v-if="pending(product)" data-binary-pending>{{product==='netbird-server'?t('pendingServer',pending(product)):t('pending',pending(product))}}</span>
    <span v-if="product==='netbird-server'" data-binary-source>{{t('source.netbird-server')}}</span>
    <KeyValueRow :label="t('latest')">{{states[product]?.latest||'—'}}</KeyValueRow>
    <SettingsSection collapsible :title="translate(ctx.locale,'connect.diagnostics')" :default-open="false">
     <KeyValueRow :label="t('checked')">{{checked(product)}}</KeyValueRow>
     <KeyValueRow :label="t('verification')">{{verified(product)}}</KeyValueRow>
     <KeyValueRow :label="t('previous')">{{states[product]?.previous||'—'}}</KeyValueRow>
    </SettingsSection>
    <span v-if="!busy[product]&&states[product]?.phase&&!['idle','failed'].includes(states[product].phase)" role="status">{{t(states[product].phase)}}</span>
    <Callout v-if="!busy[product]&&(errors[product]||states[product]?.error)" tone="danger" :title="translate(ctx.locale,'provider.attention')"><span role="alert">{{errors[product]||failure(product)}}</span><template v-if="github(product)" #actions><GitHubTokenAction :locale="ctx.locale" :code="github(product)!" @open="emit('githubToken')" /></template></Callout>
   </Space>
   <template #footer>
    <Space spacing="compact">
     <Button :loading="loading(product,'check')" :disabled="!!busy[product]" @click="run(product,'check')">{{t('check')}}</Button>
     <Button v-if="!states[product]?.current" :loading="loading(product,'update')" :disabled="!!busy[product]" @click="run(product,'update')">{{t('download')}}</Button>
     <Button v-else-if="states[product]?.updateAvailable && states[product]?.latest" :loading="loading(product,'update')" :disabled="!!busy[product]" @click="run(product,'update')">{{t('updateTo',states[product].latest)}}</Button>
     <Button v-if="states[product]?.previous" :loading="loading(product,'rollback')" :disabled="!!busy[product]" data-binary-rollback @click="run(product,'rollback')">{{t('rollbackTo',states[product].previous)}}</Button>
    </Space>
   </template>
  </Card>
  </CardGrid>
 </Space>
</template>
