<script setup lang="ts">
// Step 4: the addresses that now work, each with Copy, Open and a QR code,
// and how to check them from a phone the way a stranger would: off the home
// Wi-Fi. IPv6 first checks that the phone has IPv6 at all; the test sites
// only open in a new tab, Connect never calls them. Add another way and Done
// are the wizard footer's. A temporary address adds one hint under it: Free
// link keeps an address that stays the same.
import {computed,onMounted,onUnmounted,ref} from 'vue'
import {Button,Callout,Link,LoadingIndicator,Space,StepList} from '@synopackageland/ui'
import {useConnectState} from '../../connect/state'
import {appsApi,listedApps,type AppsView} from '../../connect/appUrls'
import {addressesOf} from '../../connect/addresses'
import {APP_SCOPE,TEST_SITES,providerOf,selfTestSteps,type WizardWay} from '../../connect/wizard'
import {translate} from '../../i18n'
import UrlCard from '../UrlCard.vue'
const props=defineProps<{way:WizardWay;dsmHost?:string}>()
const emit=defineEmits<{freeLink:[]}>()
const s=useConnectState()
const t=(key:string)=>translate(s.ctx.locale,'wizard.'+key)
const scope=computed(()=>APP_SCOPE[props.way])
let disposed=false
onUnmounted(()=>{disposed=true})
const view=ref<AppsView>(),loading=ref(!!scope.value)
onMounted(async()=>{
 if(!scope.value)return
 try{const list=await s.ctx.call<AppsView>(appsApi.list,{});if(!disposed)view.value=list}
 catch{/* the way's own address still shows */}
 finally{if(!disposed)loading.value=false}
})
// DSM first: the way's own address, or the workers.dev name just approved.
const addresses=computed(()=>{
 const dsm=props.way==='workers'
  ?(props.dsmHost?[`https://${props.dsmHost}`]:[])
  :addressesOf(s.connectionFor(providerOf(props.way)))
 const out=dsm.map(url=>({label:t('done.dsm'),url}))
 const sc=scope.value
 if(sc)for(const row of listedApps(view.value)){const v=row[sc];if(v?.state==='active'&&v.url)out.push({label:row.name,url:v.url})}
 return out
})
// The phone app is the provider's own: Tailscale or NetBird.
const app=computed(()=>translate(s.ctx.locale,'ux.method.'+props.way))
const steps=computed(()=>selfTestSteps(props.way).map(step=>({key:step,title:t('test.'+step).replace('{app}',app.value)})))
</script>
<template>
 <Space direction="column" spacing="regular" data-wizard-done>
  <LoadingIndicator v-if="loading" />
  <Space v-else-if="addresses.length" direction="column" spacing="regular" data-wizard-addresses>
   <Space v-for="address in addresses" :key="address.url" direction="column" spacing="dense" :data-wizard-address="address.label">
    <strong>{{address.label}}</strong>
    <UrlCard :url="address.url" :locale="s.ctx.locale" :label="address.label" />
   </Space>
  </Space>
  <span v-else data-wizard-no-address>{{t('done.noAddress')}}</span>
  <Callout v-if="way==='temporary'" tone="info" variant="inline" data-wizard-temporary-hint>{{t('temporary.stable')}}<template #actions><Button data-wizard-use-free-link @click="emit('freeLink')">{{t('use').replace('{way}',t('way.workers'))}}</Button></template></Callout>
  <strong>{{t('test.title')}}</strong>
  <StepList :items="steps" data-wizard-test>
   <template #description="{index}">
    <Space v-if="steps[index]?.key==='phone-ipv6'" direction="column" spacing="none" data-wizard-test-ipv6>
     <Link :href="TEST_SITES.phoneIpv6" external>test-ipv6.com</Link>
     <span>{{t('test.phoneIpv6Score')}}</span>
    </Space>
    <span v-else-if="steps[index]?.key==='open'&&way==='ipv6'">{{t('test.firewall')}}</span>
    <Link v-else-if="steps[index]?.key==='outside'" :href="TEST_SITES.outsideIpv6" external data-wizard-test-outside>{{t('test.outsideSite')}}</Link>
   </template>
  </StepList>
  <span data-wizard-test-help>{{t('test.notOpen')}}</span>
 </Space>
</template>
