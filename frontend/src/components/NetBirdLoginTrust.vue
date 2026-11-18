<script setup lang="ts">
// Only an administrator's confirmation adds this exact observed IdP origin.
// The candidate is displayed as text and is never a link before approval.
import {computed,ref,watch} from 'vue'
import {Button,Callout,Dialog,Space} from '@synopackageland/ui'
import {useConnectState} from '../connect/state'
import {netbirdPendingOrigin} from '../connect/login'
import {translate} from '../i18n'
const s=useConnectState()
const t=(key:string)=>translate(s.ctx.locale,'netbird.'+key)
const status=computed(()=>s.connectionFor('netbird'))
const origin=computed(()=>status.value?.phase==='trust-login-origin'?netbirdPendingOrigin(status.value.loginOrigin):'')
const open=ref(false),reviewed=ref('')
watch(origin,()=>{open.value=false;reviewed.value=''})
function review(){reviewed.value=origin.value;open.value=true}
function trust(){
 const current=status.value
 if(!current||!reviewed.value||reviewed.value!==origin.value)return
 const loginOrigins=[...new Set([...(current.loginOrigins??[]),reviewed.value])]
 open.value=false
 void s.connectProvider({provider:'netbird',managementUrl:current.managementUrl,loginOrigins})
}
</script>
<template>
 <template v-if="origin">
  <Callout tone="warning" :dismiss-key="origin" :title="t('trustTitle')" data-nb-origin-required>
   <Space direction="column" spacing="dense"><span>{{t('trustHost').replace('{host}',origin)}}</span></Space>
   <template #actions><Button :disabled="s.busy.value" data-nb-trust @click="review">{{t('trustAction')}}</Button></template>
  </Callout>
  <Dialog v-model:open="open" :title="t('trustAction')" :description="t('trustHelp')" data-nb-trust-dialog>
   <span data-nb-reviewed-origin>{{reviewed}}</span>
   <template #footer><Button @click="open=false">{{translate(s.ctx.locale,'apps.cancel')}}</Button><Button variant="primary" :disabled="s.busy.value||reviewed!==origin" data-nb-trust-confirm @click="trust">{{t('trustAction')}}</Button></template>
  </Dialog>
 </template>
</template>
