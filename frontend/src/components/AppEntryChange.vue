<script setup lang="ts">
import {Button,Callout,CellText,Space} from '@synopackageland/ui'
import {entryLabel,entryChangeTitleKey,protectionLines,type Protection,type EntryChange} from '../connect/appUrls'
import {translate} from '../i18n'
defineProps<{locale:string;change:EntryChange;busy?:boolean}>()
function protection(p:Protection|undefined,locale:string):string {
 if(!p)return translate(locale,'apps.entry.unknownProtection')
 const lines=protectionLines(p,'private').map(l=>translate(locale,'apps.protection.'+l.key).replace('{paths}',l.paths??''))
 if(p.entry)lines.push(translate(locale,'apps.entry.gate.'+p.entry))
 return lines.join(' · ')
}
const emit=defineEmits<{update:[]}>()
</script>
<template>
 <Space direction="column" spacing="dense" data-entry-change>
  <Callout tone="warning" :title="translate(locale,'apps.'+entryChangeTitleKey(change))" :dismiss-key="entryLabel(change.after)">
   {{translate(locale,change.reason==='protection_changed'?'apps.entry.protection':'apps.entry.review')}}
  </Callout>
  <CellText :text="translate(locale,'apps.entry.before')" :secondary="entryLabel(change.before)" wrap data-entry-before />
  <CellText :text="translate(locale,'apps.entry.after')" :secondary="entryLabel(change.after)" wrap data-entry-after />
  <CellText :text="translate(locale,'apps.entry.protectionBefore')" :secondary="protection(change.beforeProtection,locale)" wrap />
  <CellText v-if="change.afterProtection" :text="translate(locale,'apps.entry.protectionAfter')" :secondary="protection(change.afterProtection,locale)" wrap />
  <Button :disabled="busy" data-entry-update @click="emit('update')">{{translate(locale,'apps.entry.update')}}</Button>
 </Space>
</template>
