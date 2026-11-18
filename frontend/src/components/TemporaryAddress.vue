<script setup lang="ts">
// The temporary address (a Cloudflare quick tunnel) on the Cloudflare page's
// Temporary tab: what it is (TemporaryFacts, as in the wizard), then one
// Start, or Stop once it runs; the page's Connection status shows the address
// itself. It starts the way the wizard's Start does, and only while
// Cloudflare runs nothing else.
import {computed,ref} from 'vue'
import CloudflareOffDialog from './CloudflareOffDialog.vue'
import {Button,Space} from '@synopackageland/ui'
import TemporaryFacts from './TemporaryFacts.vue'
import {useConnectState} from '../connect/state'
import {addressesOf} from '../connect/addresses'
import {startTemporary,temporaryAvailable} from '../connect/wizard'
import {translate} from '../i18n'
const s=useConnectState()
const offOpen=ref(false)
const t=(key:string)=>translate(s.ctx.locale,key)
const status=computed(()=>s.connectionFor('cloudflare'))
const address=computed(()=>status.value?.mode==='quick'&&status.value.state==='connected'?addressesOf(status.value)[0]:undefined)
const available=computed(()=>temporaryAvailable(s.connections.value))
const starting=computed(()=>s.starting.value==='cloudflare'||status.value?.mode==='quick'&&status.value.state==='connecting')
</script>
<template>
 <Space direction="column" spacing="regular" data-cf-temporary-address>
  <TemporaryFacts :locale="s.ctx.locale" />
  <Space v-if="address" spacing="compact"><Button variant="secondary" data-cf-temporary-stop :disabled="s.busy.value" @click="offOpen=true">{{t('ux.stop')}}</Button></Space>
  <span v-else-if="!available" data-cf-temporary-busy>{{t('cfpage.temporary.busy')}}</span>
  <Space v-else spacing="compact"><Button variant="primary" data-cf-quick :loading="starting" :disabled="s.busy.value" @click="startTemporary(s)">{{t('cloudflare.quick')}}</Button></Space>
 </Space>
 <CloudflareOffDialog v-model:open="offOpen" />
</template>
