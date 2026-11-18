<script setup lang="ts">
// NetBird: one single sign-on button. A setup key, a self-hosted management
// server and login origins are folded away; a key for a self-hosted server
// asks once before the server's policy merges. The provider page and the
// setup wizard embed this same form. In the wizard (footer) the sign-in
// button is the wizard footer's main action.
import {computed,onUnmounted,ref,watch} from 'vue'
import {Button,Dialog,Form,FormActions,FormItem,InfoTip,Input,SettingsSection,Space} from '@synopackageland/ui'
import {useConnectForm,type FormPrimary} from '../../connect/connectForm'
import {translate} from '../../i18n'
import {netbirdText} from '../../netbirdMessages'
const props=defineProps<{footer?:boolean}>()
const emit=defineEmits<{submitted:[];connected:[]}>()
const {s,start}=useConnectForm('netbird',{submitted:()=>emit('submitted'),connected:()=>emit('connected')})
const t=(key:string)=>translate(s.ctx.locale,'netbird.'+key)
const n=(key:string)=>netbirdText(s.ctx.locale,key)
const management=ref(''),key=ref(''),origins=ref(''),confirm=ref(false)
const status=computed(()=>s.connectionFor('netbird'))
const valid=computed(()=>{
 if(!management.value)return true
 try{const u=new URL(management.value);return u.protocol==='https:'&&!u.username&&!u.password&&!u.search&&!u.hash&&u.pathname==='/'&&!/[\s\\%]/.test(management.value)}catch{return false}
})
watch(()=>status.value?.managementUrl,value=>{if(value&&value!=='https://api.netbird.io:443')management.value=value},{immediate:true})
watch(()=>status.value?.loginOrigins,value=>{origins.value=value?.join(' ')??''},{immediate:true})
watch(()=>s.page.value,()=>{key.value='';confirm.value=false})
onUnmounted(()=>{key.value=''})
function begin(){if(key.value.trim()&&management.value.trim()&&!['https://api.netbird.io','https://api.netbird.io:443'].includes(management.value.trim())){confirm.value=true;return Promise.resolve()}return connect()}
async function connect(){
 if(s.busy.value||!valid.value)return
 const params:Record<string,unknown>={provider:'netbird'}
 if(management.value.trim())params.managementUrl=management.value.trim()
 if(key.value.trim())params.token=key.value.trim()
 if(origins.value.trim())params.loginOrigins=origins.value.trim().split(/\s+/)
 if(confirm.value)params.confirmManagementPolicy=true
 key.value='';confirm.value=false
 await start(params)
}
const primary=computed<FormPrimary|undefined>(()=>props.footer?{label:n(key.value.trim()?'keyConnect':'sso'),loading:s.starting.value==='netbird',disabled:s.busy.value||!valid.value,run:begin}:undefined)
defineExpose({primary})
</script>
<template>
 <Space direction="column" spacing="regular" data-connect-form="netbird">
  <!-- In the wizard the button is the footer's; its help shows as a line. -->
  <p v-if="footer" data-nb-help>{{n('ssoHelp')}}</p>
  <FormActions v-else>
   <slot name="actions" />
   <Button variant="primary" kind="tool" data-nb-connect :loading="s.starting.value==='netbird'" :disabled="s.busy.value||!valid" @click="begin">{{n(key.trim()?'keyConnect':'sso')}}</Button>
   <InfoTip icon="info" :content="n('ssoHelp')" :aria-label="n('sso')" data-nb-help />
  </FormActions>
  <SettingsSection :title="translate(s.ctx.locale,'connect.advanced')" :default-open="false" data-connect-advanced>
   <Form direction="vertical">
    <FormItem :label="t('management')" :help="t('managementHelp')"><Input v-model="management" placeholder="https://api.netbird.io:443" :disabled="s.busy.value" autocomplete="off" data-nb-management /></FormItem>
    <FormItem :label="t('key')" :help="t('keyHelp')"><Input v-model="key" type="password" autocomplete="off" :disabled="s.busy.value" data-nb-key /></FormItem>
    <FormItem :label="n('origins')" :help="t('originsHelp')"><Input v-model="origins" :disabled="s.busy.value" autocomplete="off" data-nb-origins /></FormItem>
   </Form>
  </SettingsSection>
  <Dialog v-model:open="confirm" :title="n('mergeTitle')" :description="n('mergeHelp')"><p>{{management}}</p><template #footer><Button @click="confirm=false">{{n('cancel')}}</Button><Button variant="primary" data-nb-confirm @click="connect">{{n('keyConnect')}}</Button></template></Dialog>
 </Space>
</template>
