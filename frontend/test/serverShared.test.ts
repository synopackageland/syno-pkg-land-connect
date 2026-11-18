// @vitest-environment jsdom
import {expect,test,vi} from 'vitest'
import {createApp,defineComponent,h,nextTick,ref} from 'vue'
import type {ConnectState} from '../src/connect/state'
import {provideServerStatus} from '../src/connect/serverStatus'

test('Cloudflare and NetBird share one live dependency result and one poll owner',async()=>{
 const call=vi.fn(async()=>({state:'failed',dependencies:{enabled:true,problem:{code:'netbird_server_bridge_missing'}}}))
 const s={page:ref('netbird'),refreshes:ref(0),ctx:{call}} as unknown as ConnectState
 const reads:unknown[]=[]
 const Page=defineComponent({props:['name'],setup(p){reads.push(provideServerStatus(s,p.name));return()=>h('div')}})
 const app=createApp({render:()=>h('div',[h(Page,{name:'cloudflare'}),h(Page,{name:'netbird'})])})
 const el=document.createElement('div');document.body.append(el);app.mount(el)
 try{await nextTick();await Promise.resolve();expect(reads[0]).toBe(reads[1]);expect(call).toHaveBeenCalledTimes(1)}finally{app.unmount();el.remove()}
})

test('a running server with pending dependency evidence polls before the observation expires',async()=>{
 vi.useFakeTimers()
 const call=vi.fn().mockResolvedValueOnce({state:'running',joined:true,dependencies:{enabled:true,checking:true}}).mockResolvedValue({state:'running',joined:true,dependencies:{enabled:true}})
 const s={page:ref('netbird'),refreshes:ref(0),ctx:{call}} as unknown as ConnectState
 let read:ReturnType<typeof provideServerStatus>|undefined
 const Page=defineComponent({setup(){read=provideServerStatus(s,'netbird');return()=>h('div')}})
 const app=createApp(Page),el=document.createElement('div');document.body.append(el);app.mount(el)
 try{
  await nextTick();await Promise.resolve();expect(read?.status.value?.dependencies?.checking).toBe(true)
  await vi.advanceTimersByTimeAsync(2500)
  expect(call).toHaveBeenCalledTimes(2)
  expect(read?.status.value?.dependencies?.checking).toBeUndefined()
 }finally{app.unmount();el.remove();vi.useRealTimers()}
})
