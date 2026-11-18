<script setup lang="ts">
// The way to a custom app as one path: visitor → Cloudflare → DSM sign-in →
// the app. With Connect's sign-in on, the third stop checks every visitor
// first. With it off, that stop is skipped and Cloudflare reaches the app's
// own sign-in page directly: the stop fades and is struck through and its
// lines turn to a warning. Theme colours keep light and dark; the flowing
// lines hold still under prefers-reduced-motion. Words are catalog labels.
import {computed} from 'vue'
import {AppWindow,Cloud,ShieldCheck,ShieldOff,Users} from '@lucide/vue'
import {translate} from '../i18n'
const props=defineProps<{locale:string;app:string;gate:boolean}>()
const t=(key:string)=>translate(props.locale,'apps.custom.path.'+key)
const label=computed(()=>[t('visitor'),t('cloudflare'),...(props.gate?[t('gate')]:[]),props.app||t('app')].join(' → ')+' · '+t(props.gate?'gateOn':'gateOff'))
</script>
<template>
 <figure class="custom-path" :data-gate="gate?'on':'off'" data-custom-path role="img" :aria-label="label">
  <div class="custom-path-row" aria-hidden="true">
   <span class="custom-path-node"><Users :size="18" /><span>{{t('visitor')}}</span></span>
   <span class="custom-path-link" />
   <span class="custom-path-node"><Cloud :size="18" /><span>{{t('cloudflare')}}</span></span>
   <span class="custom-path-link custom-path-link-gate" />
   <span class="custom-path-node custom-path-gate" data-custom-path-gate>
    <component :is="gate?ShieldCheck:ShieldOff" :size="18" /><span>{{t(gate?'gate':'skipped')}}</span>
   </span>
   <span class="custom-path-link custom-path-link-gate" />
   <span class="custom-path-node"><AppWindow :size="18" /><span>{{app||t('app')}}</span></span>
  </div>
  <figcaption class="custom-path-caption">{{t(gate?'gateOn':'gateOff')}}</figcaption>
 </figure>
</template>
<style scoped>
.custom-path{margin:0;display:flex;flex-direction:column;gap:6px;padding:10px 12px;border:1px solid var(--ui-border-subtle,var(--ui-border));border-radius:var(--ui-radius-sm,6px);background:var(--ui-surface-subtle,var(--ui-surface))}
.custom-path-row{display:grid;grid-template-columns:auto minmax(16px,1fr) auto minmax(16px,1fr) auto minmax(16px,1fr) auto;align-items:center;gap:6px}
.custom-path-node{display:inline-flex;flex-direction:column;align-items:center;gap:2px;max-width:7.5rem;color:var(--ui-text);font-size:var(--ui-font-size-sm,12px);text-align:center;line-height:1.25}
.custom-path-node>span{overflow-wrap:anywhere}
.custom-path-gate{color:var(--ui-success-text,var(--ui-success))}
.custom-path-link{height:2px;background-image:linear-gradient(90deg,var(--ui-primary) 50%,transparent 50%);background-size:8px 2px;animation:custom-flow 1.2s linear infinite}
[data-gate=off] .custom-path-gate{color:var(--ui-warning-text,var(--ui-warning));opacity:.75}
[data-gate=off] .custom-path-gate>span{text-decoration:line-through}
[data-gate=off] .custom-path-link-gate{background-image:linear-gradient(90deg,var(--ui-warning) 50%,transparent 50%)}
.custom-path-caption{color:var(--ui-text-secondary);font-size:var(--ui-font-size-sm,12px)}
[data-gate=off] .custom-path-caption{color:var(--ui-warning-text,var(--ui-warning))}
@keyframes custom-flow{to{background-position:8px 0}}
@media (prefers-reduced-motion:reduce){.custom-path-link{animation:none}}
</style>
