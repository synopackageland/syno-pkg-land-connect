<script setup lang="ts">
// The Cloudflare token for the NetBird server's Signal Worker, pasted on the
// Self-host server tab. It is saved where the Free link keeps its token, so
// turning the Free link on later reuses it, but saving it here never
// publishes DSM: that stays the Free link's own Add DSM. The field clears at
// once and the token never comes back to the page. Missing permissions use
// a new token from the shared create-token link.
import {ref} from 'vue'
import {Button,Form,FormActions,FormItem,Input,Link,Space} from '@synopackageland/ui'
import {useConnectState} from '../connect/state'
import {appsApi,relayTokenUrl} from '../connect/appUrls'
import {translate} from '../i18n'
const props=defineProps<{edit?:boolean}>()
const emit=defineEmits<{saved:[],check:[]}>()
const s=useConnectState()
const t=(key:string)=>translate(s.ctx.locale,key)
const token=ref(''),checking=ref(false),error=ref('')

function errorText(e:unknown):string{
 const code=(e as {code?:string})?.code??''
 const key='cfpage.code.'+code
 return code.startsWith('workers_')&&t(key)!==key?t(key):s.message(e)
}
async function save(){
 const value=token.value.trim()
 token.value=''
 if(!value)return
 checking.value=true;error.value=''
 try{await s.ctx.call(appsApi.relayConnect,{token:value,noDsm:true});emit('saved')}
 catch(e){error.value=errorText(e)}
 finally{checking.value=false}
}
</script>
<template>
 <Form data-nbserver-token>
  <FormItem :label="t('apps.relay.token')" :help="error||undefined" :validate-status="error?'error':undefined" data-nbserver-token-item>
   <Input v-model="token" type="password" autocomplete="off" :disabled="checking" data-nbserver-token-input @keydown.enter.prevent="save" />
   <template #description><Space align-items="center" spacing="dense" wrap><Link :href="relayTokenUrl()" external data-nbserver-token-link>{{t('apps.relay.step.tokenLink')}}</Link><span>{{t('nbserver.token.help')}}</span></Space></template>
  </FormItem>
  <FormActions inset>
   <Button variant="primary" :loading="checking" :disabled="checking||!token.trim()" data-nbserver-token-save @click="save">{{t('nbserver.token.save')}}</Button>
   <template v-if="props.edit"><Link :href="relayTokenUrl()" external data-nbserver-token-edit>{{t('access.createToken')}}</Link></template>
   <Button :disabled="checking" data-nbserver-token-check @click="emit('check')">{{t('apps.retry')}}</Button>
  </FormActions>
 </Form>
</template>
