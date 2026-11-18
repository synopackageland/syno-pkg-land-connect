<script setup lang="ts">
import {Button,Callout,KeyValueRow,Space} from '@synopackageland/ui'
import {computed} from 'vue'
import type {SupervisionStatus} from '../context'
import {translate} from '../i18n'
const props=defineProps<{snapshot:SupervisionStatus;locale:string;busy?:boolean;provider?:string}>()
defineEmits<{retry:[]}>()
const reason=computed(()=>props.snapshot.lastExit?.reason??props.snapshot.reason)
const storage=computed(()=>props.snapshot.state==='failed'&&['tmpfs_budget_exceeded','tmpfs_storage_unavailable'].includes(reason.value??''))
const storageKey=computed(()=>reason.value==='tmpfs_storage_unavailable'?'storage.unavailable':'storage')
const text=(key:string)=>translate(props.locale,'supervision.'+key).replace('{provider}',props.provider==='tailscale'?'Tailscale':props.provider==='netbird'?'NetBird':props.provider??'')
</script>
<template>
 <Callout v-if="['waiting','backoff','failed'].includes(snapshot.state)" :dismissible="!storage" :dismiss-key="snapshot.state" :tone="snapshot.state==='failed'?'danger':'info'" :title="text(storage?storageKey+'.title':snapshot.state)" data-supervision-state :data-state="snapshot.state">
  <span v-if="storage" data-storage-reason>{{text(storageKey+'.body')}}</span>
  <Space v-else direction="column" spacing="dense">
   <KeyValueRow :label="translate(locale,'supervision.attempts')">{{snapshot.attempts}}</KeyValueRow>
   <KeyValueRow v-if="snapshot.state==='backoff'&&snapshot.nextRetry" :label="translate(locale,'supervision.next')">{{new Date(snapshot.nextRetry).toLocaleTimeString()}}</KeyValueRow>
   <KeyValueRow v-if="snapshot.lastExit" :label="translate(locale,'supervision.exit')">{{snapshot.lastExit.code}} · {{snapshot.lastExit.reason}}</KeyValueRow>
  </Space>
  <template v-if="snapshot.state==='failed'" #actions><Button :disabled="busy" :loading="busy" data-supervision-retry @click="$emit('retry')">{{text(storage?'storage.restart':'retry')}}</Button></template>
 </Callout>
</template>
