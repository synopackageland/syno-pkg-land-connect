<script setup lang="ts">
// DSM's own protections at the step where the user decides: read in the
// background with the user's session; a protection that is off says so with
// DSM's official help, one DSM does not let this user read says it cannot be
// checked, and one that is on needs nothing.
import {onMounted,ref} from 'vue'
import {Link,Space,Tag} from '@synopackageland/ui'
import {useConnectState} from '../connect/state'
import {dsmSecurity,PROTECTIONS,type DsmSecurity} from '../connect/dsmSecurity'
import {CATALOG} from '../connect/advisor'
import {translate} from '../i18n'
const s=useConnectState()
const t=(key:string)=>translate(s.ctx.locale,key)
const state=ref<DsmSecurity>()
onMounted(async()=>{state.value=await dsmSecurity(s.ctx)})
const tone={on:'success',off:'warning',unknown:'neutral'} as const
</script>
<template>
 <Space direction="column" spacing="dense" data-protection-checks>
  <Space v-for="p in PROTECTIONS" :key="p" align-items="center" spacing="dense" wrap :data-protection="p" :data-state="state?.[p]??'checking'">
   <span>{{t('advisor.pairWith.'+p)}}</span>
   <Tag v-if="state" :label="t('guide.check.'+state[p])" :appearance="tone[state[p]]" />
   <Link v-if="!state||state[p]!=='on'" :href="CATALOG.protections[p]" external>{{t(state?.[p]==='off'?'guide.check.turnOn':'guide.check.howTo')}}</Link>
  </Space>
 </Space>
</template>
