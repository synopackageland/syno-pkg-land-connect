<script setup lang="ts">
// The Self-host server tab: a NetBird server on this NAS, reached through the
// user's own domain on Cloudflare. Without Cloudflare on an own domain the tab
// says what is missing and opens the Cloudflare page. One button runs the
// whole setup in connectd; the page only follows its steps, so leaving or
// reloading never stops it, and a failed step names the next thing to do.
// Running, the tab shows the server's own facts and turns it off, keeping or
// deleting its data; a Cloudflare name it could not remove stays named here.
// The owner's password comes from connectd only when asked for.
import {computed,onUnmounted,ref,watch} from 'vue'
import {Button,Callout,Checkbox,CopyCutField,Dialog,formatDuration,formatFileSize,FormNote,KeyValueRow,Link,LoadingIndicator,Space,StepList,useToast} from '@synopackageland/ui'
import {useConnectState} from '../connect/state'
import {CLOUDFLARE_CODES,GRPC_BLOCKED,codeKey,grpcSettingsUrl,pollDelay,serverApi,stepItems,validStatus,waitingForGRPC,type ServerStatus} from '../connect/netbirdServer'
import {useServerStatus} from '../connect/serverStatus'
import {dependencyText} from '../connect/dependency'
import {translate,uiLocale} from '../i18n'
import NetBirdServerToken from './NetBirdServerToken.vue'
import CloudResourceGuide from './CloudResourceGuide.vue'
import SupervisionState from './SupervisionState.vue'
const props=defineProps<{active:boolean}>()
const s=useConnectState()
const toast=useToast()
const t=(key:string)=>translate(s.ctx.locale,key)
const n=(key:string)=>t('nbserver.'+key)
const shared=useServerStatus()
const status=shared?.status??ref<ServerStatus>(),loaded=shared?.loaded??ref(false),sending=ref(false)
const actionError=ref<{code?:string;text:string}>()
const password=ref(''),offOpen=ref(false),removeData=ref(false),turningOff=ref(false)

let timer=0,disposed=false
function stop(){window.clearTimeout(timer);timer=0}
async function load(){
 if(shared){await shared.load();return}
 stop()
 try{const r=await s.ctx.call<ServerStatus>(serverApi.status,{});if(validStatus(r))status.value=r}catch{/* the tab keeps what it showed */}
 finally{loaded.value=true;if(!disposed&&props.active)timer=window.setTimeout(()=>void load(),pollDelay(status.value?.state,status.value?.dependencies?.checking))}
}
watch([()=>props.active,()=>s.refreshes.value],([active])=>{if(active)void load();else stop()},{immediate:true})
// Leaving the tab forgets the password it showed.
watch(()=>props.active,active=>{if(!active)password.value=''})
onUnmounted(()=>{disposed=true;stop();password.value=''})

const state=computed(()=>status.value?.state)
const missing=computed(()=>status.value?.prerequisite??'')
const steps=computed(()=>stepItems(status.value?.steps,t))
const failedConflict=computed(()=>status.value?.steps?.find(step=>step.state==='failed'&&step.conflict)?.conflict)
const guiding=computed(()=>waitingForGRPC(status.value?.steps))
function openGRPC(){window.open(grpcSettingsUrl(status.value?.zone??''),'_blank','noopener')}
const working=computed(()=>state.value==='working')
const failure=computed(()=>state.value==='failed'?status.value?.code??status.value?.steps?.find(step=>step.state==='failed')?.code:undefined)
const peers=computed(()=>status.value?.peers===undefined?'':n('row.peersValue').replace('{online}',String(status.value.peersOnline??0)).replace('{total}',String(status.value.peers)))
const uptime=computed(()=>status.value?.uptimeSeconds?formatDuration(status.value.uptimeSeconds,{locale:uiLocale(s.ctx.locale)}):'')
const size=(bytes?:number)=>bytes?formatFileSize(bytes):''

