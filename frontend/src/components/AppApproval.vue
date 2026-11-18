<script setup lang="ts">
// The approval for one or more Apps in a dialog (ApprovalDetails holds what
// it shows). dsmOnly shows DSM alone, for DSM-only remote access, with the
// same certificate rows.
import type {NameConflict} from '../connect/nameConflict'
import {computed} from 'vue'
import {Button,Dialog} from '@synopackageland/ui'
import {type Inspection,type PinView,type Scope} from '../connect/appUrls'
import {translate} from '../i18n'
import ApprovalDetails from './ApprovalDetails.vue'
const props=defineProps<{locale:string;scope:Scope;items:Inspection[];reenable?:boolean;busy?:boolean;hostnames?:Record<string,string>;error?:string;conflict?:NameConflict;checking?:boolean;dsmReason?:string;dsmOnly?:{hostname?:string;port:number;pin:PinView}}>()
const open=defineModel<boolean>('open',{default:false})
const emit=defineEmits<{approve:[];recover:[action:'reuse'|'use'|'check',name?:string];hostname:[pkg:string,value:string]}>()
const t=(key:string)=>translate(props.locale,'apps.'+key)
const pub=computed(()=>props.scope!=='private')
const title=computed(()=>t(props.dsmOnly?'approve.dsmOnly':props.reenable?'approve.reenable':pub.value?'approve.public':'approve.private'))
const action=computed(()=>t(props.dsmOnly?'approve.dsmOnlyAction':props.reenable?'approve.reenableAction':pub.value?'approve.publicAction':'approve.privateAction'))
</script>
<template>
 <Dialog v-model:open="open" :title="title" size="lg" data-apps-approval :data-scope="scope" :data-dsm-only="dsmOnly?'':undefined">
  <ApprovalDetails :locale="locale" :scope="scope" :items="items" :reenable="reenable" :busy="busy" :hostnames="hostnames" :error="error" :conflict="conflict" @recover="(action,name)=>emit('recover',action,name)" :dsm-only="dsmOnly" :dsm-reason="dsmReason" @hostname="(pkg,value)=>emit('hostname',pkg,value)" />
  <template #footer>
   <Button :disabled="busy" @click="open=false">{{t('cancel')}}</Button>
   <Button variant="primary" :loading="busy" :disabled="busy||checking" data-approve-confirm @click="emit('approve')">{{action}}</Button>
  </template>
 </Dialog>
</template>
