<script setup lang="ts">
import {computed,onUnmounted,ref} from 'vue'
import {CopyCutField,Callout,InfoTip,KeyValueRow,LoadingIndicator,Space,formatDuration} from '@synopackageland/ui'
import {labelSeparator} from '@synopackageland/sdk'
import {useConnectState} from '../connect/state'
import {translate,uiLocale} from '../i18n'
import {netbirdKnows,netbirdText} from '../netbirdMessages'
import ReadyGate from './ReadyGate.vue'
import ProviderLayout from '../components/ProviderLayout.vue'
import NetBirdConnectForm from '../components/connect-forms/NetBirdConnectForm.vue'
import AppList from '../components/AppList.vue'
import {provideServerStatus} from '../connect/serverStatus'
import ServerDependencyBanner from '../components/ServerDependencyBanner.vue'
import NetBirdServer from '../components/NetBirdServer.vue'
const s=useConnectState()
const server=provideServerStatus(s,'netbird')
const n=(key:string)=>netbirdText(s.ctx.locale,key)
const status=computed(()=>s.connectionFor('netbird'))
const now=ref(Date.now())
const timer=window.setInterval(()=>{now.value=Date.now()},1000)
onUnmounted(()=>window.clearInterval(timer))
const remaining=computed(()=>{const deadline=Date.parse(status.value?.deadline??'');return Number.isFinite(deadline)?Math.min(600,Math.max(0,Math.ceil((deadline-now.value)/1000))):0})
// Off, Join holds the connect form; connected, Apps lists the Apps at their
// NetBird address. Self-host server runs a NetBird server on this NAS in any
// state, so its tab stays while the connection changes.
const state=computed(()=>status.value?.state??'disconnected')
const tabs=computed(()=>[
 ...(state.value==='connected'?[{value:'apps',label:translate(s.ctx.locale,'apps.page.tab'),list:true}]:[]),
 ...(['disconnected','failed'].includes(state.value)?[{value:'join',label:translate(s.ctx.locale,'nbserver.join.tab')}]:[]),
 {value:'server',label:translate(s.ctx.locale,'nbserver.tab')},
])
const countdown=computed(()=>formatDuration(remaining.value,{locale:uiLocale(s.ctx.locale)}))
</script>
<template>
 <!-- Connected NetBird has nothing to set on the NAS (access is NetBird's policy): the status card and its Apps. -->
 <ReadyGate><ProviderLayout provider="netbird" :tabs="tabs" keep-tabs>
  <template #notice><ServerDependencyBanner :report="server.status.value?.dependencies" :job="server.status.value?.job" :unavailable="server.unavailable.value" path @refresh="server.load" /></template>
  <template #status>
   <!-- The connection status above already says to sign in; this adds only the wait and the code. -->
   <template v-if="status?.phase==='awaiting-login'">
    <Callout tone="info" :dismiss-key="status.userCode||'awaiting-login'" data-nb-awaiting>
     <Space align-items="center" spacing="dense"><span data-nb-countdown>{{n('wait')}}{{labelSeparator(uiLocale(s.ctx.locale))}}{{countdown}}</span><InfoTip icon="info" :content="n('expiry')" :aria-label="n('wait')" /></Space>
    </Callout>
    <KeyValueRow v-if="status.userCode" :label="n('code')"><CopyCutField :model-value="status.userCode" :copy-label="translate(s.ctx.locale,'ux.copyCode')" :aria-label="n('code')" data-nb-code /></KeyValueRow>
   </template>
   <Callout v-if="status?.code&&status.code!=='login_origin_required'&&netbirdKnows(status.code)" tone="warning" :title="translate(s.ctx.locale,'provider.attention')" data-nb-error>{{n(status.code)}}</Callout>
  </template>
  <template #tab-join><Space direction="column" spacing="regular" data-connect-section><LoadingIndicator v-if="s.downloading.value==='netbird'" :text="translate(s.ctx.locale,'connect.downloading')" data-download-progress /><NetBirdConnectForm /></Space></template>
  <template #tab-server="{open}"><NetBirdServer :active="s.page.value==='netbird'&&!!open" /></template>
  <template #tab-apps><AppList way="netbird" :active="s.page.value==='netbird'" /></template>
 </ProviderLayout></ReadyGate>
</template>
