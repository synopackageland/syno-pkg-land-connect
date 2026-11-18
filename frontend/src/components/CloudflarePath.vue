<script setup lang="ts">
// The way a browser reaches the NAS through Cloudflare, as a picture: one
// lane from any browser, through Cloudflare (a Worker for the Free link, the
// tunnel for an own domain), to Connect on the NAS and its apps. Only short
// labels; the topology draws state with colour and a glyph, and flows only
// while the way works.
import {computed} from 'vue'
import {ConnectionTopology,type LinkState,type TopologyLane,type TopologyNode} from '@synopackageland/ui'
import {KeyRound,Monitor,Server} from '@lucide/vue'
import {cloudflareNode} from '../connect/cloudflareNode'
import type {LinkSpec} from '../connect/topology'
import {OFF_LINK_LABEL} from '../connect/wayPath'
import {translate} from '../i18n'
const props=defineProps<{kind:'free'|'domain';state:LinkState;locale:string;links?:[LinkSpec,LinkSpec]}>()
const t=(key:string)=>translate(props.locale,'cfpage.path.'+key)
const target=computed<TopologyNode>(()=>({id:'nas',label:translate(props.locale,'overview.node.nas'),caption:t('connect'),icon:Server}))
// The NAS dials out to Cloudflare, so no router port opens: said once it works.
// A stopped way (idle) says it is turned off, not that it failed.
const nasLabel=computed(()=>props.state==='idle'?translate(props.locale,OFF_LINK_LABEL):props.state==='active'?t('noPorts'):'')
const lane=computed<TopologyLane>(()=>({
 id:props.kind,
 source:{id:'browser',label:translate(props.locale,'overview.node.anyBrowser'),icon:Monitor},
 via:{id:'cloudflare',...cloudflareNode(props.kind,props.locale)},
 links:props.links?[{state:props.links[0].state,label:translate(props.locale,props.links[0].label),icon:props.links[0].icon?KeyRound:undefined},{state:props.links[1].state,label:translate(props.locale,props.links[1].label),icon:props.links[1].icon?KeyRound:undefined}]:[{state:props.state,label:''},{state:props.state,label:nasLabel.value}],
}))
</script>
<template>
 <div :data-cf-path="kind" :data-path-state="state"><ConnectionTopology :target="target" :lanes="[lane]" :selectable="false" :aria-label="t(kind==='free'?'freeLabel':'domainLabel')" /></div>
</template>
