<script setup lang="ts">
// Tailscale: one "Sign in to Tailscale" button. An auth key and a Headscale
// server are folded away. The provider page and the setup wizard embed this
// same form. In the wizard (footer) the sign-in button is the wizard
// footer's main action.
import {computed,onUnmounted,ref,watch} from 'vue'
import {Button,Form,FormActions,FormItem,InfoTip,Input,SettingsSection,Space} from '@synopackageland/ui'
import {useConnectForm,type FormPrimary} from '../../connect/connectForm'
import {translate} from '../../i18n'
import {tsText} from '../../tailscaleMessages'
const props=defineProps<{footer?:boolean}>()
const emit=defineEmits<{submitted:[];connected:[]}>()
const {s,start}=useConnectForm('tailscale',{submitted:()=>emit('submitted'),connected:()=>emit('connected')})
const t=(key:string)=>translate(s.ctx.locale,key)
const x=(key:Parameters<typeof tsText>[1])=>tsText(s.ctx.locale,key)
const status=computed(()=>s.connectionFor('tailscale'))
const token=ref(''),loginServer=ref(''),loginOrigin=ref('')
watch(()=>status.value?.loginServer,value=>{loginServer.value=value??'';loginOrigin.value=status.value?.loginOrigin??''},{immediate:true})
// Pages stay mounted, so leaving the page clears the one-time key.
watch(()=>s.page.value,()=>{token.value=''})
onUnmounted(()=>{token.value=''})
function connect(){
 const params:Record<string,unknown>={provider:'tailscale'}
 if(token.value.trim())params.token=token.value.trim()
 if(loginServer.value.trim())params.loginServer=loginServer.value.trim()
 if(loginOrigin.value.trim())params.loginOrigin=loginOrigin.value.trim()
 token.value=''
 return start(params)
}
const primary=computed<FormPrimary|undefined>(()=>props.footer?{label:t('provider.login.tailscale'),loading:s.starting.value==='tailscale',disabled:s.busy.value,run:connect}:undefined)
defineExpose({primary})
</script>
<template>
 <Space direction="column" spacing="regular" data-connect-form="tailscale">
  <!-- In the wizard the button is the footer's; its help shows as a line. -->
  <p v-if="footer" data-ts-use>{{t('connect.tailscale.use')}}</p>
  <FormActions v-else>
   <slot name="actions" />
   <Button variant="primary" kind="tool" data-ts-connect :loading="s.starting.value==='tailscale'" :disabled="s.busy.value" @click="connect">{{t('provider.login.tailscale')}}</Button>
   <InfoTip icon="info" :content="t('connect.tailscale.use')" :aria-label="t('provider.login.tailscale')" data-ts-use />
  </FormActions>
  <SettingsSection :title="t('connect.advanced')" :default-open="false" data-connect-advanced>
   <Form direction="vertical">
    <FormItem :label="t('provider.key')" :help="t('ux.ts.keyHelp')"><Input v-model="token" type="password" autocomplete="off" :disabled="s.busy.value" data-ts-key /></FormItem>
    <FormItem :label="x('server')" :help="x('serverHelp')"><Input v-model="loginServer" :disabled="s.busy.value" data-login-server /></FormItem>
    <FormItem :label="x('origin')" :help="x('originHelp')"><Input v-model="loginOrigin" :disabled="s.busy.value" data-login-origin /></FormItem>
   </Form>
  </SettingsSection>
 </Space>
</template>
