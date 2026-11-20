<script setup lang="ts">
// The "switch to the local network" option, one row for every Cloudflare way
// (own domain, Free link, temporary address). Off by default. While it is on,
// a page opened through Cloudflare checks from the visitor's own browser
// whether it reaches DSM directly on the local network and, only then, offers
// DSM's local address. Saving takes effect at once; a failed save puts the
// switch back.
import {onMounted,ref} from 'vue'
import {InfoTip,Space,Switch,useToast} from '@synopackageland/ui'
import type {Context} from '@synopackageland/sdk'
import {lanSwitchApi,type LanSwitchStatus} from '../connect/lanSwitch'
import {translate} from '../i18n'
const props=defineProps<{ctx:Context}>()
const t=(key:string)=>translate(props.ctx.locale,'lanswitch.'+key)
const toast=useToast()
const view=ref<LanSwitchStatus>(),on=ref(false),busy=ref(false)
async function load(){
 try{const v=await props.ctx.call<LanSwitchStatus>(lanSwitchApi.get,{});if(typeof v?.enabled==='boolean'){view.value=v;on.value=v.enabled}}
 catch{/* the switch stays disabled until the setting is read */}
}
async function save(next:boolean){
 if(busy.value||!view.value)return
 busy.value=true
 try{const v=await props.ctx.call<LanSwitchStatus>(lanSwitchApi.set,{enabled:next});view.value=v;on.value=v.enabled}
 catch{on.value=view.value.enabled;toast.danger(t('unsaved'))}
 finally{busy.value=false}
}
onMounted(()=>void load())
defineExpose({refresh:load})
</script>
<template>
 <Space spacing="dense" wrap data-lan-switch>
  <Switch v-model="on" :aria-label="t('label')" :disabled="busy||!view" data-lan-switch-toggle @update:model-value="save" />
  <InfoTip icon="info" :content="t('detail').replace('{port}',String(view?.port||45001))" :aria-label="t('label')" data-lan-switch-info />
  <span v-if="view?.enabled&&!view.port" role="status" data-lan-switch-unavailable>{{t('unavailable')}}</span>
 </Space>
</template>
