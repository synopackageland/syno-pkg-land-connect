<script setup lang="ts">
import {computed} from 'vue'
import {Callout,Input,Space} from '@synopackageland/ui'
import {hostnameIssue,splitHostname} from '../connect/hostname'
import {translate} from '../i18n'
const props=defineProps<{modelValue:string;hostname:string;locale:string;disabled?:boolean}>()
const emit=defineEmits<{'update:modelValue':[value:string]}>()
const suffix=computed(()=>splitHostname(props.hostname).suffix)
const label=computed(()=>props.modelValue.endsWith(suffix.value)?props.modelValue.slice(0,-suffix.value.length):splitHostname(props.modelValue).label)
const issue=computed(()=>hostnameIssue(label.value+suffix.value,props.hostname))
</script>
<template>
 <Space direction="column" spacing="dense" data-hostname-field>
  <Input :model-value="label" :disabled="disabled" :invalid="!!issue" :aria-label="translate(locale,'apps.hostname')" autocomplete="off" autocapitalize="none" spellcheck="false" @update:model-value="emit('update:modelValue',$event+suffix)"><template #unit><span data-hostname-suffix>{{suffix}}</span></template></Input>
  <Callout v-if="issue" tone="danger" variant="inline" :dismissible="false" role="alert">{{translate(locale,'apps.hostname.'+issue)}}</Callout>
 </Space>
</template>
