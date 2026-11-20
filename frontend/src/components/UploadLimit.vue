<script setup lang="ts">
// One line, the same wherever a Cloudflare way shows (the Cloudflare page's
// tabs, the temporary address's facts, a public App approval): an info icon
// whose tooltip says why, then an upload icon with Cloudflare's per-file
// limit and a download icon with "no limit". Nothing shows for a way the
// catalog gives no upload limit.
import {computed} from 'vue'
import {Download,Upload} from '@lucide/vue'
import {InfoTip} from '@synopackageland/ui'
import {uploadLimit,uploadLimitParts,type CloudflarePlan,type CloudflareWayId} from '../connect/uploadLimit'
import {translate} from '../i18n'
const props=withDefaults(defineProps<{locale:string;way?:CloudflareWayId;plan?:CloudflarePlan}>(),{way:'cloudflare-domain',plan:'free'})
const t=(key:string)=>translate(props.locale,'cflimit.'+key)
const limit=computed(()=>uploadLimit(props.way,props.plan))
const parts=computed(()=>limit.value?uploadLimitParts(props.locale,limit.value):undefined)
</script>
<template>
 <span v-if="limit" class="upload-limit" data-upload-limit :data-upload-limit-mb="limit.mb">
  <InfoTip icon="info" :content="t('detail')" :aria-label="t('label')" data-upload-limit-info />
  <span class="upload-limit-part"><Upload :size="16" aria-hidden="true" /><span data-upload-limit-up>{{parts?.upload}}</span></span>
  <span class="upload-limit-sep" aria-hidden="true">·</span>
  <span class="upload-limit-part"><Download :size="16" aria-hidden="true" /><span data-upload-limit-down>{{parts?.download}}</span></span>
 </span>
</template>
<style scoped>
.upload-limit{display:inline-flex;flex-wrap:wrap;align-items:center;gap:4px 8px;line-height:20px}
.upload-limit-part{display:inline-flex;align-items:center;gap:6px}
.upload-limit-part > svg{flex:none;color:var(--ui-text-secondary)}
.upload-limit-sep{color:var(--ui-text-secondary)}
</style>
