// A changed DSM certificate has one review action in every surface. Inspection
// repairs an already-valid route; trust refreshes shared status before success.
import {failureToast} from './errorToast'
import {computed,ref} from 'vue'
import {useToast} from '@synopackageland/ui'
import {useConnectState} from './state'
import {appsApi,dsmNeedsReview,type DsmInspection} from './appUrls'
import {translate} from '../i18n'

export type DsmReviewProvider='cloudflare'|'tailscale'|'netbird'|'ipv6'

export function useDsmReview(provider:DsmReviewProvider){
 const s=useConnectState()
 const toast=useToast()
 const t=(key:string)=>translate(s.ctx.locale,'apps.'+key)
 const scope=provider==='tailscale'?'private' as const:'public' as const
 // Tailscale's own wording (dsmReview.private.*); Cloudflare keeps the original keys.
 const portChange=computed(()=>s.connectionFor(provider)?.dsmPortChange)
 const text=(key:'title'|'line'|'current'|'action')=>{
  const change=portChange.value
  if(change&&key!=='current')return t(`dsmPortChange.${key}`).replace('{from}',String(change.from)).replace('{to}',String(change.to))
  return t(scope==='private'&&key!=='action'?`dsmReview.private.${key}`:`dsmReview.${key}`)
 }
 const needed=computed(()=>!s.statusUnavailable.value&&(dsmNeedsReview(s.connectionFor(provider))||s.dsmErrorProvider.value===provider))
 const review=ref<DsmInspection>()
 const issue=ref('')
 const open=ref(false),reading=ref(false),approving=ref(false)
 async function inspect(){
  if(reading.value)return
  reading.value=true;issue.value=''
  try{
   const next=await s.ctx.call<DsmInspection>(appsApi.inspectDsm,{scope})
   if(next.dsm){review.value=next;open.value=true}
   else {await s.refresh();toast.info(text('current'))}
  }catch(e){issue.value=s.message(e);failureToast(toast,s,t('failed'),e)}
  finally{reading.value=false}
 }
 async function approve(){
  const r=review.value
  if(!r?.dsm||approving.value)return
  approving.value=true
  try{await s.ctx.call(appsApi.setDsm,{scope,dsm:r.dsm,token:r.token});open.value=false;s.dsmErrorProvider.value='';await s.refresh();toast.success(t('applied'))}
  catch(e){failureToast(toast,s,t('failed'),e)}
  finally{approving.value=false}
 }
 return {provider,scope,text,issue,action:computed(()=>text('action')),portChange,needed,review,open,reading,approving,inspect,approve}
}
