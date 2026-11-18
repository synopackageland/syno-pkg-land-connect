<script setup lang="ts">
import {computed} from 'vue'
import {LoadingIndicator,Space} from '@synopackageland/ui'
import {useConnectState} from '../connect/state'
import {translate} from '../i18n'
import ReadyGate from './ReadyGate.vue'
import ProviderLayout from '../components/ProviderLayout.vue'
import TailscaleConnectForm from '../components/connect-forms/TailscaleConnectForm.vue'
import AppList from '../components/AppList.vue'
const s=useConnectState()
// Off, Join holds the connect form; connected, Apps lists the Apps on
// Tailscale. Tailnet devices reach DSM and any NAS port at its Tailscale
// address directly, so the page sets no services.
const state=computed(()=>s.connectionFor('tailscale')?.state??'disconnected')
const tabs=computed(()=>[
 ...(state.value==='connected'?[{value:'apps',label:translate(s.ctx.locale,'apps.page.tab'),list:true}]:[]),
 ...(['disconnected','failed'].includes(state.value)?[{value:'join',label:translate(s.ctx.locale,'nbserver.join.tab')}]:[]),
])
</script>
<template>
 <ReadyGate><ProviderLayout provider="tailscale" :tabs="tabs" keep-tabs>
  <template #tab-join><Space direction="column" spacing="regular" data-connect-section><LoadingIndicator v-if="s.downloading.value==='tailscale'" :text="translate(s.ctx.locale,'connect.downloading')" data-download-progress /><TailscaleConnectForm /></Space></template>
  <template #tab-apps><AppList way="private" :active="s.page.value==='tailscale'" /></template>
 </ProviderLayout></ReadyGate>
</template>
