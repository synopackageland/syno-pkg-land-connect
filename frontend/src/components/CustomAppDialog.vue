<script setup lang="ts">
import {readDsmPorts, type DsmPort} from '@synopackageland/sdk'
import ConnectionError from './ConnectionError.vue'

// Add or edit a custom app: any web service on the NAS or the LAN, by URL.
// One compact form and one primary action. "From this NAS" only fills the
// URL and name. DSM sign-in stays on unless the administrator turns it off
// and confirms the risk in the same form; the path picture shows what that
// changes. Saving shares nothing: the app then opens with the free link or the
// user's own domain through the ordinary approval. Edit also removes the app,
// after one confirmation, which deletes its Worker or DNS record.
import {computed,ref,watch} from 'vue'
import {Button,Callout,Checkbox,Dialog,Form,FormItem,FormNote,Input,Select,Space,Spinner,Switch,useToast} from '@synopackageland/ui'
import {useConnectState} from '../connect/state'
import {appsApi,type AppRow} from '../connect/appUrls'
import {readSynologyPortals,candidateName,candidatePort,knownCandidate,customForm,customRequest,isHttps,nameValid,needsRisk,urlError,type Candidate,type CustomForm} from '../connect/customApps'
import {translate} from '../i18n'
import AppIcon from './AppIcon.vue'
import CustomGatePath from './CustomGatePath.vue'
const props=defineProps<{row?:AppRow}>()
const open=defineModel<boolean>('open',{default:false})
const emit=defineEmits<{saved:[id:string];removed:[];choose:[id:string]}>()
const s=useConnectState()
const toast=useToast()
const t=(key:string)=>translate(s.ctx.locale,'apps.custom.'+key)

const form=ref<CustomForm>(customForm())
const step=ref<'form'|'remove'>('form')
const busy=ref(false),error=ref(''),touched=ref(false)
const candidates=ref<Candidate[]>([]),dsmPorts=ref<DsmPort[]>([]),picked=ref(''),candidateQuery=ref('')
const candidatesLoading=ref(false),candidatesError=ref('')
const candidateError=(e:unknown)=>{
 if(e instanceof Error)return e.message
 if(typeof e==='object'&&e!==null){
  const detail='message' in e&&typeof e.message==='string'?e.message:s.message(e)
  return 'code' in e&&typeof e.code==='string'?`${detail} (${e.code})`:detail
 }
 return s.message(e)
}
let loads=0
async function loadCandidates(){
 const current=++loads
 candidatesLoading.value=true;candidatesError.value='';candidates.value=[];dsmPorts.value=[];picked.value=''
 void readDsmPorts(s.ctx).then(list=>{if(current===loads)dsmPorts.value=list}).catch(()=>{})
 try{
  let portals:unknown[]=[]
  try{portals=await readSynologyPortals(s.ctx)}
  catch(e){if(current===loads)candidatesError.value=candidateError(e)}
  const out=await s.ctx.call<{candidates:Candidate[]}>(appsApi.customCandidates,portals.length?{portals}:{})
  if(current===loads)candidates.value=out.candidates??[]
 }catch(e){if(current===loads)candidatesError.value=candidateError(e)}
 finally{if(current===loads)candidatesLoading.value=false}
}
// Closing invalidates pending results; every open starts an independent read.
watch(open,now=>{
 if(!now){++loads;return}
 form.value=customForm(props.row);step.value='form';error.value='';touched.value=false;picked.value='';candidateQuery.value=''
 void loadCandidates()
},{immediate:true})

