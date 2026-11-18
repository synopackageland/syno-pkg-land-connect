<script setup lang="ts">
// The change just asked for could not keep the running connection: one row
// beside the provider's status with Apply anyway, which confirms first in
// one line, and Dismiss. It never changes the status word; it goes away on
// Dismiss, on leaving the page and when a later check passes.
import {computed,ref} from 'vue'
import {Button,Callout,Dialog} from '@synopackageland/ui'
import {useConnectState} from '../connect/state'
import {browsingThrough} from '../connect/rejected'
import {translate} from '../i18n'
const props=defineProps<{provider:string}>()
const s=useConnectState()
const t=(key:string)=>translate(s.ctx.locale,'apply.rejected.'+key)
const rejected=computed(()=>s.rejected.value?.provider===props.provider?s.rejected.value:undefined)
const yours=computed(()=>browsingThrough(s.connectionFor(props.provider),window.location.host))
const confirming=ref(false)
function apply(){confirming.value=false;s.applyAnyway()}
</script>
<template>
 <template v-if="rejected">
  <Callout tone="warning" variant="inline" :title="t('title')" data-rejected-change>
   <template #actions><Button size="sm" :disabled="s.busy.value" data-rejected-apply @click="confirming=true">{{t('apply')}}</Button><Button size="sm" data-rejected-dismiss @click="s.dismissRejected()">{{t('dismiss')}}</Button></template>
  </Callout>
  <Dialog v-model:open="confirming" :title="t('confirm')" data-rejected-dialog>
   <span :data-rejected-yours="yours||undefined">{{yours?t('yours'):t('drops')}}</span>
   <template #footer><Button @click="confirming=false">{{translate(s.ctx.locale,'apps.cancel')}}</Button><Button variant="primary" :disabled="s.busy.value" data-rejected-confirm @click="apply">{{t('apply')}}</Button></template>
  </Dialog>
 </template>
</template>
