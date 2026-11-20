// Runs the local-network check once for a page opened through Cloudflare and
// holds what it found; nothing is found anywhere else or when anything fails.
import {onBeforeUnmount,onMounted,ref} from 'vue'
import type {Context} from '@synopackageland/sdk'
import {findLan,lanSwitchApi,shouldProbe,type LanFound,type LanProbe} from './lanSwitch'

export function useLanFound(ctx:Context){
 const found=ref<LanFound>()
 const stop=new window.AbortController()
 onMounted(async()=>{
  if(!shouldProbe(window.location))return
  try{
   const probe=await ctx.call<LanProbe>(lanSwitchApi.probe,{})
   found.value=await findLan(probe,{signal:stop.signal})
  }catch{/* no offer: the page stays where it is */}
 })
 onBeforeUnmount(()=>stop.abort())
 return found
}