const editing=computed(()=>!!props.row)
const gate=computed({get:()=>!form.value.appSignIn,set:on=>{form.value.appSignIn=!on;if(on)form.value.acceptRisk=false}})
const known=computed(()=>!editing.value?knownCandidate(form.value.url,candidates.value):undefined)
const urlCode=computed(()=>known.value?'':urlError(form.value.url))
const risk=computed(()=>needsRisk(form.value,props.row))
const ready=computed(()=>!!known.value||!urlCode.value&&nameValid(form.value.name)&&(!risk.value||form.value.acceptRisk))
const sourceLabel=(c:Candidate)=>candidateName(c,dsmPorts.value)||t('pick.port').replace('{port}',candidatePort(c))
const options=computed(()=>candidates.value.map((c,i)=>({value:String(i),package:c.package,label:`${sourceLabel(c)} · ${c.url.replace(/^https?:\/\//,'')}`})).filter(c=>!c.package&&candidates.value[Number(c.value)]?.source!=='synology'))
const synologyCandidates=computed(()=>candidates.value.filter(c=>c.source==='synology'))
const packageCandidates=computed(()=>candidates.value.filter(c=>c.package&&(!candidateQuery.value||`${c.name} ${c.package}`.toLowerCase().includes(candidateQuery.value.toLowerCase()))))
const hasPackages=computed(()=>candidates.value.some(c=>c.package))
const candidatePath=(c:Candidate)=>{try{return new URL(c.url).pathname}catch{return ''}}
function choosePackage(c:Candidate){if(c.package){open.value=false;emit('choose',c.package)}}
function pick(value:string){
 picked.value=value
 const c=candidates.value[Number(value)]
 if(!c)return
 form.value.url=c.url
 if(!form.value.name.trim()||!editing.value)form.value.name=sourceLabel(c)
}

