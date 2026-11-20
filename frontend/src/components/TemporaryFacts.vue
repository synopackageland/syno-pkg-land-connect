<script setup lang="ts">
// What a temporary address is, drawn before words: an address chip whose
// arrow turns once (it changes when the NAS or Connect restarts), then the
// two facts, one line each with its icon, and Cloudflare's upload limit. The wizard's confirm and the
// Cloudflare page show the same; the chip's words are an example, so it is
// hidden from screen readers, which read the two lines.
import {Link2,RotateCw,Users} from '@lucide/vue'
import UploadLimit from './UploadLimit.vue'
import {translate} from '../i18n'
const props=defineProps<{locale:string}>()
const t=(key:string)=>translate(props.locale,'temporary.'+key)
</script>
<template>
 <div class="temporary-facts" data-temporary-facts>
  <span class="temporary-chip" aria-hidden="true"><Link2 :size="14" /><span>random-words.trycloudflare.com</span><RotateCw :size="14" class="temporary-turn" /></span>
  <ul class="temporary-lines">
   <li data-temporary-fact="changes"><RotateCw :size="16" aria-hidden="true" /><span>{{t('changes')}}</span></li>
   <li data-temporary-fact="anyone"><Users :size="16" aria-hidden="true" /><span>{{t('anyone')}}</span></li>
  </ul>
  <UploadLimit :locale="locale" way="cloudflare-quick" />
 </div>
</template>
<style scoped>
.temporary-facts{display:flex;flex-direction:column;align-items:flex-start;gap:8px}
.temporary-chip{display:inline-flex;align-items:center;gap:6px;max-width:100%;padding:2px 8px;border:1px solid var(--ui-border);border-radius:var(--ui-radius-sm);background:var(--ui-surface-inset);color:var(--ui-text-secondary);font-family:var(--ui-font-mono);font-size:var(--ui-font-size-sm);line-height:20px}
.temporary-chip > span{overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.temporary-turn{flex:none;animation:temporary-turn 1.2s var(--ui-motion-ease,ease) .3s 1}
@keyframes temporary-turn{to{transform:rotate(360deg)}}
.temporary-lines{display:flex;flex-direction:column;gap:4px;margin:0;padding:0;list-style:none}
.temporary-lines li{display:flex;align-items:flex-start;gap:8px;line-height:20px}
.temporary-lines li > svg{flex:none;margin-top:2px;color:var(--ui-text-secondary)}
@media (prefers-reduced-motion:reduce){.temporary-turn{animation:none}}
</style>
