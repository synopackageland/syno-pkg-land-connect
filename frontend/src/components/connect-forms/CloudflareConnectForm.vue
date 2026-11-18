<script setup lang="ts">
// The fields keep the label column; the consent, the button and the note
// start on the value column, as on the Free link's token form, so both
// Cloudflare tabs line up.
// My own domain: a saved own domain reconnects with one button (another name
// waits under one closed line); otherwise the name in front of the domain
// picked while signing in to Cloudflare. Both ask the one consent, since
// DSM's sign-in page becomes public. The Cloudflare page's Own domain tab and
// the setup wizard embed this same form. In the wizard (footer) its primary
// button is the wizard footer's main action.
import {nameConflict,type NameReuse} from '../../connect/nameConflict'
import {computed,ref,watch} from 'vue'
import {Button,Checkbox,FormActions,FormItem,FormNote,KeyValueRow,Link,Space} from '@synopackageland/ui'
import CloudflareDomain from '../CloudflareDomain.vue'
import ShowMore from '../ShowMore.vue'
import {useConnectForm,type FormPrimary} from '../../connect/connectForm'
import {CATALOG} from '../../connect/advisor'
import {translate} from '../../i18n'
const props=defineProps<{footer?:boolean}>()
const emit=defineEmits<{submitted:[];connected:[]}>()
const {s,start}=useConnectForm('cloudflare',{submitted:()=>emit('submitted'),connected:()=>emit('connected')})
const t=(key:string)=>translate(s.ctx.locale,key)
const c=(key:string)=>t('cloudflare.'+key)
const status=computed(()=>s.connectionFor('cloudflare'))
// A stopped own domain keeps its name; a token address is not an own domain.
const saved=computed(()=>!conflict.value&&status.value?.state!=='connected'&&status.value?.mode!=='token'?status.value?.hostname??'':'')
const confirmed=ref(false)
watch(()=>s.page.value,()=>{confirmed.value=false})
const registrar=CATALOG.options.find(o=>o.id==='cloudflare-domain')?.links?.registrar
const conflict=computed(()=>nameConflict(status.value))
// A taken name stays in the field until its alternative is chosen.
const typedName=computed(()=>(conflict.value?.conflictName??(status.value?.mode==='domain'?status.value.hostname:undefined)??status.value?.suggestedName)?.split('.')[0])
const starting=computed(()=>s.starting.value==='cloudflare')
function connect(params:Record<string,unknown>){
 if(s.busy.value)return Promise.resolve()
 confirmed.value=false
 return start({provider:'cloudflare',...params,confirmExposure:true},undefined,conflict.value?false:undefined)
}
const domain=(subdomain:string,reuseName?:NameReuse)=>connect({mode:'domain',subdomain,...(reuseName?{reuseName}:{})})
const form=ref<InstanceType<typeof CloudflareDomain>>()
// A saved name reconnects; otherwise the name typed in the form.
const primary=computed<FormPrimary|undefined>(()=>!props.footer?undefined:saved.value
 ?{label:c('reconnect'),loading:starting.value,disabled:s.busy.value||!confirmed.value,run:()=>connect({mode:'domain'})}
 :{label:c('domain'),loading:starting.value,disabled:s.busy.value||!confirmed.value||!form.value?.valid,run:async()=>form.value?.submit()})
defineExpose({primary})
</script>
<template>
 <Space direction="column" spacing="regular" data-connect-form="cloudflare">
  <Space v-if="saved" direction="column" spacing="regular" data-cf-saved>
   <KeyValueRow :label="t('ux.row.address')"><span data-cf-saved-host>{{saved}}</span></KeyValueRow>
   <FormItem label="" inset><Checkbox v-model="confirmed" :label="c('confirm')" :disabled="s.busy.value" data-cf-confirm /></FormItem>
   <FormActions v-if="!footer||$slots.actions" inset><slot name="actions" /><Button v-if="!footer" variant="primary" kind="tool" data-cf-reconnect :loading="starting" :disabled="s.busy.value||!confirmed" @click="connect({mode:'domain'})">{{c('reconnect')}}</Button></FormActions>
   <ShowMore :title="t('cfpage.domain.other')"><CloudflareDomain :quiet="footer" :conflict="conflict" :initial="typedName" :locale="s.ctx.locale" :disabled="s.busy.value" :locked="!confirmed" :loading="starting" @connect="domain" /></ShowMore>
  </Space>
  <template v-else>
   <CloudflareDomain ref="form" :footer="footer" :conflict="conflict" :initial="typedName" :locale="s.ctx.locale" :disabled="s.busy.value" :locked="!confirmed" :loading="starting" @connect="domain"><template #consent><Checkbox v-model="confirmed" :label="c('confirm')" :disabled="s.busy.value" data-cf-confirm /></template><slot name="actions" /></CloudflareDomain>
   <FormNote v-if="!conflict" inset data-cf-no-domain>{{c('noDomain')}} <Link :href="registrar" external>{{c('registrar')}}</Link></FormNote>
  </template>
 </Space>
</template>
