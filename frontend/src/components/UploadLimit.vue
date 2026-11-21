<script setup lang="ts">
// One line, the same wherever a Cloudflare way shows (the Cloudflare page's
// tabs, the temporary address's facts, a public App approval): an info icon
// whose tooltip says why, then an upload icon with "large files work in a
// web browser" and the limit other apps keep (100 MB per file, or the
// temporary address's 100 seconds per upload) and a
// download icon with "no limit". The separator ends the upload half, so a
// narrow screen wraps after it and never starts a line with it. Nothing
// shows for a way the catalog gives no upload limit.
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
 <span v-if="limit" class="upload-limit" data-upload-limit :data-upload-limit-mb="limit.kind==='size'?limit.mb:undefined" :data-upload-limit-seconds="limit.kind==='time'?limit.seconds:undefined">
  <InfoTip icon="info" :content="parts?.detail" :aria-label="t('label')" data-upload-limit-info />
  <span class="upload-limit-part"><Upload :size="16" aria-hidden="true" /><span data-upload-limit-up>{{parts?.upload}}</span><span class="upload-limit-sep" aria-hidden="true" data-upload-limit-sep>·</span></span>
  <span class="upload-limit-part"><Download :size="16" aria-hidden="true" /><span data-upload-limit-down>{{parts?.download}}</span></span>
 </span>
</template>
<style scoped>
.upload-limit{display:inline-flex;flex-wrap:wrap;align-items:center;gap:4px 8px;line-height:20px}
.upload-limit-part{display:inline-flex;align-items:center;gap:6px}
.upload-limit-part > svg{flex:none;color:var(--ui-text-secondary)}
.upload-limit-sep{flex:none;margin-left:2px;color:var(--ui-text-secondary)}
</style>
