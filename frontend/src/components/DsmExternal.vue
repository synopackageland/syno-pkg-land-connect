<script setup lang="ts">
// Once an own Cloudflare address runs, DSM's External Access address should
// name it so DSM's share links use it. Read through the browser's same-origin
// DSM session; change only on the administrator's click, with confirmed
// readback. A task needing the user: one title, one line, one action; the
// manual path sits in a tooltip and success is a toast.
import {computed,ref,watch} from 'vue'
import {Button,Callout,InfoTip,Space,useToast} from '@synopackageland/ui'
import {useConnectState} from '../connect/state'
import {translate} from '../i18n'
import {readDsmExternal,applyDsmExternal,externalMatches} from '../connect/dsmExternal'
interface Advice {state:string;current:{hostname:string;https_port?:number|null}|null;suggested:{hostname:string;httpsPort:number}|null}
const props=defineProps<{hostname:string}>()
const s=useConnectState()
const toast=useToast()
const t=(key:string)=>translate(s.ctx.locale,key)
const advice=ref<Advice>()
const applied=ref(false),busy=ref(false),failed=ref('')
let asked=0
watch(()=>props.hostname,async host=>{
 const n=++asked
 advice.value=undefined;applied.value=false;failed.value=''
 if(!host)return
 try{
  const current=await readDsmExternal(s.ctx)
  if(n===asked)advice.value={state:externalMatches(current,host)?'matches':current.hostname?'differs':'not-set',current,suggested:{hostname:host,httpsPort:443}}
 }catch{/* the Advanced section still explains the manual path */}
},{immediate:true})
const offer=computed(()=>advice.value?.suggested&&['not-set','differs'].includes(advice.value.state)?advice.value.suggested:undefined)
const current=computed(()=>{const c=advice.value?.current;return c?.hostname?c.hostname+(c.https_port?` · HTTPS ${c.https_port}`:''):''})
async function apply(){
 const target=offer.value
 if(!target||busy.value)return
 busy.value=true;failed.value=''
 try{
  const n=asked
  const current=await applyDsmExternal(s.ctx,target.hostname)
  if(n===asked){applied.value=true;advice.value={state:'matches',current,suggested:target};toast.success(t('dsmExternal.applied').replace('{host}',target.hostname))}
 }catch{failed.value=t('dsmExternal.failed')}
 finally{busy.value=false}
}
</script>
<template>
 <Callout v-if="offer&&!applied" tone="info" :title="current?t('dsmExternal.current').replace('{current}',current):t('dsmExternal.notSet')" data-dsm-external>
  <Space direction="column" spacing="dense"><span>{{t('dsmExternal.suggest').replace('{host}',offer.hostname)}} <InfoTip icon="info" :content="t('dsmExternal.manual')" :aria-label="t('dsmExternal.manual')" /></span>
  <span v-if="failed" role="status">{{failed}}</span></Space>
  <template #actions><Button size="sm" data-dsm-external-apply :disabled="busy" @click="apply">{{t('dsmExternal.apply')}}</Button></template>
 </Callout>
</template>