async function save(){
 touched.value=true
 if(known.value?.package){open.value=false;emit('choose',known.value.package);return}
 if(!ready.value||busy.value)return
 busy.value=true;error.value=''
 try{
  const out=await s.ctx.call<{id:string}>(editing.value?appsApi.customEdit:appsApi.customAdd,customRequest(form.value,props.row?.package))
  toast.success(t(editing.value?'saved':'added'))
  open.value=false
  emit('saved',out.id)
 }catch(e){error.value=s.message(e)}
 finally{busy.value=false}
}
async function remove(){
 if(!props.row||busy.value)return
 busy.value=true;error.value=''
 try{await s.ctx.call(appsApi.customRemove,{id:props.row.package});toast.success(t('removed'));open.value=false;emit('removed')}
 catch(e){error.value=s.message(e)}
 finally{busy.value=false}
}
</script>
<template>
 <Dialog v-model:open="open" :title="step==='remove'?t('remove.title').replace('{app}',row?.name??''):t(editing?'editTitle':'addTitle')" size="md" data-custom-dialog :data-custom-step="step">
  <Space v-if="step==='form'" direction="column" spacing="regular">
   <Form direction="horizontal" field-width="full" label-width="8.5rem" @submit.prevent="save">
    <FormItem :label="t('pick.label')" data-custom-pick>
     <Space v-if="candidatesLoading" align-items="center" spacing="compact" role="status" data-candidates-loading><Spinner inline :size="16" :label="t('pick.loading')" /><span>{{t('pick.loading')}}</span></Space>
     <Space v-if="candidatesError" direction="column" spacing="dense" data-candidates-error>
      <Callout tone="danger" variant="inline">{{candidatesError}}</Callout>
      <Button :disabled="candidatesLoading" data-candidates-retry @click="loadCandidates">{{t('pick.retry')}}</Button>
     </Space>
     <FormNote v-if="!candidatesLoading&&!candidatesError&&!candidates.length" data-candidates-empty>{{t('pick.empty')}}</FormNote>
     <Input v-if="hasPackages" v-model="candidateQuery" :placeholder="t('pick.placeholder')" :aria-label="t('pick.label')" :disabled="busy" data-package-search />
     <Space v-if="hasPackages" direction="column" spacing="dense" style="max-height:16rem;overflow:auto" data-package-candidates>
      <Space v-for="c in packageCandidates" :key="c.package" align-items="center" spacing="compact" :data-package-candidate="c.package"><AppIcon :src="c.icon" /><Button :disabled="busy" @click="choosePackage(c)">{{c.name}}</Button><span>{{candidatePath(c)}}</span></Space>
     </Space>
     <Space v-if="synologyCandidates.length" direction="column" spacing="dense" style="max-height:12rem;overflow:auto" data-synology-candidates>
      <div v-for="c in synologyCandidates" :key="c.url" class="candidate-row"><AppIcon :src="c.icon" /><div class="candidate-detail"><Button :disabled="busy" @click="pick(String(candidates.indexOf(c)))"><span class="candidate-name">{{c.name}}</span></Button><span class="candidate-url">{{c.url.replace(/^https?:\/\//,'')}}</span></div></div>
     </Space>
     <Select v-if="options.length" :model-value="picked" :options="options" :placeholder="t('pick.placeholder')" :disabled="busy" search @update:model-value="pick" />
    </FormItem>
    <FormItem :label="t('url')" :validate-status="touched&&urlCode?'error':''" :help="touched&&urlCode?t('code.'+urlCode):t('urlHelp')" data-custom-url>
     <Input v-model="form.url" :disabled="busy" placeholder="http://127.0.0.1:8080" autocomplete="off" :invalid="touched&&!!urlCode" @blur="touched=true" />
    </FormItem>
    <FormItem :label="t('name')" :validate-status="touched&&!nameValid(form.name)?'error':''" :help="touched&&!nameValid(form.name)?t('code.custom_name_invalid'):''" data-custom-name>
     <Input v-model="form.name" :disabled="busy" autocomplete="off" maxlength="64" />
    </FormItem>
    <FormItem v-if="!known" :label="t('signIn')" data-custom-gate>
     <Space direction="column" spacing="dense">
      <Switch v-model="gate" :label="t('gate')" :disabled="busy" data-custom-gate-switch />
      <FormNote>{{t(gate?'gateHelp':'appSignInHelp')}}</FormNote>
     </Space>
    </FormItem>
    <FormItem v-if="!known&&isHttps(form.url)" :label="t('certificate')" data-custom-tls>
     <Checkbox v-model="form.insecureTls" :label="t('selfSigned')" :description="t('selfSignedHelp')" :disabled="busy" />
    </FormItem>
   </Form>
   <Space v-if="known" align-items="center" spacing="compact" data-known-package><AppIcon :src="known.icon" /><span>{{known.name}}</span></Space>
   <CustomGatePath v-else :locale="s.ctx.locale" :app="form.name.trim()" :gate="gate" />
   <template v-if="!known&&!gate">
    <Callout tone="warning" variant="inline" data-custom-risk>{{t('risk.line')}}</Callout>
    <Checkbox v-if="risk" v-model="form.acceptRisk" :label="t('risk.accept')" :disabled="busy" data-custom-accept />
   </template>
   <ConnectionError :message="error" scope="public" data-custom-error />
  </Space>
  <Space v-else direction="column" spacing="dense" data-custom-remove>
   <span>{{t('remove.line')}}</span>
   <span>{{t('remove.cloud')}}</span>
   <ConnectionError :message="error" scope="public" data-custom-error />
  </Space>
  <template #footer>
   <template v-if="step==='form'">
    <Button v-if="editing" :disabled="busy" data-custom-remove-start @click="step='remove';error=''">{{t('remove.action')}}</Button>
    <Button :disabled="busy" @click="open=false">{{t('cancel')}}</Button>
    <Button variant="primary" :loading="busy" :disabled="busy||touched&&!ready" data-custom-save @click="save">{{known?translate(s.ctx.locale,'apps.page.open'):t(editing?'save':'add')}}</Button>
   </template>
   <template v-else>
    <Button :disabled="busy" @click="step='form';error=''">{{t('back')}}</Button>
    <Button variant="danger" :loading="busy" :disabled="busy" data-custom-remove-confirm @click="remove">{{t('remove.action')}}</Button>
   </template>
  </template>
 </Dialog>
</template>

<style scoped>
.candidate-row{display:flex;gap:8px;align-items:flex-start}
.candidate-detail{display:flex;flex-direction:column;align-items:flex-start;gap:2px;min-width:0;flex:1}
.candidate-detail :deep(button){max-width:100%;height:auto;min-height:28px}
.candidate-name{white-space:normal;text-align:left}
.candidate-url{overflow-wrap:anywhere;color:var(--ui-text-secondary);font-size:12px}
</style>
