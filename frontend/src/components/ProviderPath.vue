<script setup lang="ts">
// A provider's path on its status card: the shared WayPath, with the links
// of the Overview lane once the provider runs, so its reach check shows here too.
// While the way is off its NAS-side link says so ("Turned off"), never a failure.
import {computed} from 'vue'
import WayPath from './WayPath.vue'
import {useConnectState} from '../connect/state'
import {pathLinks,pathWay} from '../connect/providerStatus'
import {phaseLinkLabels,wayPhase} from '../connect/wayPath'
import {translate} from '../i18n'
import type {CheckResult} from '../connect/checks'
const props=defineProps<{provider:string;check?:CheckResult}>()
const s=useConnectState()
const status=computed(()=>s.connectionFor(props.provider))
const phase=computed(()=>wayPhase(status.value))
const links=computed(()=>pathLinks(props.provider,status.value,props.check,s.statusUnavailable.value))
const labels=computed<[string?,string?]|undefined>(()=>{
 const spec=phaseLinkLabels(phase.value)
 if(!spec)return undefined
 const [left,right]=spec
 return [left?translate(s.ctx.locale,left):left,right?translate(s.ctx.locale,right):right]
})
</script>
<template>
 <div data-provider-path :data-path-state="links[1]"><WayPath :way="pathWay(provider,status)" :locale="s.ctx.locale" :phase="phase" :links="links" :link-labels="labels" /></div>
</template>
