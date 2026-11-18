<script setup lang="ts">
// One route's phone code: the QR code of the phone browser page, its full
// address (wrapped, never cut) with Copy, and at most one line on what the
// route needs from the phone. The code stays black on white in both themes
// so every phone camera reads it; it is drawn here, no service sees the text.
import {computed,type Component} from 'vue'
import {CopyButton} from '@synopackageland/ui'
import {Clock,Download,ShieldAlert,Wifi} from '@lucide/vue'
import {qrPath} from '../../connect/qr'
import {routeNeed,type PhoneNote,type PhoneRoute} from '../../connect/phoneRoutes'
import {translate} from '../../i18n'
const props=withDefaults(defineProps<{route:PhoneRoute;locale:string;size?:number}>(),{size:144})
const t=(key:string)=>translate(props.locale,key)
const qr=computed(()=>qrPath(props.route.link))
const ICONS:Record<PhoneNote,{icon:Component;warning?:boolean}>={
 netbirdApp:{icon:Download},tailscaleApp:{icon:Download},sameNetwork:{icon:Wifi},selfSigned:{icon:ShieldAlert,warning:true},temporary:{icon:Clock,warning:true},
}
const need=computed(()=>{const n=routeNeed(props.route);return n?{id:n,...ICONS[n],text:t('phone.need.'+n)}:undefined})
</script>
<template>
 <div class="phone-qr" :data-phone-route="route.id">
  <svg class="phone-qr__code" data-phone-route-qr role="img" :aria-label="t('connect.url.qrLabel').replace('{url}',route.link)" :viewBox="`-4 -4 ${qr.size+8} ${qr.size+8}`" :width="size" :height="size" shape-rendering="crispEdges"><rect x="-4" y="-4" :width="qr.size+8" :height="qr.size+8" fill="#fff" /><path :d="qr.d" fill="#000" /></svg>
  <div class="phone-qr__text">
   <div class="phone-qr__line">
    <span class="phone-qr__url" data-phone-route-url>{{route.link}}</span>
    <CopyButton :text="route.link" :label="t('ux.copy')" size="sm" data-phone-route-copy />
   </div>
   <span v-if="need" class="phone-qr__need" :class="{'phone-qr__need--warning':need.warning}" :data-phone-note="need.id"><component :is="need.icon" :size="14" aria-hidden="true" />{{need.text}}</span>
  </div>
 </div>
</template>
<style scoped>
.phone-qr{display:flex;flex-direction:column;align-items:flex-start;gap:var(--ui-space-2);min-width:0}
.phone-qr__code{display:block;flex:none;border:1px solid var(--ui-border);border-radius:var(--ui-radius-sm)}
.phone-qr__text{display:flex;flex-direction:column;gap:var(--ui-space-1);min-width:0;max-width:100%}
.phone-qr__line{display:flex;align-items:flex-start;gap:var(--ui-space-1);min-width:0}
.phone-qr__url{min-width:0;overflow-wrap:anywhere;font-family:var(--ui-font-mono);color:var(--ui-text-secondary);font-size:12px;line-height:1.5;padding-top:3px}
.phone-qr__need{display:inline-flex;align-items:center;gap:var(--ui-space-1);color:var(--ui-text-secondary);font-size:12px}
.phone-qr__need--warning{color:var(--ui-warning-text)}
</style>
