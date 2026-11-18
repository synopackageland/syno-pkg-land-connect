// One dependency result and poll owner for both provider pages in this App mount.
import {inject,onUnmounted,provide,ref,watch,type InjectionKey,type Ref} from 'vue'
import type {ConnectState} from './state'
import {pollDelay,serverApi,validStatus,type ServerStatus} from './netbirdServer'
interface ServerRead {status:Ref<ServerStatus|undefined>;loaded:Ref<boolean>;unavailable:Ref<boolean>;load():Promise<void>;accept(value:ServerStatus):void}
const key:InjectionKey<ServerRead>=Symbol('server-status')
const reads=new WeakMap<ConnectState,ServerRead>()
export function provideServerStatus(s:ConnectState,page:'netbird'|'cloudflare'):ServerRead{
 const existing=reads.get(s)
 if(existing){provide(key,existing);return existing}
 const active=()=>s.page.value===page||s.page.value===(page==='netbird'?'cloudflare':'netbird')
 const status=ref<ServerStatus>(),loaded=ref(false),unavailable=ref(false)
 let timer=0,disposed=false,pending:Promise<void>|undefined
 function stop(){window.clearTimeout(timer)}
 function load():Promise<void>{
  if(pending)return pending
  stop()
  pending=(async()=>{
   try{const r=await s.ctx.call<ServerStatus>(serverApi.status,{});if(validStatus(r)){status.value=r;unavailable.value=false}else unavailable.value=true}
   catch{unavailable.value=true}
   finally{loaded.value=true;pending=undefined;if(!disposed&&active())timer=window.setTimeout(()=>void load(),pollDelay(status.value?.state,status.value?.dependencies?.checking))}
  })()
  return pending
 }
 watch([()=>s.page.value,()=>s.refreshes.value],()=>{if(active())void load();else stop()},{immediate:true})
 onUnmounted(()=>{disposed=true;stop()})
 const accept=(value:ServerStatus)=>{if(validStatus(value)){status.value=value;loaded.value=true;unavailable.value=false}}
 const result={status,loaded,unavailable,load,accept};reads.set(s,result);provide(key,result);return result
}
export const useServerStatus=()=>inject(key,undefined)
