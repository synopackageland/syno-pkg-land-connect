<script setup lang="ts">
// The Cloudflare sign-in behind own-domain App addresses (apps.list's
// public field): which domain it is for, Reconnect once it stops working,
// and Disconnect, which deletes only the certificate on the NAS.
import CloudflareOffDialog from './CloudflareOffDialog.vue'
import {computed,ref} from 'vue'
import {Button,Callout,KeyValueRow,Space,Tag} from '@synopackageland/ui'
import {useConnectState} from '../connect/state'
import {appsApi,tunnelHealth,type AppsView} from '../connect/appUrls'
import {trustedLoginUrl} from '../connect/login'
import {translate} from '../i18n'
defineProps<{account?:AppsView['public'];inUse?:boolean}>()
const emit=defineEmits<{changed:[]}>()
const s=useConnectState()
const reconnectError=ref('')
const t=(key:string)=>translate(s.ctx.locale,'apps.'+key)
// The tag reports whether the tunnel serves, not only that Cloudflare is authorized.
const health=computed(()=>tunnelHealth(s.connectionFor('cloudflare')))
const chip=computed(()=>({serving:{key:'cf.connected',tone:'success'},connecting:{key:'cf.connecting',tone:'warning'},stopped:{key:'cf.notServing',tone:'danger'}} as const)[health.value])
const reconnecting=ref(false)
async function reconnect(){
 reconnecting.value=true;reconnectError.value=''
 try{const result=await s.ctx.call<{loginUrl?:string}>(appsApi.cloudflareConnect,{});const login=trustedLoginUrl('cloudflare',result.loginUrl);if(login)window.open(login,'_blank','noopener,noreferrer')}
 catch(e){reconnectError.value=s.message(e)}
 finally{reconnecting.value=false;emit('changed')}
}
const open=ref(false),disconnecting=ref(false)
</script>
<template>
 <Space v-if="account?.connected||account?.invalid" direction="column" spacing="regular" data-cf-account>
  <Callout v-if="account.invalid&&inUse" tone="warning" :title="t('cf.invalid')" code="cloudflare_cert_invalid" data-cf-invalid>{{t('cf.invalidLine')}}<template #actions><Button :loading="reconnecting" :disabled="reconnecting" data-cf-reconnect @click="reconnect">{{t('cf.reconnect')}}</Button></template></Callout>
  <KeyValueRow v-else-if="account.invalid" :label="translate(s.ctx.locale,'cfpage.account.title')"><Button :loading="reconnecting" data-cf-reconnect @click="reconnect">{{t('cf.reconnect')}}</Button></KeyValueRow>
  <KeyValueRow v-else :label="translate(s.ctx.locale,'cfpage.account.title')">
   <Space align-items="center" spacing="dense" wrap><Tag :label="t(chip.key).replace('{zone}',account.zone??'')" :appearance="chip.tone" data-cf-zone :data-cf-health="health" /><Button variant="secondary" :disabled="disconnecting" data-cf-disconnect @click="open=true">{{t('cf.disconnect')}}</Button></Space>
  </KeyValueRow>
  <Callout v-if="reconnectError" tone="danger" data-cf-account-error>{{reconnectError}}</Callout>
  <CloudflareOffDialog v-model:open="open" authorization @changed="emit('changed')" />
 </Space>
</template>
