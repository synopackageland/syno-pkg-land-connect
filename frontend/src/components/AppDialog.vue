<script setup lang="ts">
// One App: every way it opens with, changed in one place. "Only my devices"
// holds a switch per private way that Connect drives (Tailscale) and the
// NetBird address, which needs no switch. "Public" is one choice, because an
// App has at most one public way: off, the user's own domain or the free
// workers.dev link. Save turns off what goes (confirmed with what goes and
// what stays, R18), then shows the approval of each way that comes in this
// same dialog: name, port, protection and certificate (R16a). A way that is
// not set up offers "Set up" instead of a disabled control: the public ways
// on the Cloudflare page, a private one in the setup guide. A custom app
// opens with the public ways only; Edit hands it to the add/edit dialog and
// Pause keeps its approval and name for Re-enable.
import {failureToast} from '../connect/errorToast'
import {computed,ref,watch} from 'vue'
import {Button,CopyCutField,Dialog,Form,FormItem,FormNote,InfoTip,KeyValueRow,Link,RadioGroup,Space,Switch,Tag,useToast} from '@synopackageland/ui'
import {useConnectState} from '../connect/state'
import {appsApi,protectionLines,rowStatus,singleOriginUrl,statusTone,pauseReasons,type AppRow,type PauseReason,type PublicScope,type Scope} from '../connect/appUrls'
import {netbirdUrl,planChange,publicChoice,type Plan,type PublicChoice,type WaysReady} from '../connect/appWays'
import {useApprovalFlow} from '../connect/approvalFlow'
import {openFreeLink} from '../connect/freeLink'
import {translate} from '../i18n'
import HostnameInput from './HostnameInput.vue'
import NameConflictRow from './NameConflictRow.vue'
import AppEntryChange from './AppEntryChange.vue'
import ConnectionError from './ConnectionError.vue'
import ApprovalDetails from './ApprovalDetails.vue'
import ShowMore from './ShowMore.vue'
const props=defineProps<{row:AppRow;ready:WaysReady;privateHost?:string;privateCode?:string;repairScope?:Scope}>()
const open=defineModel<boolean>('open',{default:false})
const emit=defineEmits<{changed:[];edit:[row:AppRow]}>()
const s=useConnectState()
const toast=useToast()
const t=(key:string)=>translate(s.ctx.locale,'apps.'+key)
const flow=useApprovalFlow(s)

