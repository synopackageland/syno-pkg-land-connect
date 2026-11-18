<script setup lang="ts">
// The setup wizard, the one place a way is set up: a dialog over the window,
// opened from the Overview (Set up, Add a way, Switch to Free link) and from
// App URLs (Set up a way). Choose a way, connect it, choose what opens with
// it, then the addresses and how to test them. It is the shared WizardDialog
// (DSM's wizard window: the step's title and progress in a fixed header,
// the hero, a fixed footer with Back and one main action at the end; it
// closes from the header only, and Compare all ways is a quiet text action at
// the footer's start); the steps
// stay this file's: where it opens comes from the backend only (wizard.ts
// resume), so a sign-in tab or a reload brings the user back to the same
// step; once the user picks something, or the dialog opens on a way, the
// wizard follows them. Nothing changes before the footer's main action goes
// on with the selected card. The temporary address has no card: a quiet row
// under the cards opens its Connect step, whose main action is Start.
import {computed,ref,watch} from 'vue'
import {Button,WizardDialog,WizardStep} from '@synopackageland/ui'
import {useConnectState} from '../../connect/state'
import {appsApi} from '../../connect/appUrls'
import {APP_SCOPE,MAIN_WAYS,MORE_WAYS,connectFailed,connectPhase,connectReady,pathWay,resume,stepAfterConnect,startTemporary,stepFor,temporaryAvailable,wayState,type WayState,type WizardStep as Step,type WizardWay} from '../../connect/wizard'
import type {RelayStatus} from '../../connect/topology'
import type {CheckResult} from '../../connect/checks'
import {translate} from '../../i18n'
import WayPath from '../WayPath.vue'
import WizardChoose from './WizardChoose.vue'
import WizardConnect from './WizardConnect.vue'
import WizardApps from './WizardApps.vue'
import WizardDone from './WizardDone.vue'
const s=useConnectState()
const t=(key:string)=>translate(s.ctx.locale,'wizard.'+key)
const open=s.wizardOpen

// workers.dev, read while the dialog is open: when it opens and after each refresh.
const relay=ref<RelayStatus>()
let reads=0
async function readRelay(){
 const mine=++reads
 try{const w=await s.ctx.call<RelayStatus>(appsApi.workersStatus,{});if(mine===reads)relay.value=typeof w?.connected==='boolean'?w:undefined}
 catch{if(mine===reads)relay.value=undefined}
}
watch(s.refreshes,()=>{if(open.value)void readRelay()})

const ways=[...MAIN_WAYS,...MORE_WAYS,'temporary'] as WizardWay[]
const states=computed(()=>Object.fromEntries(ways.map(w=>[w,wayState(w,s.connections.value,relay.value)])) as Record<WizardWay,WayState>)
const temporaryShown=computed(()=>temporaryAvailable(s.connections.value))

const way=ref<WizardWay>('workers'),step=ref<Step>('choose')
// What the Apps step approved, for the addresses on the last step.
const dsmHost=ref<string>()
// Until the user acts, the wizard follows what the backend reports.
let touched=false
const touch=()=>{touched=true}
// A way that comes on while its Connect step shows moves on by itself once
// it works: its connection check, which Connect shows, passed.
const check=ref<CheckResult>()
watch([way,step],()=>{check.value=undefined},{flush:'sync'})
watch(check,now=>{if(step.value==='connect'&&connectReady(way.value,states.value[way.value],now)){touch();step.value=stepAfterConnect(way.value)}})
watch(states,next=>{
 // A way that runs but does not work yet keeps its Connect step, setting up.
 if(step.value==='connect'&&next[way.value]==='on'){
  if(connectReady(way.value,next[way.value],check.value)){touch();step.value=stepAfterConnect(way.value)}
  return
 }
 if(touched)return
 const r=resume(next);way.value=r.way;step.value=r.step
},{immediate:true,flush:'sync'})
// Each opening starts afresh: where the backend says, or on the way asked for.
watch(open,now=>{
 if(!now)return
 touched=false;dsmHost.value=undefined
 const r=resume(states.value);way.value=r.way;step.value=r.step
 if(s.wizardWay.value){touch();way.value=s.wizardWay.value;step.value='choose'}
 void readRelay()
},{immediate:true})

function select(next:WizardWay){touch();way.value=next}
function choose(next:WizardWay){touch();way.value=next;dsmHost.value=undefined;step.value=stepFor(next,states.value[next])}
// Back from Connect or the Apps goes to the cards: a way is chosen again
// there, the Free link when the temporary address was the one.
function back(){touch();if(way.value==='temporary')way.value='workers';step.value='choose'}
function finished(host?:string){touch();dsmHost.value=host;step.value='done'}
function again(){touch();dsmHost.value=undefined;if(way.value==='temporary')way.value='workers';step.value='choose'}
// "Use Free link" under a temporary address: back to the cards, the Free link selected.
function useFreeLink(){touch();dsmHost.value=undefined;way.value='workers';step.value='choose'}
// Compare all ways is the Overview's folded comparison.
function compare(){open.value=false;s.go('status');s.compareRequests.value++}

