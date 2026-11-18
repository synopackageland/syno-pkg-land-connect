<script setup lang="ts">
import ConnectionError from '../ConnectionError.vue'

// Step 2: connect the way chosen, with the same form its own page shows
// while it is off (the Free link's from the Cloudflare page, the others'
// from connect-forms). The forms share Connect's state, so a sign-in in
// another tab finishes here too: when the way comes on, SetupWizard moves to
// the next step itself. The way's path is the step's hero and Back is in the
// wizard footer. The temporary address has no form: the step is its confirm
// (TemporaryFacts), and the wizard footer's Start is the one action. Once a
// way other than the Free link runs, its connection check shows here, as
// setting up until it passes; SetupWizard moves on when it does. Each form's
// primary button is the wizard footer's main action (primary, exposed here).
import {computed,ref} from 'vue'
import {Callout,Space} from '@synopackageland/ui'
import {useConnectState} from '../../connect/state'
import {FORM,providerOf,type WayState,type WizardWay} from '../../connect/wizard'
import TemporaryFacts from '../TemporaryFacts.vue'
import type {RelayStatus} from '../../connect/topology'
import {translate} from '../../i18n'
import {CONNECT_FORMS} from '../connect-forms'
import FreeLink from '../FreeLink.vue'
import DsmReview from '../DsmReview.vue'
import ConnectionCheck from '../ConnectionCheck.vue'
import type {CheckResult} from '../../connect/checks'
import type {FormPrimary} from '../../connect/connectForm'
const props=defineProps<{way:WizardWay;state:WayState;relay?:RelayStatus}>()
const emit=defineEmits<{relay:[];check:[CheckResult]}>()
const s=useConnectState()
const t=(key:string)=>translate(s.ctx.locale,'wizard.'+key)
const form=ref<{primary?:FormPrimary}>()
// A start can be refused immediately or fail later while status is polled.
// Keep the reason inside the modal, beside the action that requested it.
const failure=computed(()=>{
 if(s.error.value)return s.error.value
 if(props.way==='workers')return ''
 const current=s.connectionFor(providerOf(props.way))
 return current?.state==='failed'&&current.code?s.message({code:current.code}):''
})
// A way that runs has no form action left: the wizard moves on, or offers Continue anyway.
const primary=computed<FormPrimary|undefined>(()=>props.way==='temporary'||props.state==='on'&&props.way!=='workers'?undefined:form.value?.primary)
defineExpose({primary})
</script>
<template>
 <Space direction="column" spacing="regular" data-wizard-connect :data-wizard-connect-way="way">
  <ConnectionCheck v-if="state==='on'&&way!=='workers'&&way!=='ipv6'" :key="way" :provider="providerOf(way)" @result="emit('check',$event)" />
  <template v-if="way==='temporary'">
   <TemporaryFacts :locale="s.ctx.locale" />
   <Callout v-if="state==='started'" tone="info" data-wizard-waiting>{{t('temporary.starting')}}</Callout>
   <ConnectionError v-else-if="s.error.value" :message="s.error.value" :title="translate(s.ctx.locale,'provider.attention')" data-wizard-error />
  </template>
  <template v-else>
   <ConnectionError v-if="failure" :message="failure" :title="translate(s.ctx.locale,'provider.attention')" data-wizard-error />
   <DsmReview :key="way" v-if="way==='cloudflare-domain'||way==='tailscale'" :provider="way==='tailscale'?'tailscale':'cloudflare'" />
   <Callout v-if="state==='started'" tone="info" data-wizard-waiting>{{t('connect.waiting')}}</Callout>
   <FreeLink v-if="way==='workers'" ref="form" footer :status="relay" @changed="emit('relay')" />
   <component :is="CONNECT_FORMS[FORM[way]]" v-else ref="form" footer />
  </template>
 </Space>
</template>
