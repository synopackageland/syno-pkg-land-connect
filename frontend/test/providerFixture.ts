// A mounted Connect app whose routes answer from a status list and per-method handlers.
import {vi} from 'vitest'
import {mount} from '../src/main'
import {createStandaloneContext,type Context} from '@synopackageland/sdk'
import type {ConnectionStatus} from '../src/context'
import {translate} from '../src/i18n'
import {settle} from './support'

export type Handler=(params:Record<string,unknown>)=>unknown
const ports={detection:'ready',mappingState:'pending-provider',revision:1,entries:[{id:'dsm-http',name:'DSM HTTP',protocol:'http',port:5000,source:'dsm',enabled:true,state:'ready'}]}
export function fixture(connections:ConnectionStatus[]=[],handlers:Record<string,Handler>={},files?:Context['files'],locale='en'){
 const root=document.createElement('div');document.body.append(root)
 let status={provider:'multiple',state:'disconnected',simulation:false,shareUrl:'',connections} as unknown as ConnectionStatus
 const call=vi.fn(async(method:string,params:Record<string,unknown>={})=>{
  if(handlers[method])return handlers[method](params)
  if(method==='app.status')return {admin:true}
  if(method==='ports.list')return ports
  if(method==='netbirdserver.status')return {state:'off',dependencies:{enabled:false}}
  if(method==='connection.check')return {provider:params.provider,state:'not-checked'}
  if(method==='binaries.status')return {product:params.product,current:'1.0.0',phase:'idle'}
  return status
 })
 const ctx=createStandaloneContext({call,locale});ctx.files=files
 const app=mount(root,ctx)
 return {root,call,app,setStatus(next:ConnectionStatus[]){status={...status,connections:next} as ConnectionStatus},
  page:(name:string)=>root.querySelector<HTMLElement>(`[data-page="${name}"]`)!,
  async nav(label:string){[...root.querySelectorAll<HTMLElement>('[data-nav-item]')].find(n=>n.textContent?.trim()===label)!.click();await settle()},
  done(){app.unmount();root.remove()}}
}
export const t=(key:string)=>translate('en',key)
export const connected=(provider:string,extra:Partial<ConnectionStatus>={})=>({provider,state:'connected',simulation:false,shareUrl:'',...extra}) as ConnectionStatus

