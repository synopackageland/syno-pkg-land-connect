<script setup lang="ts">
// The DSM certificate review as a provider page's own callout; the Home
// lists the same review under Needs attention (connect/dsmReview.ts).
import {onMounted,onUnmounted} from 'vue'
import {Button,Callout} from '@synopackageland/ui'
import AppApproval from './AppApproval.vue'
import {useConnectState} from '../connect/state'
import {useDsmReview,type DsmReviewProvider} from '../connect/dsmReview'
const props=withDefaults(defineProps<{provider?:DsmReviewProvider;global?:boolean;attentionId?:string;force?:boolean}>(),{provider:'cloudflare'})
const s=useConnectState()
const {text,issue,action,needed,review,open,reading,approving,inspect,approve,scope,portChange}=useDsmReview(props.provider)
onMounted(()=>{if(props.global)s.dsmReviewAction.value=inspect})
onUnmounted(()=>{if(props.global&&s.dsmReviewAction.value===inspect)s.dsmReviewAction.value=undefined})
</script>
<template>
 <Callout v-if="needed||force" tone="warning" :title="text('title')" data-dsm-review :data-dsm-review-provider="provider" :data-attention-item="attentionId?'':undefined" :data-attention-id="attentionId"><span :data-dsm-review-error="issue?'':undefined">{{issue||text('line')}}</span><template #actions><Button variant="primary" size="sm" :loading="reading" :disabled="reading||approving" data-dsm-review-open @click="inspect">{{action}}</Button></template></Callout>
 <AppApproval v-if="review?.dsm" v-model:open="open" :locale="s.ctx.locale" :scope="scope" :items="[]" :dsm-only="review.dsm" :dsm-reason="portChange?text('line'):undefined" :busy="approving" @approve="approve" />
</template>
