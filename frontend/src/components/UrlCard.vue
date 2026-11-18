<script setup lang="ts">
// One address the user can copy, open in a new tab or scan from a phone.
import {computed,ref} from 'vue'
import {Button,CopyCutField,Space} from '@synopackageland/ui'
import {qrPath} from '../connect/qr'
import {translate} from '../i18n'
const props=defineProps<{url:string;locale:string;label:string}>()
const t=(key:string)=>translate(props.locale,key)
const showQr=ref(false)
const qr=computed(()=>qrPath(props.url))
function open(){window.open(props.url,'_blank','noopener,noreferrer')}
</script>
<template>
 <Space direction="column" spacing="dense" data-url-card>
  <CopyCutField :model-value="url" :copy-label="t('ux.copy')" :aria-label="label" />
  <Space spacing="dense">
   <Button v-if="/^https?:\/\//.test(url)" data-url-open @click="open">{{t('connect.url.open')}}</Button>
   <Button data-url-qr :aria-expanded="showQr" @click="showQr=!showQr">{{t('connect.url.qr')}}</Button>
  </Space>
  <svg v-if="showQr" data-qr role="img" :aria-label="t('connect.url.qrLabel').replace('{url}',url)" :viewBox="`-2 -2 ${qr.size+4} ${qr.size+4}`" width="160" height="160" shape-rendering="crispEdges"><rect x="-2" y="-2" :width="qr.size+4" :height="qr.size+4" fill="var(--ui-surface)" /><path :d="qr.d" fill="var(--ui-text)" /></svg>
 </Space>
</template>
