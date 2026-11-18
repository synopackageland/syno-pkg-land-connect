<script setup lang="ts">
// Every provider page is a DSM settings page: the tab strip on top, one
// SettingsPage per tab whose body scrolls over a footer pinned at the bottom.
// Connection comes first: the status card (state word, path picture,
// address, Turn off; its status slot adds what the connection shows and its
// actions slot sits beside Turn off), and while the provider is off or failed
// its connect form (slot connect, the same component the setup wizard
// embeds). The page's own tabs (slots tab-<value>, given whether they are
// open; the most used first) follow once connected, and Advanced last. A tab
// whose slot apply-<value> is set pins those buttons (Reset then Apply) in its
// footer. A list tab (list:true) is the list itself, a DataGrid filling the
// tab. SectionTabs keep-mounted keeps every panel mounted while hidden, so a
// draft survives a tab switch. One "Learn more" link to the provider's documentation closes the
// Connection tab; explanations sit in InfoTips beside what they explain.
import {computed,provide,ref,watch} from 'vue'
import {FormNote,Link,LoadingIndicator,PageContent,SectionTabs,SettingsPage,Space} from '@synopackageland/ui'
import ConnectionControl from './ConnectionControl.vue'
import {useConnectState} from '../connect/state'
import {LEARN_MORE,learnMoreKey,type PageTab} from '../connect/pageTabs'
import type {StatusWord} from '../connect/providerStatus'
import {translate} from '../i18n'
// advancedOff:false keeps Advanced to the connected page, for a provider
// whose connect form folds its own start options. initialTab is the tab a
// connected page lands on once it knows which one fits; a guide's tab wins.
// path:false drops the status card's path picture for a page that draws its own.
// otherWord is the status word of a way the page runs without its connection.
// stop:false drops the status card's Turn off for a page whose tabs each have one.
// keepTabs keeps the tabs while the connection changes, for a page with a tab
// that does not depend on it.
// The footer slot (given the open tab) is the note at the foot of the
// Connection tab, before "Learn more".
const props=withDefaults(defineProps<{provider:string;tabs?:PageTab[];advancedOff?:boolean;initialTab?:string;path?:boolean;otherWord?:StatusWord;stop?:boolean;keepTabs?:boolean}>(),{advancedOff:true,path:true,stop:true,keepTabs:false})
const slots=defineSlots<Record<string,(props:{open?:boolean;tab?:string})=>unknown>>()
const s=useConnectState()
const t=(key:string)=>translate(s.ctx.locale,key)
const state=computed(()=>s.connectionFor(props.provider)?.state??'disconnected')
const off=computed(()=>['disconnected','failed'].includes(state.value))
const on=computed(()=>state.value==='connected')
const connectForm=computed(()=>off.value&&!!slots.connect)
const ownTabs=computed(()=>on.value||off.value&&!slots.connect||props.keepTabs?props.tabs??[]:[])
const hasAdvanced=computed(()=>!!slots.advanced&&(props.advancedOff||!off.value))
const items=computed<PageTab[]>(()=>[
 {value:'connection',label:t('tab.connection')},
 ...ownTabs.value,
 ...(hasAdvanced.value?[{value:'advanced',label:t('tab.advanced')}]:[]),
])
// Until a tab is chosen, a page whose tabs are its connect forms lands on the
// first of them while off; a failure keeps Connection, which says why.
const landing=computed(()=>state.value==='disconnected'&&!slots.connect&&ownTabs.value[0]?.value||'connection')
const tab=ref('')
// The chosen tab shows while it exists; a tab hidden while the connection
// changes comes back selected.
const current=computed(()=>items.value.some(i=>i.value===tab.value)?tab.value:landing.value)
const userSelected=ref(false)
const select=(value:string)=>{tab.value=value;userSelected.value=true}
// A guide explicitly opens its tab. Background connection changes and a late
// landing suggestion must preserve the user's selection.
watch(()=>s.guide.value,guide=>{
 const target=items.value.find(i=>i.value===guide?.tab)
 if(target)tab.value=target.value
},{immediate:true})
watch(()=>props.initialTab,next=>{if(next&&!userSelected.value&&!s.guide.value?.tab&&items.value.some(i=>i.value===next))tab.value=next},{immediate:true})
const learnMore=computed(()=>LEARN_MORE[props.provider])
provide(learnMoreKey,LEARN_MORE[props.provider])
</script>
<template>
 <Space direction="column" spacing="regular" style="height:100%;min-height:0">
 <slot name="notice" />
 <div class="flex min-h-0 flex-1 flex-col">
 <SectionTabs :model-value="current" :items="items" fill keep-mounted :aria-label="t('ux.method.'+provider)" data-page-tabs @update:model-value="select">
  <template #panel-connection>
   <SettingsPage data-tab-panel="connection">
    <Space direction="column" spacing="regular">
     <ConnectionControl :provider="provider" :path="path" :other="otherWord" :stop="stop"><slot name="status" /><template v-if="$slots.actions" #actions><slot name="actions" /></template></ConnectionControl>
     <Space v-if="connectForm" direction="column" spacing="regular" data-connect-section>
      <LoadingIndicator v-if="s.downloading.value===provider" :text="t('connect.downloading')" data-download-progress />
      <slot name="connect" />
     </Space>
     <Space v-if="learnMore||$slots.footer" direction="column" spacing="dense" align-items="start" data-provider-footer>
      <FormNote v-if="$slots.footer"><slot name="footer" :tab="current" /></FormNote>
      <Link v-if="learnMore" :href="learnMore" external data-learn-more>{{t('ux.learnMore')}}</Link>
     </Space>
    </Space>
   </SettingsPage>
  </template>
  <template v-for="item in ownTabs" :key="item.value" #[`panel-${item.value}`]>
   <PageContent v-if="item.list" height="fill" surface="none" :data-tab-panel="item.value"><slot :name="'tab-'+item.value" :open="current===item.value" /></PageContent>
   <SettingsPage v-else :data-tab-panel="item.value">
    <Space direction="column" spacing="regular"><slot :name="'tab-'+item.value" :open="current===item.value" /></Space>
    <template v-if="$slots['apply-'+item.value]" #footer><slot :name="'apply-'+item.value" /></template>
   </SettingsPage>
  </template>
  <template v-if="hasAdvanced" #panel-advanced>
   <SettingsPage data-tab-panel="advanced" data-advanced-section>
    <Space direction="column" spacing="regular"><slot name="advanced" /></Space>
    <template v-if="$slots['apply-advanced']" #footer><slot name="apply-advanced" /></template>
   </SettingsPage>
  </template>
 </SectionTabs>
 </div>
 </Space>
</template>
