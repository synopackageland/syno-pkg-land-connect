<script setup lang="ts">
import {computed,ref,watch} from 'vue'
import {Button,Callout,ConnectionTopology,Space,type TopologyLane,type TopologyNode} from '@synopackageland/ui'
import {Cloud,Monitor,Server} from '@lucide/vue'
import {useConnectState} from '../connect/state'
import {dependencyLinks,dependencyText,type DependencyReport} from '../connect/dependency'
import {grpcSettingsUrl,serverApi,type ServerStatus} from '../connect/netbirdServer'
import {translate} from '../i18n'
import {useServerStatus} from '../connect/serverStatus'
const props=defineProps<{report?:DependencyReport;unavailable?:boolean;path?:boolean;job?:ServerStatus['job']}>()
const emit=defineEmits<{refresh:[]}>()
const shared=useServerStatus()
const s=useConnectState(),failure=ref<{code:string;text:string}>(),busy=ref(false)
const t=(k:string)=>translate(s.ctx.locale,k)
const issue=computed(()=>props.unavailable?{code:'unavailable',action:'check_status',page:'netbird' as const}:props.report?.problem)
// A healthy dependency shows nothing; the stop and turn-off dialogs explain it where it matters.
// Closing a problem hides it until its code changes or it clears and comes back.
const shown=computed(()=>props.report?.enabled?issue.value:undefined)
const dismissed=ref<string>()
watch(()=>shown.value?.code,code=>{if(code!==dismissed.value)dismissed.value=undefined})
const message=computed(()=>shown.value?dependencyText(shown.value.code,t):'')
const target=computed<TopologyNode>(()=>({id:'server',label:t('dependency.path.server'),icon:Server}))
const lanes=computed<TopologyLane[]>(()=>['management','signal'].map(way=>({id:way,
 source:{id:'devices-'+way,label:t('dependency.path.devices'),icon:Monitor},
 via:{id:way,label:t('dependency.path.'+way),icon:Cloud},
 links:dependencyLinks(props.report,way as 'management'|'signal').map(l=>({state:l.state,label:l.label?t(l.label):''})) as TopologyLane['links'],
})))
async function act(){
 failure.value=undefined
 const p=shown.value
 if(!p)return
 if(props.job){s.go('netbird',{tab:'server'});return}
 if(p.action==='review_bridge'){window.open('https://dash.cloudflare.com/','_blank','noopener');return}
 if(p.action==='enable_grpc'&&props.report?.zone){window.open(grpcSettingsUrl(props.report.zone),'_blank','noopener');return}
 if(p.action==='repair_bridge'){
  busy.value=true
  try{const r=await s.ctx.call<ServerStatus>(serverApi.setup,{});shared?.accept(r);emit('refresh')}
  catch(e){const code=(e as {code?:string})?.code??'unavailable';failure.value={code,text:dependencyText(code,t)}}
  finally{busy.value=false}
 }else if((p.action==='check_token'||p.action==='check_status'))emit('refresh')
 else s.go(p.page,{tab:p.page==='netbird'?'server':'domain'})
}
</script>
<template>
 <Space v-if="report&&shown&&dismissed!==shown.code" direction="column" spacing="regular" data-server-dependency>
  <Callout tone="warning" :title="message" :dismiss-key="shown.code" data-dependency-notice @close="dismissed=shown.code;failure=undefined">
   <span>{{job?.step?t('nbserver.progress').replace('{step}',t('nbserver.step.'+job.step)):t('dependency.cloudflareUsed')}}</span>
   <template #actions><Button :loading="busy" :disabled="busy" data-dependency-action @click="act">{{t(job?'nbserver.viewProgress':'dependency.action.'+shown.action)}}</Button></template>
  </Callout>
  <Callout v-if="failure" tone="danger" :code="failure.code" data-dependency-error>{{failure.text}}</Callout>
  <ConnectionTopology v-if="path&&report.problem" :target="target" :lanes="lanes" :selectable="false" :aria-label="t('nbserver.tab')" data-server-topology />
 </Space>
</template>