function codeText(code?:string){return t(codeKey(code))}
async function send(action:'setup'|'disable',params:Record<string,unknown>={}){
 sending.value=true;actionError.value=undefined
 try{const r=await s.ctx.call<ServerStatus>(serverApi[action],params);if(validStatus(r))status.value=r;return true}
 catch(e){const code=(e as {code?:string})?.code;actionError.value={code,text:code==='netbird_cloudflare_in_use'?t('dependency.stopOrder'):dependencyText(code??'unavailable',t)};return false}
 finally{sending.value=false;void load()}
}
const setup=()=>send('setup')
const anotherName=()=>send('setup',{useAnotherName:true})
// The token the Signal Worker needs: missing, or refused while setting up.
const TOKEN_CODES=new Set(['netbird_server_workers_required','workers_not_connected','workers_routes_permission','workers_permission_denied','workers_zone_account','workers_token_invalid'])
// Missing permission uses the shared new-token/replacement form.
const PERMISSION='workers_routes_permission'
async function tokenSaved(){
 toast.success(n('token.saved'))
 if(state.value==='failed')await setup();else await load()
}
async function turnOff(){
 turningOff.value=true
 if(await send('disable',{removeData:removeData.value})){offOpen.value=false;removeData.value=false;password.value=''}
 turningOff.value=false
}
async function showPassword(){
 try{password.value=(await s.ctx.call<{password?:string}>(serverApi.credentials,{}))?.password??''}
 catch(e){actionError.value={code:(e as {code?:string})?.code,text:codeText((e as {code?:string})?.code)}}
}
</script>
<template>
 <Space direction="column" spacing="regular" data-nbserver>
  <LoadingIndicator v-if="!loaded" :text="n('loading')" data-nbserver-loading />
  <template v-else-if="!status">
   <Callout tone="warning" :title="n('unavailable')" data-nbserver-unavailable><template #actions><Button @click="load">{{t('ux.refresh')}}</Button></template></Callout>
  </template>
  <template v-else>
   <SupervisionState v-if="status.supervision" :snapshot="status.supervision" :locale="s.ctx.locale" :busy="sending" @retry="setup" @alternative="anotherName" />
   <Callout v-if="actionError&&!offOpen" tone="danger" :code="actionError.code" data-nbserver-action-error>{{actionError.text}}</Callout>
   <!-- A Cloudflare name left behind by turning off: Cloudflare still points it at this NAS. -->
   <Callout v-if="status.cleanupHostname" tone="warning" :title="n('cleanup').replace('{host}',status.cleanupHostname)" data-nbserver-cleanup>
    <template #actions><Button :loading="sending" :disabled="sending" data-nbserver-cleanup-retry @click="send('disable')">{{n('retryCleanup')}}</Button></template>
   </Callout>
   <template v-if="state==='off'">
    <!-- What is missing is a closable note; what fixes it stays on the page. -->
    <template v-if="missing">
     <Callout tone="info" :title="n('prereq.title')" :dismiss-key="missing" data-nbserver-prereq>{{codeText(missing)}}</Callout>
     <NetBirdServerToken v-if="TOKEN_CODES.has(missing)" :edit="missing===PERMISSION||missing==='workers_permission_denied'" data-nbserver-prereq-form @saved="tokenSaved" @check="load" />
     <Space v-else direction="column" spacing="dense" data-nbserver-prereq-form>
      <FormNote>{{n('prereq.why')}}</FormNote>
      <Space><Button variant="primary" data-nbserver-cloudflare @click="s.go('cloudflare')">{{n('prereq.action')}}</Button></Space>
     </Space>
    </template>
    <template v-else>
     <KeyValueRow v-if="status.zone" :label="n('row.domain')" :value="status.zone" />
     <FormNote>{{n('facts')}}</FormNote>
     <Space><Button variant="primary" :loading="sending" :disabled="sending" data-nbserver-setup @click="setup">{{n('setup')}}</Button></Space>
    </template>
   </template>
   <template v-if="working||state==='failed'">
    <StepList :items="steps" data-nbserver-steps>
     <template #description="{item,index}">
      <CloudResourceGuide v-if="status.steps?.[index]?.conflict" :conflict="status.steps[index]!.conflict!" :locale="s.ctx.locale" :busy="sending" @retry="setup" @alternative="anotherName" />
      <span v-else>{{item.description}}</span>
     </template>
    </StepList>
    <template v-if="failure">
     <Callout v-if="!failedConflict" tone="warning" :title="n('failed')" :dismiss-key="failure" data-nbserver-failure>{{codeText(failure)}}</Callout>
     <Space direction="column" spacing="regular" data-nbserver-failure-form>
      <NetBirdServerToken v-if="TOKEN_CODES.has(failure)" :edit="failure===PERMISSION||failure==='workers_permission_denied'" @saved="tokenSaved" @check="setup" />
      <Space wrap>
       <Button v-if="CLOUDFLARE_CODES.has(failure)&&!TOKEN_CODES.has(failure)" data-nbserver-cloudflare @click="s.go('cloudflare')">{{n('prereq.action')}}</Button>
       <Button v-if="failure===GRPC_BLOCKED&&status.zone" data-nbserver-grpc @click="openGRPC">{{n('guide.action')}}</Button>
       <Button v-if="!failedConflict" variant="primary" :loading="sending" :disabled="sending" data-nbserver-continue @click="setup">{{t('apps.retry')}}</Button>
       <Button variant="secondary" :disabled="sending" data-nbserver-off @click="offOpen=true">{{n('offAction')}}</Button>
      </Space>
     </Space>
    </template>
    <!-- Cloudflare's gRPC setting has no API: one switch for the user, then setup goes on by itself. -->
    <template v-else-if="guiding">
     <Callout tone="info" :title="n('guide.title')" dismiss-key="grpc" data-nbserver-guide>{{n('guide.text')}}</Callout>
     <Space v-if="status.zone" data-nbserver-guide-form><Button variant="primary" data-nbserver-grpc @click="openGRPC">{{n('guide.action')}}</Button></Space>
    </template>
    <Space v-else direction="column" spacing="regular"><span role="status" data-nbserver-working>{{status.job?.step?n('progress').replace('{step}',n('step.'+status.job.step)):n('workingHelp')}}</span><Button :disabled="sending" data-nbserver-off @click="offOpen=true">{{n('offAction')}}</Button></Space>
   </template>
   <Space v-if="status.resumeRequired&&state!=='failed'" direction="column" spacing="regular" data-nbserver-resume><Callout tone="warning" :title="n('code.netbird_server_setup_incomplete')" /><Button :loading="sending" :disabled="sending" data-nbserver-continue @click="setup">{{t('apps.retry')}}</Button></Space>
   <template v-if="state==='running'||state==='starting'||state==='waiting'||state==='backoff'">
    <Callout v-if="state==='starting'" tone="info" :title="n('state.starting')" data-nbserver-starting>{{n('startingHelp')}}</Callout>
    <!-- Devices reach Signal only through the bridge, which needs the Workers token. -->
    <template v-if="TOKEN_CODES.has(missing)">
     <Callout tone="warning" :title="dependencyText(missing,t)" :dismiss-key="missing" data-nbserver-bridge>{{codeText(missing)}}</Callout>
     <NetBirdServerToken :edit="missing===PERMISSION||missing==='workers_permission_denied'" data-nbserver-bridge-form @saved="tokenSaved" @check="setup" />
    </template>
    <Space direction="column" spacing="dense" data-nbserver-status>
     <KeyValueRow :label="n('row.url')"><CopyCutField :model-value="status.managementUrl??''" readonly :copy-label="t('ux.copy')" :aria-label="n('row.url')" data-nbserver-url /></KeyValueRow>
     <KeyValueRow v-if="status.version" :label="n('row.version')" :value="status.version" />
     <KeyValueRow v-if="peers" :label="n('row.peers')" :value="peers" data-nbserver-peers />
     <KeyValueRow v-if="uptime" :label="n('row.uptime')" :value="uptime" />
     <KeyValueRow v-if="status.memoryBytes" :label="n('row.memory')" :value="size(status.memoryBytes)" data-nbserver-memory />
     <KeyValueRow v-if="status.dataBytes" :label="n('row.data')" :value="size(status.dataBytes)" />
     <KeyValueRow v-if="status.adminEmail" :label="n('row.admin')" :value="status.adminEmail" />
    </Space>
    <CopyCutField v-if="password" :model-value="password" readonly :copy-label="n('copyPassword')" :aria-label="n('password')" data-nbserver-password />
    <span v-if="status.joined&&!status.dependencies?.problem&&!status.dependencies?.checking" data-nbserver-joined>{{n('joined')}}</span>
    <FormNote>{{n('devicesHelp')}}</FormNote>
    <span data-nbserver-dashboard-guide>{{n('dashboardHelp')}} <Link href="https://github.com/synopackageland/syno-pkg-land-connect/blob/main/docs/netbird-server.md#optional-web-dashboard" external>{{t('ux.learnMore')}}</Link></span>
    <Space>
     <Button v-if="status.adminEmail&&!password" data-nbserver-show-password @click="showPassword">{{n('showPassword')}}</Button>
     <Button variant="secondary" :disabled="sending" data-nbserver-off @click="offOpen=true">{{n('offAction')}}</Button>
    </Space>
   </template>
  </template>
  <Dialog v-model:open="offOpen" :title="n('offTitle')" data-nbserver-off-dialog>
   <Space direction="column" spacing="regular">
    <span>{{n('offLine')}}</span>
    <Callout v-if="actionError" tone="danger" :code="actionError.code" data-nbserver-action-error>{{actionError.text}}</Callout>
    <Checkbox v-model="removeData" :label="n('removeData')" data-nbserver-remove-data />
   </Space>
   <template #footer><Button :disabled="turningOff" @click="offOpen=false">{{t('apps.cancel')}}</Button><Button variant="danger" :loading="turningOff" :disabled="turningOff" data-nbserver-off-confirm @click="turnOff">{{n('offAction')}}</Button></template>
  </Dialog>
 </Space>
</template>
