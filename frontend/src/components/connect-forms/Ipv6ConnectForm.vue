<script setup lang="ts">
// Shared by the IPv6 page and setup wizard: diagnostics, never an enable action.
import {computed} from 'vue'
import {InfoTip,Link,Space} from '@synopackageland/ui'
import {useConnectState} from '../../connect/state'
import {ipv6Selection} from '../../connect/ipv6Services'
import {LEARN_MORE} from '../../connect/pageTabs'
import {labelled,translate} from '../../i18n'
import RiskNote from '../RiskNote.vue'
import Ipv6Checklist from '../Ipv6Checklist.vue'
// No primary action: footer is accepted like the other forms' and changes nothing.
const props=defineProps<{serviceIds?:string[];footer?:boolean}>()
const s=useConnectState()
const t=(key:string)=>translate(s.ctx.locale,key)
const names=computed(()=>{
 const ids=props.serviceIds??ipv6Selection(s.portView.value,s.connectionFor('ipv6'))
 const entries=s.portView.value?.entries??[]
 return new Intl.ListFormat(s.ctx.locale.replace('_','-'),{type:'conjunction'}).format(ids.flatMap(id=>{
  const entry=entries.find(e=>e.id===id)
  return entry?.port?[`${entry.name} (${entry.port})`]:[]
 }))
})
</script>
<template>
 <Space direction="column" spacing="regular" data-connect-form="ipv6">
  <RiskNote :text="t('risk.ipv6')" :href="LEARN_MORE.ipv6" data-ipv6-risk />
  <Ipv6Checklist />
  <Space v-if="names" align-items="center" spacing="dense"><span data-ipv6-services>{{labelled(s.ctx.locale,'connect.ipv6.opens',names)}}</span><InfoTip icon="info" :content="t('connect.ipv6.prepare')" :aria-label="t('connect.ipv6.opens')" data-ipv6-prepare /></Space>
  <p><Link :href="LEARN_MORE.ipv6" external>{{t('ux.learnMore')}}</Link></p>
 </Space>
</template>