type Step='edit'|'confirm'|'single'|'approve'
const step=ref<Step>('edit')
const draft=ref<{private:boolean;public:PublicChoice}>({private:false,public:'off'})
const queue=ref<Plan>({off:[],on:[]})
const clearing=ref(false),reenabling=ref(false),busy=ref(false)
let changed=false
const dsmScope=computed<PublicScope>(()=>props.row.relay?'relay':'public')
const dsmHost=computed(()=>props.row[dsmScope.value]!.url.replace(/^https:\/\//,'').replace(/\/.*$/,''))
// Each open starts from what the NAS has now.
watch(open,now=>{
 if(now){draft.value={private:!!props.row.private,public:publicChoice(props.row)};step.value='edit';queue.value={off:[],on:[]};clearing.value=false;reenabling.value=false;changed=false;flow.reset();flow.approval.value=undefined;if(props.row.dsm)void inspectName();else if(props.repairScope)void reenable(props.repairScope)}
 else if(changed){changed=false;emit('changed')}
},{immediate:true})

const nameDirty=computed(()=>!!props.row.dsm&&(flow.hostnames.value.dsm??dsmHost.value)!==dsmHost.value)
async function inspectName(){
 busy.value=true
 try{await flow.inspect(['dsm'],dsmScope.value)}
 catch(e){flow.error.value=s.message(e);flow.valid.value=false}
 finally{busy.value=false}
}
async function saveName(){
 const failed=await flow.approve()
 if(failed===false)return
 changed=true;await s.refresh();finish()
}
function turnOffDsm(){open.value=false;if(dsmScope.value==='relay')openFreeLink(s);else s.go('cloudflare')}
const status=(scope:Scope)=>rowStatus(props.row,scope)
const removed=computed(()=>status('private')==='removed')
const single=computed(()=>!!props.row.external?.single_origin)
const plan=computed(()=>planChange(props.row,draft.value))
const dirty=computed(()=>plan.value.off.length+plan.value.on.length>0)
const reasonLine=(scope:Scope)=>{
 const st=status(scope)
 if(st==='paused'){const reason=props.row[scope]?.reason;return t('reason.'+(pauseReasons.includes(reason as PauseReason)?reason:'unknown'))}
 return ['waiting','cleanup'].includes(st)&&props.row[scope]?t('line.'+st):''
}
const publicOptions=computed(()=>{
 const capable=!!props.row.publicCapable&&!props.row.needsUpdate
 const option=(scope:PublicScope)=>{
  const url=props.row[scope]?.url
  const ready=scope==='relay'?props.ready.relay:props.ready.public
  const description=url??(!ready?t('dialog.notSetUp'):scope==='relay'&&props.row.media?t('relay.mediaRow'):'')
  return {label:t('way.'+scope),value:scope,disabled:!capable||(!ready&&!props.row[scope]),...(description?{description}:{})}
 }
 return [{label:t('status.off'),value:'off',disabled:!capable&&!props.row.public&&!props.row.relay},option('relay'),option('public')]
})
// Public ways not set up, both at once as one line; each opens the Cloudflare page.
const publicSetUp=computed(()=>{
 if(!props.row.publicCapable||props.row.needsUpdate)return []
 const missing=(['relay','public'] as const).filter(scope=>!props.ready[scope]&&!props.row[scope])
 return missing.length===2?['all' as const]:missing
})
function setUpPublic(scope:PublicScope|'all'){open.value=false;if(scope==='relay')openFreeLink(s);else s.go('cloudflare')}

// Save: what goes first, then what comes.
function save(){
 if(!dirty.value)return void(open.value=false)
 queue.value={off:[...plan.value.off],on:[...plan.value.on]}
 if(queue.value.off.length)step.value='confirm'
 else void next()
}
async function confirmed(){
 busy.value=true
 try{
  if(clearing.value){await s.ctx.call(appsApi.clear,{package:props.row.package});changed=true;return finish()}
  for(const scope of queue.value.off){
   if(scope==='private')await s.ctx.call(appsApi.setPrivate,{package:props.row.package,enabled:false})
   else await s.ctx.call(scope==='relay'?appsApi.setRelay:appsApi.setPublic,{packages:[props.row.package],enabled:false})
   changed=true
  }
  queue.value={...queue.value,off:[]}
  await next()
 }catch(e){failureToast(toast,s,t('failed'),e);open.value=false}
 finally{busy.value=false}
}
const singleShown=new Set<Scope>()
async function next(){
 const scope=queue.value.on[0]
 if(!scope)return finish()
 // A single-origin App signs in at one address: say so before it gets another.
 if(single.value&&!singleShown.has(scope)){singleShown.add(scope);step.value='single';return}
 await approveStep(scope,false)
}
async function approveStep(scope:Scope,reenable:boolean){
 busy.value=true
 try{
  const waiting=await flow.inspect([props.row.package],scope,reenable)
  if(waiting.length){toast.info(t('notReady'),waiting.join(', '));return finish()}
  step.value='approve'
 }catch(e){failureToast(toast,s,t('failed'),e);finish()}
 finally{busy.value=false}
}
async function approve(){
 const scope=flow.approval.value?.scope
 const failed=await flow.approve()
 if(failed===false)return
 changed=true
 if(failed[props.row.package])toast.danger(t('failed'),t(scope==='relay'?'relay.failed':'public.dnsFailed'))
 if(reenabling.value)return finish()
 queue.value={...queue.value,on:queue.value.on.slice(1)}
 await next()
}
// Pause a custom app's public way; Re-enable resumes it with a fresh approval.
const pausable=computed(()=>!!props.row.custom&&(['relay','public'] as const).some(scope=>status(scope)==='on'))
async function pause(){
 busy.value=true
 try{await s.ctx.call(appsApi.customPause,{id:props.row.package});changed=true;finish()}
 catch(e){failureToast(toast,s,t('failed'),e)}
 finally{busy.value=false}
}
function finish(){
 if(changed)toast.success(t('applied'))
 open.value=false
}
function reenable(scope:Scope){reenabling.value=true;queue.value={off:[],on:[]};void approveStep(scope,true)}
function clear(){clearing.value=true;step.value='confirm'}
function back(){step.value='edit';clearing.value=false;reenabling.value=false;singleShown.clear()}

const singleScope=computed(()=>queue.value.on[0]??'private')
const singleUrl=computed(()=>singleOriginUrl(props.row,props.privateHost))
const offRules=computed(()=>clearing.value?['off.clearRule']:queue.value.off.map(scope=>scope==='relay'?'off.relayRule':scope==='public'?'off.publicRule':'off.privateRule'))
const dnsLine=computed(()=>clearing.value?!!props.row.public:queue.value.off.includes('public'))
const approvalAction=computed(()=>{const a=flow.approval.value;return t(a?.reenable?'approve.reenableAction':a?.scope==='private'?'approve.privateAction':'approve.publicAction')})
const protection=computed(()=>protectionLines(props.row.protection,'private').map(l=>t('protection.'+l.key).replace('{paths}',l.paths??'')))
</script>
<template>
 <Dialog v-model:open="open" :title="row.name" size="md" data-app-dialog :data-app-dialog-step="step" :data-package="row.package">
  <Space v-if="step==='edit'" direction="column" spacing="regular">
   <AppEntryChange v-for="scope in (['private','public','relay'] as const).filter(k=>row[k]?.entryChange)" :key="scope" :locale="s.ctx.locale" :change="row[scope]!.entryChange!" :busy="busy" @update="reenable(scope)" />
   <span v-if="removed" data-dialog-line>{{t('line.removed')}}</span>
   <span v-else-if="row.needsUpdate||!row.publicCapable" data-dialog-line>{{t('line.needs-update')}}</span>
   <span v-else-if="row.custom?.reachable===false" data-dialog-line>{{t('custom.unreachable')}}</span>
   <span v-else-if="row.waiting&&!row.custom&&!row.private&&!row.public&&!row.relay" data-dialog-line>{{t('line.waiting')}}</span>
   <Space v-if="row.custom&&!removed" align-items="center" spacing="compact" wrap data-dialog-custom>
    <span data-dialog-custom-url>{{row.custom.url}}</span>
    <Tag :label="t(row.custom.appSignIn?'custom.appSignInTag':'custom.gateTag')" :appearance="row.custom.appSignIn?'warning':'success'" data-dialog-custom-gate />
    <Button :disabled="busy" data-dialog-custom-edit @click="emit('edit',row)">{{t('custom.edit')}}</Button>
   </Space>
   <Form v-if="row.dsm" direction="horizontal" field-width="full" data-dialog-dsm>
    <FormItem :label="t('hostname')" data-dialog-dsm-name>
     <HostnameInput :locale="s.ctx.locale" :hostname="dsmHost" :model-value="flow.hostnames.value.dsm??dsmHost" :disabled="busy||flow.approving.value" autocomplete="off" @update:model-value="flow.changeHostname('dsm',$event)" />
     <NameConflictRow v-if="flow.conflict.value" :locale="s.ctx.locale" :conflict="flow.conflict.value" :disabled="flow.approving.value" @reuse="flow.recoverName('reuse')" @use="name=>flow.recoverName('use',name)" @check="flow.recoverName('check')" />
     <ConnectionError v-else-if="!['required','length','format'].some(key=>flow.error.value===t('hostname.'+key))" :message="flow.error.value" :scope="dsmScope" />
    </FormItem>
    <FormItem :label="t('page.opensWith')"><Tag :label="t('way.chip.'+dsmScope)" appearance="neutral" /></FormItem>
    <FormItem :label="t('row.address')"><CopyCutField :model-value="row[dsmScope]!.url" :copy-label="t('copy')" /></FormItem>
    <FormItem :label="t('row.protection')"><span>{{t('dsm.signIn')}}</span></FormItem>
    <FormItem :label="t('turnOff')"><Button data-dialog-dsm-off @click="turnOffDsm">{{t('public.open')}}</Button></FormItem>
   </Form>
   <Form v-else-if="!removed" direction="horizontal" field-width="full">
    <FormItem v-if="!row.custom" :label="t('dialog.private')" data-dialog-private>
     <Space direction="column" spacing="dense">
      <Space v-if="ready.private||row.private" align-items="center" spacing="compact" wrap>
       <Switch v-model="draft.private" :aria-label="`${t('way.private')}: ${row.name}`" :disabled="busy||(!row.private&&!!row.needsUpdate)" data-dialog-switch />
       <span>{{t('way.private')}}</span>
       <Tag v-if="row.private&&status('private')!=='on'" :label="t('status.'+status('private'))" :appearance="statusTone[status('private')]" data-dialog-status="private" />
       <Button v-if="status('private')==='paused'" :disabled="busy" data-dialog-reenable="private" @click="reenable('private')">{{t('reenable')}}</Button>
      </Space>
      <span v-if="reasonLine('private')" data-dialog-reason="private">{{reasonLine('private')}}</span>
      <CopyCutField v-if="row.private?.url&&draft.private" :model-value="row.private.url" :copy-label="t('copy')" :aria-label="`${t('way.private')}: ${row.name}`" data-dialog-url="private" />
      <Space v-if="!ready.private&&!row.private" align-items="center" spacing="compact" data-dialog-setup="private">
       <template v-if="privateCode&&privateCode!=='tailscale-off'"><span>{{t('private.'+privateCode)}}</span><Button data-dialog-fix="private" @click="open=false;s.go('tailscale')">{{t('private.open')}}</Button></template>
       <template v-else><span>{{t('way.private')}} · {{t('dialog.notSetUp')}}</span><Button data-dialog-fix="private" @click="open=false;s.go('tailscale')">{{t('dialog.setUp')}}</Button></template>
      </Space>
      <Space v-if="ready.netbird" align-items="center" spacing="compact" data-dialog-netbird>
       <span>{{t('way.netbird')}}</span>
       <CopyCutField :model-value="netbirdUrl(ready.netbird,row.port,row.entry?.path)" :copy-label="t('copy')" :aria-label="`${t('way.netbird')}: ${row.name}`" data-dialog-url="netbird" />
       <InfoTip :content="t('netbird.certificate')" icon="info" />
      </Space>
     </Space>
    </FormItem>
    <FormItem :label="t('dialog.public')" data-dialog-public>
     <Space direction="column" spacing="dense">
      <RadioGroup v-model="draft.public" :options="publicOptions" :disabled="busy" data-dialog-choice />
      <template v-for="scope in (['relay','public'] as const)" :key="scope">
       <Space v-if="row[scope]&&status(scope)!=='on'" align-items="center" spacing="compact" wrap :data-dialog-state="scope">
        <Tag :label="t('status.'+status(scope))" :appearance="statusTone[status(scope)]" :data-dialog-status="scope" />
        <span v-if="reasonLine(scope)">{{reasonLine(scope)}}</span>
        <Button v-if="status(scope)==='paused'" :disabled="busy||(scope==='relay'?!ready.relay:!ready.public)" :data-dialog-reenable="scope" @click="reenable(scope)">{{t('reenable')}}</Button>
       </Space>
      </template>
      <Space v-if="pausable" align-items="center" spacing="compact" data-dialog-custom-pause-row>
       <Button variant="secondary" :disabled="busy" data-dialog-custom-pause @click="pause">{{t('custom.pause')}}</Button>
       <FormNote>{{t('custom.pauseHelp')}}</FormNote>
      </Space>
      <Space v-for="scope in publicSetUp" :key="scope" align-items="center" spacing="compact" wrap :data-dialog-setup="scope">
       <span>{{t(scope==='all'?'dialog.publicNotSetUp':'way.chip.'+scope)}}</span><Button :data-dialog-fix="scope" @click="setUpPublic(scope)">{{t('dialog.setUp')}}</Button>
      </Space>
     </Space>
    </FormItem>
   </Form>
   <Space v-if="single&&!removed" direction="column" spacing="none" data-dialog-single-origin>
    <span>{{t(row.private?'line.singleOriginSet':'dialog.singleOrigin')}}</span>
   </Space>
   <ShowMore v-if="!row.dsm" :title="t('dialog.details')">
    <Space direction="column" spacing="dense" data-dialog-details>
     <KeyValueRow v-if="!row.custom" :label="t('row.port')"><span>{{row.port}}</span></KeyValueRow>
     <KeyValueRow :label="t('row.protection')"><Space direction="column" spacing="none"><span v-for="line in protection" :key="line">{{line}}</span></Space></KeyValueRow>
     <Link v-if="row.settingsUrl&&!removed" :href="row.settingsUrl" external data-dialog-settings>{{t('settings')}}</Link>
    </Space>
   </ShowMore>
  </Space>

  <Space v-else-if="step==='confirm'" direction="column" spacing="dense" data-dialog-confirm>
   <span v-for="rule in offRules" :key="rule">{{t(rule)}}</span>
   <span v-if="dnsLine">{{t('off.dns')}} <Link href="https://dash.cloudflare.com/" external>{{t('off.dashboard')}}</Link></span>
  </Space>

  <Space v-else-if="step==='single'" direction="column" spacing="dense" data-dialog-single>
   <template v-if="singleScope==='private'">
    <span>{{t('single.rule')}}</span>
    <CopyCutField v-if="singleUrl" :model-value="singleUrl" :copy-label="t('copy')" :aria-label="t('single.address')" data-single-url />
    <span>{{t('single.step')}}</span>
   </template>
   <span v-else>{{t('dialog.singlePublic')}}</span>
   <Link v-if="row.settingsUrl" :href="row.settingsUrl" external data-single-settings>{{t('settings')}}</Link>
  </Space>

  <div v-else-if="step==='approve'&&flow.approval.value" data-apps-approval :data-scope="flow.approval.value.scope">
   <ApprovalDetails :locale="s.ctx.locale" :scope="flow.approval.value.scope" :items="flow.approval.value.items" :reenable="flow.approval.value.reenable" :busy="flow.approving.value" :hostnames="flow.hostnames.value" :error="flow.error.value" :conflict="flow.conflict.value" @recover="flow.recoverName" @hostname="flow.changeHostname" />
  </div>

  <template #footer>
   <template v-if="step==='edit'">
    <Button :disabled="busy" @click="open=false">{{t('cancel')}}</Button>
    <Button v-if="row.dsm" variant="primary" :loading="flow.approving.value" :disabled="busy||flow.checking.value||!flow.valid.value||!nameDirty" data-dialog-save @click="saveName">{{t('dialog.save')}}</Button>
    <Button v-else-if="removed" variant="primary" :disabled="busy" data-dialog-clear @click="clear">{{t('clear')}}</Button>
    <Button v-else variant="primary" :loading="busy" :disabled="busy||!dirty" data-dialog-save @click="save">{{t('dialog.save')}}</Button>
   </template>
   <template v-else-if="step==='confirm'">
    <Button :disabled="busy" @click="back">{{t('dialog.back')}}</Button>
    <Button variant="danger" :loading="busy" :disabled="busy" data-off-confirm @click="confirmed">{{t(clearing?'clear':'turnOff')}}</Button>
   </template>
   <template v-else-if="step==='single'">
    <Button :disabled="busy" @click="back">{{t('dialog.back')}}</Button>
    <Button variant="primary" :loading="busy" :disabled="busy" data-single-continue @click="approveStep(singleScope,false)">{{t('single.continue')}}</Button>
   </template>
   <template v-else>
    <Button :disabled="flow.approving.value" @click="open=false">{{t('cancel')}}</Button>
    <Button variant="primary" :loading="flow.approving.value" :disabled="flow.approving.value||flow.checking.value||!flow.valid.value" data-approve-confirm @click="approve">{{approvalAction}}</Button>
   </template>
  </template>
 </Dialog>
</template>
