<script setup lang="ts">
// The own-domain start: only the name in front of the domain the user picks
// while signing in to Cloudflare. The default slot adds actions before its button
// and the consent slot sits between the name and the button.
// locked holds only the button, so the name can be typed before consent.
// footer hides the button: the host draws it (submit, valid); quiet makes
// it a plain button, for a form folded under another primary action.
// On a wide page the name sits in the label column of the status rows above
// it; a name conflict, the consent and the button start on its value column.
import {computed,ref,watch} from 'vue'
import {Button,Form,FormActions,FormItem,Input} from '@synopackageland/ui'
import NameConflictRow from './NameConflictRow.vue'
import type {NameConflict,NameReuse} from '../connect/nameConflict'
import {translate} from '../i18n'
const props=defineProps<{locale:string;disabled:boolean;locked?:boolean;loading?:boolean;initial?:string;error?:string;conflict?:NameConflict;footer?:boolean;quiet?:boolean}>()
const emit=defineEmits<{connect:[subdomain:string,reuseName?:NameReuse]}>()
const t=(key:string)=>translate(props.locale,'cloudflare.'+key)
const name=ref(props.initial||'')
watch(()=>props.initial,(next,old)=>{if(!name.value||name.value===old)name.value=next??''})
// One DNS label, as the daemon checks it.
const label=computed(()=>name.value.trim().toLowerCase())
const valid=computed(()=>/^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$/.test(label.value))
function submit(){if(valid.value&&!props.disabled&&!props.locked)emit('connect',label.value)}
defineExpose({valid,submit})
</script>
<template>
 <Form class="cf-domain" @submit="submit">
  <FormItem :label="t('subdomain')" :description="t('subdomainShort')" :help="error" :validate-status="error?'error':undefined"><Input v-model="name" :disabled="disabled" autocomplete="off" placeholder="nas" /></FormItem>
  <FormItem v-if="conflict" label="" inset><NameConflictRow :locale="locale" :conflict="conflict" :disabled="disabled" @reuse="emit('connect',conflict.conflictName!.split('.')[0]!,{name:conflict.conflictName!,record:conflict.reuseRecord!})" @use="value=>{name=value.split('.')[0]!;emit('connect',name)}" @check="emit('connect',conflict.conflictName!.split('.')[0]!)" /></FormItem>
  <FormItem v-if="$slots.consent" label="" inset><slot name="consent" /></FormItem>
  <FormActions v-if="!footer||$slots.default" inset><slot /><Button v-if="!footer" :variant="quiet?'secondary':'primary'" kind="tool" data-cf-domain :loading="loading" :disabled="disabled||locked||!valid" @click="emit('connect',label)">{{t('domain')}}</Button></FormActions>
 </Form>
</template>
