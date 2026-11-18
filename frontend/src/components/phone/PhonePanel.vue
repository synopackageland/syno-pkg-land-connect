<script setup lang="ts">
// The Phone section's panel, in three columns: how to open the phone page
// (#scan: a heading and the route picker above this route's code, address
// and need), the phone with a live demo of the page (#phone: what goes under
// it), and what to do next (the default slot). Narrower, the phone keeps the
// left column and the other two stack beside it; narrower still, everything
// stacks, the code first.
import type {PhoneRoute} from '../../connect/phoneRoutes'
import PhoneFrame from './PhoneFrame.vue'
import PhoneQr from './PhoneQr.vue'
defineProps<{route:PhoneRoute;locale:string}>()
</script>
<template>
 <div class="phone-panel" data-phone-panel>
  <div class="phone-panel__grid">
   <div class="phone-panel__scan" data-phone-column="scan">
    <slot name="scan" />
    <PhoneQr :route="route" :locale="locale" :size="208" />
   </div>
   <div class="phone-panel__phone" data-phone-column="phone">
    <PhoneFrame :locale="locale" />
    <slot name="phone" />
   </div>
   <div class="phone-panel__next" data-phone-column="next">
    <slot />
   </div>
  </div>
 </div>
</template>
<style scoped>
/* The grey surface fills the section; the columns sit centered in it, each
   with a cap so a long address or step wraps instead of widening it. In
   three columns the code and the next steps sit level with the phone's middle. */
.phone-panel{container-type:inline-size;box-sizing:border-box;padding:var(--ui-space-6,24px) var(--ui-space-4);border-radius:var(--ui-radius-md);background:var(--ui-surface-subtle)}
.phone-panel__grid{display:grid;grid-template-columns:minmax(220px,280px) auto minmax(220px,320px);grid-template-areas:"scan phone next";justify-content:center;align-items:center;gap:var(--ui-space-6,24px) var(--ui-space-8,40px)}
.phone-panel__scan{grid-area:scan;display:flex;flex-direction:column;gap:var(--ui-space-3);min-width:0}
.phone-panel__phone{grid-area:phone;display:flex;flex-direction:column;align-items:center;gap:var(--ui-space-2);min-width:0}
.phone-panel__phone :deep([data-phone-frame]){--w:288px}
.phone-panel__next{grid-area:next;display:flex;flex-direction:column;gap:var(--ui-space-3);min-width:0}
/* Two columns: the phone on the left, the code above the next steps */
@container (max-width:900px){
 .phone-panel__grid{grid-template-columns:auto minmax(220px,320px);grid-template-areas:"phone scan" "phone next";grid-template-rows:auto 1fr;align-items:start}
 .phone-panel__phone :deep([data-phone-frame]){--w:264px}
}
/* One column: the code first, then the phone, then the next steps */
@container (max-width:600px){
 .phone-panel__grid{grid-template-columns:minmax(0,360px);grid-template-areas:"scan" "phone" "next";grid-template-rows:none;align-items:start}
 .phone-panel__phone :deep([data-phone-frame]){--w:240px}
}
</style>
