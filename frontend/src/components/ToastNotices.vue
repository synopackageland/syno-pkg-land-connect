<script setup lang="ts">
// Passing results become toasts and leave nothing behind: a start or stop
// still running, and a configuration change whose checks passed while the
// window was open. A change committed before the window opened says nothing.
// It wraps the pages inside ToastProvider, since useToast needs a provider above.
import {watch} from 'vue'
import {useToast} from '@synopackageland/ui'
import {useConnectState} from '../connect/state'
import {translate} from '../i18n'
const s=useConnectState()
const toast=useToast()
const t=(key:string)=>translate(s.ctx.locale,key)
watch(s.notice,text=>{if(text)toast.info(text)})
watch(()=>s.connections.value.cloudflare?.recoveryNotice,(notice,before)=>{
 if(notice==='cloudflare_tunnel_recreated'&&before!==notice)toast.success(t('cloudflare.tunnelRecreated'))
},{immediate:true})
const seen=new Map<string,string>()
watch(()=>Object.values(s.connections.value).map(c=>[c.provider,c.apply] as const),list=>{
 for(const [provider,apply] of list){
  const key=JSON.stringify(apply??null),before=seen.get(provider)
  seen.set(provider,key)
  if(before===undefined||before===key||apply?.phase!=='committed')continue
  const lines=[apply.addedPorts?.length?`${t('apply.added')} ${apply.addedPorts.join(', ')}`:'',apply.removedPorts?.length?`${t('apply.removed')} ${apply.removedPorts.join(', ')}`:''].filter(Boolean)
  toast.success(t('apply.phase.committed'),lines.join(' · ')||undefined)
 }
},{immediate:true})
</script>

<template><slot /></template>
