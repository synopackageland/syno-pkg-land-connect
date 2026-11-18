<script setup lang="ts">
// Generic error surfaces preserve the complete inline DSM recovery, including
// inside an existing dialog. Other errors retain their original retry actions.
import {computed,ref,watch} from 'vue'
import {Button,Callout} from '@synopackageland/ui'
import DsmReview from './DsmReview.vue'
import GitHubTokenAction from './GitHubTokenAction.vue'
import {useConnectState} from '../connect/state'
import type {DsmReviewProvider} from '../connect/dsmReview'
import {dsmNeedsReview} from '../connect/appUrls'
import {translate} from '../i18n'
defineOptions({inheritAttrs:false})
const props=defineProps<{message?:string;scope?:string;title?:string;code?:string}>()
const s=useConnectState()
const dismissed=ref(false)
watch(()=>props.message,()=>{dismissed.value=false})
const github=computed(()=>props.message?s.githubAction(props.message):undefined)
const dependency=computed(()=>props.code==='netbird_cloudflare_in_use'||props.message===translate(s.ctx.locale,'dependency.stopOrder')||props.message===translate(s.ctx.locale,'dependency.changeBlocked'))
const dsm=computed(()=>props.code==='pin_proxy_required'||props.message===translate(s.ctx.locale,'apps.code.pin_proxy_required'))
const provider=computed<DsmReviewProvider>(()=>props.scope==='private'?'tailscale':props.scope?'cloudflare':(s.dsmErrorProvider.value||'cloudflare') as DsmReviewProvider)
function openServer(){s.wizardOpen.value=false;s.go('netbird',{tab:'server'})}
watch(s.refreshes,()=>{
 if(dsm.value&&!s.dsmErrorProvider.value&&!dsmNeedsReview(s.connectionFor(provider.value)))dismissed.value=true
})
</script>
<template>
 <template v-if="message&&!dismissed">
  <DsmReview v-if="dsm" :key="provider" :provider="provider" force />
  <Callout v-else-if="dependency" tone="danger" :title="title" data-dependency-refusal v-bind="$attrs"><template v-if="message===translate(s.ctx.locale,'dependency.changeBlocked')">{{message}}</template><template v-else>{{translate(s.ctx.locale,'dependency.stopLine')}} {{translate(s.ctx.locale,'dependency.stopOrder')}}</template><template #actions><Button @click="openServer">{{translate(s.ctx.locale,'dependency.action.open_server')}}</Button></template></Callout>
  <Callout v-else tone="danger" :title="title" v-bind="$attrs">{{message}}<template v-if="$slots.actions||github" #actions><slot name="actions" /><GitHubTokenAction v-if="github" :locale="s.ctx.locale" :code="github" @open="s.openGitHubToken()" /></template></Callout>
 </template>
</template>
