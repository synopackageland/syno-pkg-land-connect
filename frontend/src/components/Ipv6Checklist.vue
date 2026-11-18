<script setup lang="ts">
// What IPv6 direct needs, item by item: the app detects only the NAS address.
// The firewall, router and DDNS stay the user's; the app never changes them,
// and only a phone outside this network can tell whether the address opens.
import {computed,onMounted,onUnmounted,ref,watch} from 'vue'
import {Badge,Button,ListItem,SettingsSection,Tag} from '@synopackageland/ui'
import {useConnectState} from '../connect/state'
import {translate} from '../i18n'
import {openNetworkSettings,openFirewallSettings} from '../dsm-navigation'
import {checkConnection,type CheckResult} from '../connect/checks'
interface IPv6Status {state:string;reason:string;addresses:Array<{ip:string;interface:string;prefixBits:number}>}
type CheckState='ok'|'missing'|'unknown'|'confirm'|'optional'|'pending'
const s=useConnectState()
// workers.dev is set up on App URLs, in its own section.
function freeLink(){s.appsFocus.value={scope:'workers'};s.go('apps')}
const t=(key:string)=>translate(s.ctx.locale,'connect.ipv6.'+key)
const status=ref<IPv6Status>(),failed=ref(false),navigationFailed=ref(false),checking=ref(false),outside=ref<CheckResult>()
let disposed=false,timer:ReturnType<typeof window.setInterval>|undefined
async function refresh(){
 if(checking.value)return
 checking.value=true;outside.value=undefined
 try{
  const next=await s.ctx.call<IPv6Status>('ipv6.status')
  if(disposed)return
  status.value=next;failed.value=false
  // A local address or a legacy enabled setting proves no outside access.
  if(next.addresses.length&&s.connectionFor('ipv6')?.state==='connected'){
   const result=await checkConnection(s.ctx,'ipv6')
   if(!disposed)outside.value=result
  }
 }catch{if(!disposed){status.value=undefined;failed.value=true}}
 finally{if(!disposed)checking.value=false}
}
const addresses=computed(()=>(status.value?.addresses??[]).map(a=>a.ip))
const address=computed<CheckState>(()=>failed.value||!status.value||status.value.reason==='addresses_unavailable'?'unknown':addresses.value.length?'ok':'missing')
const working=computed(()=>outside.value?.state==='ok'&&outside.value.externalVerified===true)
const summary=computed(()=>working.value?'working':address.value==='missing'?'notSetUp':'waiting')
const items=computed<Array<{name:string;state:CheckState;detail:string}>>(()=>[
 {name:'address',state:address.value,detail:address.value==='ok'?addresses.value.join(', '):address.value==='missing'?t('check.address.missing'):''},
 {name:'firewall',state:'confirm',detail:t('check.firewall.detail')},
 {name:'router',state:'confirm',detail:t('check.router.detail')},
 {name:'ddns',state:'optional',detail:t('check.ddns.detail')},
 {name:'external',state:working.value?'ok':'pending',detail:t('check.external.phone')},
])
const tones={ok:'success',missing:'neutral',unknown:'neutral',confirm:'neutral',optional:'neutral',pending:'neutral'} as const
watch(s.refreshes,(_,before)=>{if(before)void refresh()})
function open(firewall:boolean){navigationFailed.value=!(firewall?openFirewallSettings(s.ctx):openNetworkSettings(s.ctx))}
onMounted(()=>{
 s.ctx.reopen(()=>{if(!disposed)void refresh()})
 void refresh();timer=window.setInterval(()=>void refresh(),30000)
})
onUnmounted(()=>{disposed=true;window.clearInterval(timer)})
</script>
<template>
 <SettingsSection :collapsible="false" :title="t('check.title')" data-ipv6-checklist>
  <template #actions><Tag :label="t(summary)" :appearance="working?'success':'neutral'" data-ipv6-status :data-ipv6-state="summary" /></template>
  <p data-ipv6-guide>{{t('guide')}}</p>
  <div role="list">
   <ListItem v-for="item in items" :key="item.name" :title="t(`check.${item.name}.title`)" :description="item.detail||undefined" :data-check-item="item.name" :data-check-state="item.state">
    <template #meta><Badge :tone="tones[item.state]">{{t(`check.${item.state}`)}}</Badge></template>
    <template v-if="item.name==='address' || item.name==='firewall'" #actions>
     <Button v-if="item.name==='address'" data-open-network @click="open(false)">{{translate(s.ctx.locale,'setup.ipv6.network')}}</Button>
     <Button v-else data-open-firewall @click="open(true)">{{translate(s.ctx.locale,'setup.ipv6.firewall')}}</Button>
    </template>
   </ListItem>
  </div>
  <p v-if="address==='missing'" data-ipv6-fallback>{{t('check.address.fallback')}} <Button data-ipv6-fallback-go="workers" @click="freeLink">workers.dev</Button> <Button data-ipv6-fallback-go="tailscale" @click="s.go('tailscale')">{{translate(s.ctx.locale,'ux.method.tailscale')}}</Button></p>
  <p v-if="navigationFailed" role="status">{{translate(s.ctx.locale,'setup.ipv6.navFailed')}}</p>
  <template #footer><Button :loading="checking" :disabled="checking" data-ipv6-check-again @click="refresh">{{translate(s.ctx.locale,'connect.check.again')}}</Button></template>
 </SettingsSection>
</template>
