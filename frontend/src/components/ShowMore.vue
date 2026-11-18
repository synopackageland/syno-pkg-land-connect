<script setup lang="ts">
// One closed line on an Overview card: a text button whose chevron points
// right while closed and down once open, and the content under it only then.
import {ref} from 'vue'
import {Button,Space} from '@synopackageland/ui'
import {ChevronDown,ChevronRight} from '@lucide/vue'
defineProps<{title:string}>()
const open=ref(false)
</script>
<template>
 <Space direction="column" spacing="dense">
  <Space><Button variant="ghost" :aria-expanded="open" data-use-more @click="open=!open"><ChevronDown v-if="open" :size="16" aria-hidden="true" /><ChevronRight v-else :size="16" aria-hidden="true" />{{title}}</Button></Space>
  <slot v-if="open" />
 </Space>
</template>
