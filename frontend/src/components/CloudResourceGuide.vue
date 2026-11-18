<script setup lang="ts">
import {computed} from 'vue'
import {Button,Callout,CopyCutField,Link,Space} from '@synopackageland/ui'
import {cloudflareDashboard,type NameConflict} from '../connect/nameConflict'
import {translate} from '../i18n'
const props=defineProps<{conflict:NameConflict;locale:string;busy?:boolean}>()
const emit=defineEmits<{retry:[];alternative:[]}>()
const t=(key:string)=>translate(props.locale,'recovery.'+key)
const kind=computed(()=>props.conflict.objectType??'dns')
const dashboard=computed(()=>cloudflareDashboard(props.conflict.dashboardUrl??props.conflict.dnsUrl))
const where=computed(()=>t('where.'+kind.value).replace('{name}',props.conflict.conflictName??''))
</script>
<template>
 <Space direction="column" spacing="regular" data-resource-conflict-guide>
  <Callout tone="warning" :title="t('conflict.'+kind)" :dismiss-key="conflict.conflictName">{{t('foreign')}}</Callout>
  <CopyCutField :model-value="conflict.conflictName" :copy-label="t('copy')" :aria-label="t('name')" :input-type="kind==='route'?'textarea':'text'" :autosize="true" data-resource-conflict-name />
  <span class="resource-guide-location">{{where}}</span>
  <span>{{t('deleteWhenUnused')}}</span>
  <Space wrap>
   <Link v-if="dashboard" :href="dashboard" external data-resource-conflict-open>{{t('open')}}</Link>
   <Button v-if="conflict.suggestedName" :disabled="busy" data-resource-conflict-alternative @click="emit('alternative')">{{t('another')}}</Button>
   <Button :loading="busy" :disabled="busy" data-resource-conflict-retry @click="emit('retry')">{{translate(locale,'apps.retry')}}</Button>
  </Space>
 </Space>
</template>

<style scoped>
.resource-guide-location {min-width:0;overflow-wrap:anywhere}
</style>
