<script setup lang="ts">
// Needs attention: shown only while something is wrong, one Callout per
// thing with its one action. It gathers what the Home used to spread over
// separate callouts: DSM's certificate review for Cloudflare and Tailscale,
// a way that failed or waits for a sign-in or approval, and a workers.dev
// token that stopped working.
import {computed} from 'vue'
import {Button,Callout,SettingsSection,Space} from '@synopackageland/ui'
import DsmReview from './DsmReview.vue'
import {useConnectState} from '../connect/state'
import {openFreeLink} from '../connect/freeLink'
import {attentionItems,type AttentionItem,type WorkersStatus} from '../connect/home'
import {labelled,translate} from '../i18n'
import type {ShellPage} from '../shell/pages'
const props=defineProps<{workers?:WorkersStatus}>()
const s=useConnectState()
const t=(key:string)=>translate(s.ctx.locale,key)
const items=computed(()=>attentionItems(s.connections.value,props.workers,s.statusUnavailable.value))
const nameKey=(item:AttentionItem)=>item.kind==='workers'?'home.way.workers':item.provider==='ipv6'?'connect.ipv6':'ux.method.'+item.provider
const stateKey={failed:'ux.state.failed','needs-login':'ux.state.needs-login','needs-machine-auth':'overview.nas.approval'} as const
function title(item:AttentionItem):string {
 if(item.kind==='dsm')return t('apps.dsmReview.title')
 return labelled(s.ctx.locale,nameKey(item),t(item.kind==='workers'?'home.workers.invalid':stateKey[item.state]))
}
function line(item:AttentionItem):string {
 if(item.kind==='dsm')return t('apps.dsmReview.line')
 if(item.kind==='way'&&item.state==='failed'&&item.code)return s.message({code:item.code})
 return ''
}
const tone=(item:AttentionItem)=>item.kind==='way'&&item.state==='failed'?'danger':'warning'
// workers.dev's controls are on the Cloudflare page's Free link tab.
function open(item:AttentionItem){
 if(item.kind==='workers')openFreeLink(s)
 else s.go(item.provider as ShellPage)
}
// The item's one action: review DSM's certificate here, else open the way's page.
function action(item:AttentionItem){
 return {label:t('home.attention.open').replace('{way}',t(nameKey(item))),'data-attention-open':'',onClick:()=>open(item)}
}
</script>
<template>
 <SettingsSection v-if="items.length" :collapsible="false" :title="t('home.attention.title')" data-needs-attention>
  <Space direction="column" spacing="dense">
   <template v-for="item in items" :key="item.id">
    <DsmReview v-if="item.kind==='dsm'" :provider="item.provider" :attention-id="item.id" />
    <Callout v-else :tone="tone(item)" :title="title(item)" data-attention-item :data-attention-id="item.id">{{line(item)}}<template #actions><Button size="sm" v-bind="action(item)">{{action(item).label}}</Button></template></Callout>
   </template>
  </Space>
 </SettingsSection>

</template>
