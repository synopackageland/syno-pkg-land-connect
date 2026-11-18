<script setup lang="ts">
// A risk as an inline note: one sentence and "Learn more", the given
// documentation, else the provider page's.
import {inject} from 'vue'
import {Callout,Link} from '@synopackageland/ui'
import {useConnectState} from '../connect/state'
import {learnMoreKey} from '../connect/pageTabs'
import {translate} from '../i18n'
defineProps<{text:string;href?:string}>()
const s=useConnectState()
const pageDocs=inject(learnMoreKey,undefined)
</script>
<template>
 <!-- "Learn more" sits in the sentence, not #actions: ui's actions are a flex item that drops to its own line once the text fills the row. -->
 <Callout tone="warning" variant="inline" data-risk>{{text}} <Link v-if="href||pageDocs" :href="href||pageDocs" external data-risk-more>{{translate(s.ctx.locale,'ux.learnMore')}}</Link></Callout>
</template>
