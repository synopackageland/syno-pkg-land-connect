<script setup lang="ts">
// A way drawn as one path: who connects → what carries it → the NAS or an App.
// While connecting the links flow (ConnectionTopology animates pending links
// and holds still under prefers-reduced-motion); colours come from the theme,
// so light and dark follow DSM. The words are DOM labels from the catalogs.
import {computed,type Component} from 'vue'
import {ConnectionTopology,type LinkState,type TopologyNode} from '@synopackageland/ui'
import {AppWindow,Cloud,Globe,Monitor,Network,Server,Shield,Smartphone} from '@lucide/vue'
import {pathModel,PHASE_TAG,type PathNode,type PathWay,type WayPhase} from '../connect/wayPath'
import {translate} from '../i18n'
const props=withDefaults(defineProps<{
 way:PathWay
 locale:string
 phase?:WayPhase
 /** Draws this App as the far end instead of the NAS. */
 app?:string
 /** Per-link states, e.g. a wizard step where the first link is done and the second still flows. */
 links?:[LinkState,LinkState]
 /** Short translated labels on the links; none by default. */
 linkLabels?:[string?,string?]
 /** The stacked small form for cards and dialogs; false draws the Overview's wide form. */
 compact?:boolean
 /** Lines kept under every node, so paths side by side (cards in a row) keep one height. */
 labelLines?:number
}>(),{phase:'off',compact:true,labelLines:undefined})
const t=(key:string)=>translate(props.locale,key)
const icons:Record<PathNode,Component>={anyone:Monitor,devices:Smartphone,internet:Globe,cloudflare:Cloud,workers:Cloud,tailscale:Network,netbird:Shield,nas:Server,app:AppWindow}
const node=(id:PathNode,label?:string):TopologyNode=>({id,label:label??t(id==='anyone'?'overview.node.anyBrowser':'path.node.'+id),icon:icons[id]})
const model=computed(()=>pathModel(props.way,props.phase,{app:props.app,links:props.links,linkLabels:props.linkLabels}))
const target=computed(()=>node(model.value.target,props.app))
const lanes=computed(()=>[{id:props.way,source:node(model.value.source),via:node(model.value.via),links:model.value.links}])
// One sentence for screen readers: the nodes in order and the status word.
const label=computed(()=>[lanes.value[0].source.label,lanes.value[0].via!.label,target.value.label].join(' → ')+' · '+t(PHASE_TAG[props.phase].label))
</script>
<template>
 <div data-way-path :data-way="way" :data-phase="phase">
  <ConnectionTopology :target="target" :lanes="lanes" :compact="compact" :selectable="false" :aria-label="label" :label-lines="labelLines" />
 </div>
</template>
