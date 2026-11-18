<script setup lang="ts">
// The picture of nothing set up yet, on the Overview and the App URLs empty
// state: your devices and the NAS as the wizard's topology cards, with the
// way between them still to be chosen (a dashed card with a question mark).
// The links are short themed strokes, not grey tracks; colours come from the
// theme, so light and dark follow DSM. The words are DOM labels.
import {computed} from 'vue'
import {CircleHelp,Server,Smartphone} from '@lucide/vue'
import {translate} from '../i18n'
const props=defineProps<{locale:string}>()
const t=(key:string)=>translate(props.locale,key)
const label=computed(()=>`${t('path.node.devices')} → ${t('path.node.nas')} · ${t('path.phase.off')}`)
</script>
<template>
 <div class="no-way" role="img" :aria-label="label" data-no-way-picture>
  <div class="no-way-node" data-no-way-node="devices"><Smartphone class="no-way-icon" aria-hidden="true" /><span>{{t('path.node.devices')}}</span></div>
  <span class="no-way-link" aria-hidden="true" />
  <div class="no-way-node" data-no-way-node="way" data-state="none"><CircleHelp class="no-way-icon" aria-hidden="true" /><span>{{t('path.node.none')}}</span></div>
  <span class="no-way-link" aria-hidden="true" />
  <div class="no-way-node" data-no-way-node="nas"><Server class="no-way-icon" aria-hidden="true" /><span>{{t('path.node.nas')}}</span></div>
 </div>
</template>
<style scoped>
.no-way{display:flex;align-items:center;justify-content:center;width:100%;max-width:560px;margin-inline:auto}
.no-way-node{display:flex;flex:0 1 148px;min-width:0;min-height:96px;flex-direction:column;align-items:center;justify-content:center;gap:var(--ui-space-2);padding:var(--ui-space-3);box-sizing:border-box;border:1px solid var(--ui-border);border-radius:var(--ui-radius-lg);background:var(--ui-surface-raised);box-shadow:var(--ui-shadow-control);color:var(--ui-text);font-size:var(--ui-font-size-sm);font-weight:600;line-height:16px;text-align:center}
.no-way-node>span{overflow-wrap:anywhere}
.no-way-icon{width:28px;height:28px;flex:none;color:var(--ui-text-secondary)}
/* The way still to be chosen: dashed, in the primary tint. */
.no-way-node[data-state=none]{border:1.5px dashed var(--ui-primary);background:var(--ui-primary-soft);box-shadow:none;color:var(--ui-primary)}
.no-way-node[data-state=none] .no-way-icon{color:var(--ui-primary)}
.no-way-link{flex:1 1 24px;min-width:12px;max-width:56px;height:0;border-top:2px dashed var(--ui-primary);opacity:.55}
@media (max-width:480px){.no-way-node{min-height:80px;padding:var(--ui-space-2)}.no-way-icon{width:22px;height:22px}}
</style>