const name=computed(()=>t('way.'+way.value))
const title=(key:Step)=>t('step.'+key).replace('{way}',name.value)
const lead=(key:Step)=>t('lead.'+key).replace('{way}',name.value)
const temporary=computed(()=>way.value==='temporary')

// The Apps step's main action (Continue or Skip) is the wizard's: it opens
// the approval, and the approval moves on to the addresses.
const apps=ref<InstanceType<typeof WizardApps>>()
async function reviewApps(){await apps.value?.next();return false as const}
// Choose's main action goes on with the selected card: a way already on to
// its Apps, any other to Connect.
const picker=ref<InstanceType<typeof WizardChoose>>()
const useLabel=computed(()=>t(states.value[way.value]==='on'?'continueWith':'use').replace('{way}',name.value))
function useSelected(){choose(way.value);return false as const}
// The temporary address's main action starts it; Connect moves on once it runs.
async function start(){touch();await startTemporary(s);return false as const}
// A check that still failed after the settling window: the user may go on.
const failed=computed(()=>connectFailed(way.value,states.value[way.value],check.value))
// Otherwise the way's form action (Check and connect, Sign in…) is the
// footer's main action; the footer stays busy while it runs.
const connect=ref<InstanceType<typeof WizardConnect>>()
const formAction=computed(()=>failed.value||temporary.value?undefined:connect.value?.primary)
const connectLabel=computed(()=>failed.value?t('connect.continueAnyway'):temporary.value?t('temporary.start'):formAction.value?.label)
const connectDisabled=computed(()=>formAction.value?formAction.value.disabled:s.busy.value)
async function connectNext(){
 if(failed.value){touch();step.value=stepAfterConnect(way.value);return false as const}
 if(temporary.value)return start()
 await formAction.value?.run();return false as const
}
</script>
<template>
 <WizardDialog
  v-model:open="open"
  :title="t('title')"
  :close-label="t('close')"
  :active-step-key="step"
  data-setup-wizard
  :data-wizard-step="step"
  :data-wizard-current="way"
  @update:active-step-key="step=$event as Step"
  @previous-step="back"
 >
  <WizardStep step-key="choose" :headline="title('choose')" :description="lead('choose')" :next-label="useLabel" :next-disabled="!!picker?.blocked" :pre-enter-next-step="useSelected">
   <WizardChoose ref="picker" :locale="s.ctx.locale" :states="states" :selected="way" :temporary="temporaryShown" @select="select" @temporary="choose('temporary')" />
   <template #actions><Button variant="ghost" kind="tool" data-wizard-compare @click="compare">{{translate(s.ctx.locale,'home.compare')}}</Button></template>
  </WizardStep>
  <WizardStep
   step-key="connect"
   :headline="temporary?t('temporary.title'):title('connect')"
   :label="t('label.connect')"
   :description="temporary?t('temporary.lead'):lead('connect')"
   previous-step-key="choose"
   :show-next="temporary&&states.temporary==='off'||failed||!!formAction"
   :next-label="connectLabel"
   :next-disabled="connectDisabled"
   :pre-enter-next-step="connectNext"
   lazy
  >
   <template #hero><WayPath :way="pathWay(way)" :phase="connectPhase(way,states[way],check)" :locale="s.ctx.locale" /></template>
   <WizardConnect ref="connect" :way="way" :state="states[way]" :relay="relay" @relay="s.refresh()" @check="check=$event" />
  </WizardStep>
  <WizardStep
   v-if="APP_SCOPE[way]"
   step-key="apps"
   :headline="title('apps')"
   :label="t('label.apps')"
   :description="lead('apps')"
   previous-step-key="choose"
   next-step-key="done"
   :next-label="apps?.label"
   :next-disabled="!apps||apps.blocked"
   :pre-enter-next-step="reviewApps"
   lazy
  >
   <WizardApps ref="apps" :way="way" @done="finished" />
  </WizardStep>
  <WizardStep step-key="done" type="done" :headline="title('done')" :description="lead('done')" :next-label="t('done.finish')" lazy>
   <WizardDone :way="way" :dsm-host="dsmHost" @free-link="useFreeLink" />
   <template #actions><Button variant="ghost" kind="tool" data-wizard-again @click="again">{{t('done.again')}}</Button></template>
  </WizardStep>
 </WizardDialog>
</template>
