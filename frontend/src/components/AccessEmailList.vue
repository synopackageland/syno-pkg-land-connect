<script setup lang="ts">
import {computed,onUnmounted,ref,watch} from 'vue'
import {Trash2} from '@lucide/vue'
import {Button,Callout,CellText,Dialog,Form,FormActions,FormItem,IconButton,Input,InputGroup,KeyValueRow,Link,SettingsSection,Select,Space,Switch} from '@synopackageland/ui'
import {useConnectState} from '../connect/state'
import {relayTokenUrl} from '../connect/appUrls'
import {accessApi,normalizeEmails,defaultSessionDuration,sessionDurations,type AccessStatus} from '../connect/access'
import {translate} from '../i18n'
const s=useConnectState(),t=(key:string)=>translate(s.ctx.locale,'access.'+key)
const session=ref(defaultSessionDuration)
const currentSession=computed(()=>status.value?.sessionDuration||defaultSessionDuration)
const sessionDirty=computed(()=>session.value!==currentSession.value)
const sessionOptions=computed(()=>{const choices=sessionDurations.map(value=>({value,label:t('session.'+value)}));return sessionDurations.some(v=>v===session.value)?choices:[{value:session.value,label:t('sessionCustom')+': '+session.value},...choices]})
const status=ref<AccessStatus>(),emails=ref<string[]>([]),enabled=ref(false),entry=ref(''),token=ref(''),showToken=ref(false),busy=ref(false),error=ref(''),confirm=ref(false),loaded=ref(false)
let poll:ReturnType<typeof window.setTimeout>|undefined,alive=true
const message=(e:unknown)=>{const code=(e as {code?:string})?.code||(e instanceof Error?e.message:'');const key='code.'+code;return t(key)===('access.'+key)?t('code.access_unavailable'):t(key)}
const draft=computed(()=>enabled.value?emails.value:[])
const dirty=computed(()=>!!status.value&&(JSON.stringify(draft.value)!==JSON.stringify(status.value.emails)||sessionDirty.value))
const warning=computed(()=>enabled.value||!!status.value?.emails.length)
function reset(){session.value=currentSession.value;emails.value=[...(status.value?.emails??[])];enabled.value=!!emails.value.length;entry.value=''}
async function load(resetDraft=false){
 try{const next=await s.ctx.call<AccessStatus>(accessApi.get,{});if(!alive||!next||!Array.isArray(next.emails))return;const clean=!dirty.value,sessionClean=!sessionDirty.value;status.value=next;if(sessionClean)session.value=currentSession.value;if(!loaded.value||((resetDraft||clean)&&!next.working&&!next.code)){reset();loaded.value=true}
  if(next.working){window.clearTimeout(poll);poll=window.setTimeout(()=>void load(resetDraft),1000)}
 }catch(e){if(alive)error.value=message(e)}
}
watch([()=>s.page.value,()=>s.refreshes.value],([page])=>{if(page==='cloudflare')void load()},{immediate:true})
onUnmounted(()=>{alive=false;window.clearTimeout(poll)})
function add(){try{emails.value=normalizeEmails([...emails.value,entry.value]);entry.value='';error.value=''}catch(e){error.value=message(e)}}
function remove(i:number){emails.value.splice(i,1);if(!emails.value.length)enabled.value=false}
function review(){error.value='';if(enabled.value&&!emails.value.length){error.value=t('code.access_invalid_email');return}confirm.value=true}
async function apply(){
 busy.value=true;error.value=''
 try{status.value=await s.ctx.call<AccessStatus>(accessApi.set,{emails:normalizeEmails(draft.value),revision:status.value?.revision??0,confirm:true,...(sessionDirty.value?{sessionDuration:session.value}:{})});confirm.value=false;if(status.value.working)void load(true);else reset()}
 catch(e){error.value=message(e);confirm.value=false}
 finally{busy.value=false}
}
async function sync(){const clean=!dirty.value,sessionClean=!sessionDirty.value;busy.value=true;error.value='';try{status.value=await s.ctx.call<AccessStatus>(accessApi.sync,{});if(sessionClean)session.value=currentSession.value;if(status.value.working)void load(clean);else if(clean)reset()}catch(e){error.value=message(e)}finally{busy.value=false}}
async function saveToken(){const value=token.value.trim();token.value='';busy.value=true;error.value='';try{status.value=await s.ctx.call<AccessStatus>(accessApi.token,{token:value});showToken.value=false}catch(e){error.value=message(e)}finally{busy.value=false}}
const code=computed(()=>error.value||(status.value?.code?t('code.'+status.value.code):''))
// The page pins Reset and Apply for the list in its footer.
const working=computed(()=>!!status.value?.working)
defineExpose({dirty,busy,working,reset,review})
</script>
<template>
 <SettingsSection :title="t('title')" :description="t('scope')" data-access-section>
  <Form @submit.prevent="review">
   <FormItem :label="t('enabled')" :description="t('empty')">
    <Switch v-model="enabled" :disabled="busy||!loaded||status?.working" data-access-toggle />
    <Callout v-if="warning" tone="warning" variant="inline" data-access-native-warning>{{t('native')}}</Callout>
   </FormItem>
   <FormItem v-if="enabled" :label="t('emails')" :help="code||undefined" :validate-status="code?'error':undefined">
    <Space direction="column" spacing="dense">
     <Space v-for="(email,i) in emails" :key="email" align-items="center" spacing="dense"><CellText :text="email" wrap /><IconButton :label="t('remove')" :disabled="busy" @click="remove(i)"><Trash2 :size="16" /></IconButton></Space>
     <InputGroup><Input v-model="entry" type="email" :aria-label="t('emails')" :disabled="busy" data-access-email @keydown.enter.prevent="add" /><Button :disabled="busy||!entry.trim()" data-access-add @click="add">{{t('add')}}</Button></InputGroup>
    </Space>
   </FormItem>
   <FormItem :label="t('session')" :description="t('sessionHint')">
    <Select v-model="session" :options="sessionOptions" :disabled="busy||!loaded||status?.working" data-access-session />
   </FormItem>
  </Form>
  <Space direction="column" spacing="regular">
   <p v-if="code" role="status" data-access-error>{{code}}</p>
   <KeyValueRow :label="t('status')"><Space spacing="dense" wrap><span>{{t('state.'+(status?.working?'pending':status?.state??'pending'))}}</span><Button :disabled="busy||status?.working" data-access-sync @click="sync">{{t('sync')}}</Button></Space></KeyValueRow>
   <KeyValueRow v-for="endpoint in status?.endpoints" :key="endpoint.host" :label="endpoint.host">{{t(endpoint.protected?'protected':'pending')}}</KeyValueRow>
   <KeyValueRow v-if="status?.checked" :label="t('checked')">{{new Date(status.checked*1000).toLocaleString(s.ctx.locale)}}</KeyValueRow>
   <p>{{t('unsupported')}}</p>
   <Space spacing="dense" wrap><Link href="https://one.dash.cloudflare.com/" external>{{t('onboarding')}}</Link><span>{{t('payment')}}</span></Space>
   <Space spacing="dense" wrap><Link :href="relayTokenUrl()" external>{{t('createToken')}}</Link><Button @click="showToken=!showToken">{{t('replaceToken')}}</Button></Space>
   <Form v-if="showToken" @submit.prevent="saveToken"><FormItem :label="t('token')" :description="t('permissions')"><Input v-model="token" type="password" autocomplete="off" :disabled="busy" data-access-token /></FormItem><FormActions inset><Button :disabled="busy" @click="showToken=false">{{t('cancel')}}</Button><Button :disabled="busy||!token.trim()" :loading="busy" @click="saveToken">{{t('saveToken')}}</Button></FormActions></Form>
  </Space>
 </SettingsSection>
 <Dialog v-model:open="confirm" :title="t('confirm')" :description="t('interrupt')" size="md" :close-on-outside="false" data-access-confirm>
  <Space direction="column" spacing="regular"><p>{{t(draft.length?'confirmOn':'confirmOff')}}</p><Callout v-if="draft.length" tone="warning" variant="inline" :dismissible="false" data-access-confirm-native>{{t('native')}}</Callout></Space>
  <template #footer><Button :disabled="busy" @click="confirm=false">{{t('cancel')}}</Button><Button variant="primary" :loading="busy" :disabled="busy" data-access-apply @click="apply">{{t('apply')}}</Button></template>
 </Dialog>
</template>
