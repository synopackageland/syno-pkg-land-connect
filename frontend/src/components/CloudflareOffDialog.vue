<script setup lang="ts">
import {ref,watch} from 'vue'
import {Button,Callout,Dialog,Space} from '@synopackageland/ui'
import {useConnectState} from '../connect/state'
import {useServerStatus} from '../connect/serverStatus'
import {serverApi,validStatus,type ServerStatus} from '../connect/netbirdServer'
import {dependencyText} from '../connect/dependency'
import {translate} from '../i18n'
const props=withDefaults(defineProps<{open:boolean;authorization?:boolean}>(),{authorization:false})
const emit=defineEmits<{'update:open':[boolean];changed:[]}>()
const s=useConnectState(),shared=useServerStatus()
const checked=ref(false),loading=ref(false),sending=ref(false),blocked=ref(false),failure=ref<{code:string;text:string}>()
const t=(k:string)=>translate(s.ctx.locale,k)
async function checkDependencies(){
 checked.value=false;loading.value=true;failure.value=undefined
 try{
  let status:ServerStatus|undefined
  if(shared){await shared.load();if(shared.unavailable.value)throw {code:'unavailable'};status=shared.status.value}
  else status=await s.ctx.call<ServerStatus>(serverApi.status,{})
  if(!validStatus(status))throw {code:'unavailable'}
  checked.value=true
  blocked.value=status?.dependencies?.enabled??['running','starting','working'].includes(status?.state??'')
 }catch(e){failure.value={code:(e as {code?:string})?.code??'unavailable',text:t('dependency.code.unavailable')}}
 finally{loading.value=false}
}
watch(()=>props.open,open=>{if(open)void checkDependencies()})
function server(){emit('update:open',false);s.go('netbird',{tab:'server'})}
async function confirm(){
 if(sending.value||loading.value||blocked.value||!checked.value)return
 sending.value=true;failure.value=undefined
 try{
  await s.ctx.call(props.authorization?'apps.cloudflaredisconnect':'connection.disconnect',props.authorization?{}:{provider:'cloudflare'})
  emit('update:open',false);emit('changed');await s.refresh()
 }catch(e){const code=(e as {code?:string})?.code??'unavailable';if(code==='netbird_cloudflare_in_use')blocked.value=true;failure.value={code,text:code==='netbird_cloudflare_in_use'?t('dependency.stopOrder'):dependencyText(code,t)}}
 finally{sending.value=false}
}
</script>
<template>
 <Dialog :open="open" :title="t(authorization?'apps.cf.disconnectTitle':'dependency.stopTitle')" v-bind="authorization?{'data-cf-disconnect-dialog':''}:{'data-cloudflare-off-dialog':''}" @update:open="emit('update:open',$event)">
  <Space direction="column" spacing="regular">
   <template v-if="blocked"><span>{{t(authorization?'dependency.stopMaintenance':'dependency.stopLine')}}</span><span data-dependency-stop-order>{{t('dependency.stopOrder')}}</span></template>
   <template v-else-if="authorization"><span>{{t('apps.cf.disconnectLine')}}</span><span>{{t('apps.cf.disconnectKeep')}}</span></template>
   <template v-else><span>{{t('dependency.stopConfirm')}}</span><span data-cloudflare-off-keep>{{t('dependency.stopKeep')}}</span></template>
   <Callout v-if="failure" tone="danger" :code="failure.code" data-cloudflare-off-error>{{failure.text}}<template v-if="!checked" #actions><Button :disabled="loading" @click="checkDependencies">{{t('dependency.action.check_status')}}</Button></template></Callout>
  </Space>
  <template #footer>
   <Button :disabled="sending" @click="emit('update:open',false)">{{t('apps.cancel')}}</Button>
   <Button v-if="blocked" variant="primary" data-cloudflare-off-server @click="server">{{t('dependency.action.open_server')}}</Button>
   <Button v-else variant="danger" :loading="loading||sending" :disabled="loading||sending||!checked" v-bind="authorization?{'data-cf-disconnect-confirm':''}:{'data-cloudflare-off-confirm':''}" @click="confirm">{{t(authorization?'apps.cf.disconnect':'cfpage.domain.turnOff')}}</Button>
  </template>
 </Dialog>
</template>
