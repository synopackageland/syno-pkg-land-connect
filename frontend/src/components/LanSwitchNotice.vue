<script setup lang="ts">
// Shown when this page came through Cloudflare and this browser has just
// proved it reaches DSM directly on the local network (see connect/lanSwitch
// and useLanFound). DSM's desktop cannot be changed from here, so the notice
// only opens DSM's local address in a new tab, where the visitor signs in again.
import {Callout,Link} from '@synopackageland/ui'
import type {LanFound} from '../connect/lanSwitch'
import {translate} from '../i18n'
const props=defineProps<{found:LanFound;locale:string}>()
const t=(key:string)=>translate(props.locale,'lanswitch.'+key)
</script>
<template>
 <Callout tone="info" :title="t('found')" dismiss-scope="occurrence" data-lan-switch-notice>
  <span>{{t('signin')}}<template v-if="props.found.https"> {{t('certificate')}}</template></span>
  <template #actions><Link :href="props.found.url" external data-lan-switch-open>{{t('open')}}</Link></template>
 </Callout>
</template>
