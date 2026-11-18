<script setup lang="ts">
// One compact row under a name field: what holds the name, then one line of
// actions (reuse a leftover or use the checked alternative, open it in
// Cloudflare, check again) and at most one short hint. The name field itself
// changes only when the alternative is chosen.
import {computed} from 'vue'
import CloudResourceGuide from './CloudResourceGuide.vue'
import {Button,Link,Space,Tag} from '@synopackageland/ui'
import {cloudflareDashboard,type NameConflict} from '../connect/nameConflict'
import {translate} from '../i18n'
const props=defineProps<{locale:string;conflict:NameConflict;disabled?:boolean}>()
const emit=defineEmits<{reuse:[];use:[name:string];check:[]}>()
const t=(key:string)=>translate(props.locale,'cloudflare.conflict.'+key)
const reusable=computed(()=>props.conflict.reason==='leftover'&&!!props.conflict.reuseRecord)
// An active Connect is seen on its tunnel; anything else on its DNS record.
const dashboard=computed(()=>cloudflareDashboard(props.conflict.reason==='other_connect'?props.conflict.tunnelUrl??props.conflict.dnsUrl:props.conflict.dnsUrl))
</script>
<template>
 <CloudResourceGuide v-if="conflict.objectType" :locale="locale" :conflict="conflict" :busy="disabled" @retry="emit('check')" @alternative="conflict.suggestedName&&emit('use',conflict.suggestedName)" />
 <Space v-else direction="column" spacing="dense" data-name-conflict :data-reason="conflict.reason" role="status">
  <Space spacing="dense" align-items="center" wrap><Tag appearance="warning" :label="t(conflict.reason)" /><strong data-name-conflict-name>{{conflict.conflictName}}</strong></Space>
  <Space spacing="regular" align-items="center" wrap>
   <Button v-if="reusable" :disabled="disabled" data-name-reuse @click="emit('reuse')">{{t('reuse')}}</Button>
   <Button v-else-if="conflict.suggestedName" :disabled="disabled" data-name-use @click="emit('use',conflict.suggestedName!)">{{t('use').replace('{name}',conflict.suggestedName)}}</Button>
   <Link v-if="dashboard" :href="dashboard" external data-name-dns>{{t('open')}}</Link>
   <Button :disabled="disabled||!conflict.conflictName" data-name-check @click="emit('check')">{{t('check')}}</Button>
  </Space>
  <span data-name-hint>{{t('hint.'+conflict.reason)}}</span>
 </Space>
</template>
